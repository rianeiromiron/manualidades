package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/mux"

	"manualidades/internal/correo"
	"manualidades/internal/cuentas"
	"manualidades/internal/inventario"
	"manualidades/internal/storage"
	"manualidades/internal/tienda"
)

// Límites contra abuso. Crear un pedido por transferencia aparta stock hasta 48
// horas, así que se limita más que un checkout normal; subir archivos y pedir
// el reenvío de un enlace también se frenan por IP.
var (
	transferenciaLimiter = nuevoLimitador(6, time.Hour, time.Hour)
	comprobanteLimiter   = nuevoLimitador(10, time.Hour, time.Hour)
	consultaLimiter      = nuevoLimitador(5, 15*time.Minute, 15*time.Minute)
)

// ReiniciarLimitadores borra los contadores de intentos y de reenvíos. Existe
// solo para las pruebas, que comparten proceso y dirección IP.
func ReiniciarLimitadores() {
	for _, l := range []*limitador{transferenciaLimiter, comprobanteLimiter, consultaLimiter, checkoutLimiter} {
		l.mu.Lock()
		l.registros = make(map[string]*registroIntentos)
		l.mu.Unlock()
	}
	reenviosMu.Lock()
	reenvios = map[int]time.Time{}
	reenviosMu.Unlock()
}

// esperaReenvio es el mínimo entre dos reenvíos del enlace de un mismo pedido.
const esperaReenvio = 5 * time.Minute

var (
	reenviosMu sync.Mutex
	reenvios   = map[int]time.Time{}
)

// permiteReenvio dice si ya pasó el tiempo mínimo desde el último reenvío de
// ese pedido, y si es así lo anota.
func permiteReenvio(pedidoID int) bool {
	reenviosMu.Lock()
	defer reenviosMu.Unlock()
	if t, ok := reenvios[pedidoID]; ok && time.Since(t) < esperaReenvio {
		return false
	}
	reenvios[pedidoID] = time.Now()
	if len(reenvios) > 1000 { // evita que el mapa crezca sin límite
		for id, t := range reenvios {
			if time.Since(t) >= esperaReenvio {
				delete(reenvios, id)
			}
		}
	}
	return true
}

const (
	// maxFormularioComprobante limita lo que se lee de una subida: la imagen más
	// grande admitida más un margen para los demás campos.
	maxFormularioComprobante = storage.MaxBytesImagen + 1<<20
	patronToken              = `[0-9a-f-]{36}`
)

// almacenComprobantes devuelve dónde se guardan los comprobantes. Es una carpeta
// aparte de media/, que es la que se sirve al público: los comprobantes solo
// los descarga el admin.
func (a *App) almacenComprobantes() (storage.Storage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Comprobantes == nil {
		s, err := storage.NewLocal("comprobantes", "")
		if err != nil {
			return nil, err
		}
		a.Comprobantes = s
	}
	return a.Comprobantes, nil
}

// ---- Correo ----

// enviarCorreo manda un correo sin bloquear la respuesta al cliente. Un fallo
// solo se registra: no debe impedir que se cree un pedido ni que se suba un
// comprobante.
func (a *App) enviarCorreo(cfg correo.Config, m correo.Mensaje) {
	if !cfg.Configurado() {
		log.Printf("correo: no se envió «%s» porque el servidor de correo no está configurado", m.Asunto)
		return
	}
	enviar := a.EnviarCorreo
	if enviar == nil {
		enviar = correo.Enviar
	}
	go func() {
		if err := enviar(cfg, m); err != nil {
			var e *correo.ErrEnvio
			if errors.As(err, &e) {
				log.Printf("correo: falló «%s» en la etapa %s (código %d)", m.Asunto, e.Etapa, e.Codigo)
				return
			}
			log.Printf("correo: falló «%s»: %v", m.Asunto, err)
		}
	}()
}

// urlPublica es la base de los enlaces de los correos. Sale de la configuración,
// no de la petición: la cabecera Host la controla quien hace el pedido, y un
// enlace armado con ella permitiría mandar correos con enlaces a otro sitio.
// Solo en localhost (desarrollo) se usa la petición.
func urlPublica(r *http.Request, cfg correo.Config) string {
	if cfg.UrlTienda != "" {
		return cfg.UrlTienda
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		esquema := "http"
		if esConexionSegura(r) {
			esquema = "https"
		}
		return esquema + "://" + r.Host
	}
	return ""
}

// ---- Crear el pedido por transferencia ----

func campoSimple(valor string, max int) bool {
	if utf8.RuneCountInString(valor) > max {
		return false
	}
	for _, r := range valor {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func errorJSON(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]any{"ok": false, "error": msg})
}

// TiendaCheckoutTransferencia crea un pedido que espera una transferencia y
// reserva su stock. No cobra nada: el cliente transfiere por su cuenta y luego
// sube el comprobante en la página de su pedido.
func (a *App) TiendaCheckoutTransferencia(w http.ResponseWriter, r *http.Request) {
	conn := a.DB()
	if conn == nil {
		errorJSON(w, http.StatusServiceUnavailable, "Tienda no disponible.")
		return
	}
	ip := clientIP(r)
	if !transferenciaLimiter.Permitido(ip) {
		errorJSON(w, http.StatusTooManyRequests, "Demasiados intentos. Espera un rato e inténtalo de nuevo.")
		return
	}
	transferenciaLimiter.Contar(ip)

	r.Body = http.MaxBytesReader(w, r.Body, maxCheckoutBytes)
	var body checkoutConfirmarJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		errorJSON(w, http.StatusBadRequest, "Solicitud inválida.")
		return
	}
	body.ClienteNombre = strings.TrimSpace(body.ClienteNombre)
	body.ClienteTelefono = strings.TrimSpace(body.ClienteTelefono)
	body.ClienteEmail = strings.TrimSpace(body.ClienteEmail)
	body.ClienteNIT = strings.TrimSpace(body.ClienteNIT)
	body.DireccionEntrega = strings.TrimSpace(body.DireccionEntrega)
	body.Notas = strings.TrimSpace(body.Notas)

	if body.ClienteNombre == "" || body.ClienteTelefono == "" {
		errorJSON(w, http.StatusBadRequest, "Nombre y teléfono son obligatorios.")
		return
	}
	// El correo es obligatorio: ahí llega el enlace para volver al pedido.
	dir, err := mail.ParseAddress(body.ClienteEmail)
	if err != nil || dir.Address != body.ClienteEmail || !strings.Contains(body.ClienteEmail, "@") {
		errorJSON(w, http.StatusBadRequest, "Escribe un correo válido: ahí te enviaremos el enlace para subir tu comprobante.")
		return
	}
	if !campoSimple(body.ClienteNombre, 150) || !campoSimple(body.ClienteTelefono, 50) || !campoSimple(body.ClienteEmail, 150) ||
		!campoSimple(body.ClienteNIT, 20) || !campoSimple(body.DireccionEntrega, 255) || utf8.RuneCountInString(body.Notas) > 500 {
		errorJSON(w, http.StatusBadRequest, "Alguno de los datos es demasiado largo o tiene caracteres no válidos.")
		return
	}
	if body.MetodoEntrega != "recoger" && body.MetodoEntrega != "domicilio" {
		errorJSON(w, http.StatusBadRequest, "Método de entrega inválido.")
		return
	}
	if body.MetodoEntrega == "domicilio" && body.DireccionEntrega == "" {
		errorJSON(w, http.StatusBadRequest, "Falta la dirección de entrega.")
		return
	}

	activas, err := cuentas.ListActivas(conn)
	if err != nil {
		log.Printf("transferencia: leyendo cuentas: %v", err)
		errorJSON(w, http.StatusInternalServerError, "No se pudo crear el pedido. No se te cobró nada.")
		return
	}
	if len(activas) == 0 {
		errorJSON(w, http.StatusConflict, "El pago por transferencia no está disponible por ahora. Puedes pagar con tarjeta.")
		return
	}
	abiertas, err := tienda.TransferenciasAbiertas(conn, body.ClienteEmail)
	if err != nil {
		log.Printf("transferencia: contando pedidos abiertos: %v", err)
		errorJSON(w, http.StatusInternalServerError, "No se pudo crear el pedido.")
		return
	}
	if abiertas >= tienda.MaxTransferenciasAbiertas {
		errorJSON(w, http.StatusConflict, "Ya tienes varios pedidos por transferencia esperando pago con ese correo. Completa o espera a que venzan antes de hacer otro.")
		return
	}

	items := make([]tienda.ItemCarrito, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, tienda.ItemCarrito{ProductoID: it.ProductoID, Cantidad: it.Cantidad})
	}
	lineas, total, err := tienda.CotizarCarrito(conn, items)
	switch {
	case errors.Is(err, tienda.ErrCarritoVacio):
		errorJSON(w, http.StatusBadRequest, "El carrito está vacío.")
		return
	case err != nil:
		errorJSON(w, http.StatusInternalServerError, "No se pudo cotizar el pedido.")
		return
	}

	envio, err := costoEnvioPara(conn, body.MetodoEntrega)
	if err != nil {
		log.Printf("transferencia: leyendo el costo de envío: %v", err)
		errorJSON(w, http.StatusInternalServerError, "No se pudo cotizar el pedido.")
		return
	}
	total += envio

	tr, err := tienda.IniciarTransferencia(conn, tienda.DatosPedido{
		ClienteNombre: body.ClienteNombre, ClienteTelefono: body.ClienteTelefono, ClienteEmail: body.ClienteEmail,
		ClienteNIT: body.ClienteNIT, MetodoEntrega: body.MetodoEntrega, DireccionEntrega: body.DireccionEntrega, Notas: body.Notas,
		CostoEnvio: envio,
	}, lineas, total)
	switch {
	case errors.Is(err, inventario.ErrStockInsuficiente):
		errorJSON(w, http.StatusConflict, "Ya no hay stock suficiente: "+err.Error()+".")
		return
	case err != nil:
		log.Printf("transferencia: no se pudo crear el pedido: %v", err)
		errorJSON(w, http.StatusInternalServerError, "No se pudo crear el pedido.")
		return
	}

	// El correo con el enlace es un respaldo: el cliente ya lo recibe en pantalla.
	cfg, _ := correo.Load()
	if base := urlPublica(r, cfg); base != "" {
		if p, err := tienda.GetPedido(conn, tr.PedidoID); err == nil {
			a.enviarCorreo(cfg, correoPedidoCreado(p, activas, base+"/pedido/"+tr.Token))
		}
	} else {
		log.Printf("correo: no se envió el enlace del pedido %d: falta la dirección pública de la tienda (pantalla Correo)", tr.PedidoID)
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "pedido_id": tr.PedidoID, "redirect": "/pedido/" + tr.Token})
}

// ---- Página del pedido ----

// noCachear evita que el navegador o un proxy guarden páginas con datos del
// cliente o con el token en la URL.
func noCachear(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (a *App) renderPedidoTransferencia(w http.ResponseWriter, conn *sql.DB, p tienda.Pedido, token, message, kind string, status int) {
	comprobantes, err := tienda.ListComprobantes(conn, p.ID)
	if err != nil {
		log.Printf("transferencia: leyendo comprobantes del pedido %d: %v", p.ID, err)
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return
	}
	esperando := p.Estado == tienda.EstadoEsperaTransferencia && p.ExpiraEn != nil && p.ExpiraEn.After(time.Now())
	var activas []cuentas.Cuenta
	if esperando || p.Estado == tienda.EstadoTransferenciaPorRevisar {
		activas, err = cuentas.ListActivas(conn)
		if err != nil {
			log.Printf("transferencia: leyendo cuentas: %v", err)
			http.Error(w, "Error interno.", http.StatusInternalServerError)
			return
		}
	}
	noCachear(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	a.renderTienda(w, "tienda_pedido.html", map[string]any{
		"Title":        "Pedido #" + strconv.Itoa(p.ID),
		"Pedido":       p,
		"Token":        token,
		"Cuentas":      activas,
		"Comprobantes": comprobantes,
		"Esperando":    esperando,
		"PuedeSubir":   esperando && len(comprobantes) < tienda.MaxComprobantesPorPedido,
		"Hoy":          time.Now().Format("2006-01-02"),
		"Desde":        time.Now().AddDate(0, 0, -30).Format("2006-01-02"),
		"Message":      message,
		"MessageKind":  kind,
	})
}

func (a *App) pedidoDeLaRuta(w http.ResponseWriter, r *http.Request) (*sql.DB, tienda.Pedido, string, bool) {
	conn := a.requireTiendaDB(w)
	if conn == nil {
		return nil, tienda.Pedido{}, "", false
	}
	token := mux.Vars(r)["token"]
	p, err := tienda.PedidoPorToken(conn, token)
	if errors.Is(err, tienda.ErrPedidoNoExiste) {
		http.NotFound(w, r)
		return nil, tienda.Pedido{}, "", false
	}
	if err != nil {
		log.Printf("transferencia: leyendo el pedido: %v", err)
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return nil, tienda.Pedido{}, "", false
	}
	return conn, p, token, true
}

func (a *App) TiendaPedido(w http.ResponseWriter, r *http.Request) {
	conn, p, token, ok := a.pedidoDeLaRuta(w, r)
	if !ok {
		return
	}
	msg, kind := "", ""
	if r.URL.Query().Get("enviado") == "1" {
		msg, kind = "Recibimos tu comprobante. Lo revisaremos y te avisaremos por correo.", "success"
	}
	a.renderPedidoTransferencia(w, conn, p, token, msg, kind, http.StatusOK)
}

// TiendaComprobante recibe el comprobante: una imagen más los datos de la
// transferencia. Al guardarlo, el pedido pasa a revisión y deja de vencer.
func (a *App) TiendaComprobante(w http.ResponseWriter, r *http.Request) {
	conn, p, token, ok := a.pedidoDeLaRuta(w, r)
	if !ok {
		return
	}
	fallo := func(status int, msg string) {
		a.renderPedidoTransferencia(w, conn, p, token, msg, "error", status)
	}

	ip := clientIP(r)
	if !comprobanteLimiter.Permitido(ip) {
		fallo(http.StatusTooManyRequests, "Demasiados intentos. Espera un rato e inténtalo de nuevo.")
		return
	}
	comprobanteLimiter.Contar(ip)

	if !(p.Estado == tienda.EstadoEsperaTransferencia && p.ExpiraEn != nil && p.ExpiraEn.After(time.Now())) {
		fallo(http.StatusConflict, "Este pedido ya no admite comprobantes.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFormularioComprobante)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		fallo(http.StatusBadRequest, "No se pudo leer el formulario. Revisa que la imagen pese menos de 10 MB.")
		return
	}
	defer r.MultipartForm.RemoveAll()

	fecha, err := time.ParseInLocation("2006-01-02", r.FormValue("fecha"), time.Local)
	if err != nil {
		fallo(http.StatusBadRequest, "Indica la fecha de la transferencia.")
		return
	}
	datos, err := (tienda.DatosComprobante{
		NumeroBoleta: r.FormValue("numero_boleta"), BancoOrigen: r.FormValue("banco_origen"), Fecha: fecha,
	}).Limpiar(time.Now())
	if err != nil {
		fallo(http.StatusBadRequest, tienda.TextoComprobante(err))
		return
	}

	archivo, _, err := r.FormFile("comprobante")
	if err != nil {
		fallo(http.StatusBadRequest, "Adjunta la imagen del comprobante.")
		return
	}
	defer archivo.Close()

	almacen, err := a.almacenComprobantes()
	if err != nil {
		log.Printf("transferencia: preparando la carpeta de comprobantes: %v", err)
		fallo(http.StatusInternalServerError, "No se pudo guardar el comprobante. Inténtalo de nuevo.")
		return
	}
	nombre, err := almacen.Save("comprobante", archivo)
	if err != nil {
		if errors.Is(err, storage.ErrImagenInvalida) || errors.Is(err, storage.ErrImagenGrande) {
			fallo(http.StatusBadRequest, err.Error()+".")
			return
		}
		log.Printf("transferencia: guardando el comprobante: %v", err)
		fallo(http.StatusInternalServerError, "No se pudo guardar el comprobante. Inténtalo de nuevo.")
		return
	}

	_, err = tienda.RegistrarComprobante(conn, p.ID, nombre, datos, time.Now())
	if err != nil {
		almacen.Delete(nombre) // no quedan archivos huérfanos
		switch {
		case errors.Is(err, tienda.ErrComprobanteInvalido):
			fallo(http.StatusBadRequest, tienda.TextoComprobante(err))
		case errors.Is(err, tienda.ErrBoletaRepetida):
			fallo(http.StatusConflict, "Esa boleta ya está registrada. Si crees que es un error, comunícate con la tienda.")
		case errors.Is(err, tienda.ErrDemasiadosComprobantes):
			fallo(http.StatusConflict, "Ya subiste el máximo de comprobantes para este pedido.")
		case errors.Is(err, tienda.ErrPedidoNoAceptaComprobante):
			fallo(http.StatusConflict, "Este pedido ya no admite comprobantes.")
		default:
			log.Printf("transferencia: registrando el comprobante del pedido %d: %v", p.ID, err)
			fallo(http.StatusInternalServerError, "No se pudo registrar el comprobante. Inténtalo de nuevo.")
		}
		return
	}
	comprobanteLimiter.Limpiar(ip)

	// Aviso al negocio: que sepa que hay una transferencia por revisar.
	cfg, _ := correo.Load()
	if len(cfg.Destinatarios) > 0 {
		a.enviarCorreo(cfg, correoComprobanteRecibido(cfg.Destinatarios, p, datos, urlPublica(r, cfg)))
	} else {
		log.Printf("correo: hay una transferencia por revisar (pedido %d) pero no hay correos de aviso configurados", p.ID)
	}
	http.Redirect(w, r, "/pedido/"+token+"?enviado=1", http.StatusSeeOther)
}

// ---- Recuperar el enlace ----

const respuestaConsulta = "Si el número de pedido y el correo coinciden con un pedido por transferencia, te enviamos el enlace a ese correo. Revisa también la carpeta de spam."

func (a *App) TiendaConsultarPedido(w http.ResponseWriter, r *http.Request) {
	conn := a.requireTiendaDB(w)
	if conn == nil {
		return
	}
	datos := map[string]any{"Title": "Consultar mi pedido"}
	if r.Method == http.MethodPost {
		ip := clientIP(r)
		if !consultaLimiter.Permitido(ip) {
			noCachear(w)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			datos["Message"], datos["MessageKind"] = "Demasiados intentos. Espera unos minutos e inténtalo de nuevo.", "error"
			a.renderTienda(w, "tienda_consultar.html", datos)
			return
		}
		consultaLimiter.Contar(ip)

		if err := r.ParseForm(); err == nil {
			a.reenviarEnlace(r, conn, r.FormValue("numero"), r.FormValue("correo"))
		}
		// Misma respuesta coincida o no: así la pantalla no confirma qué pedidos
		// ni qué correos existen.
		datos["Message"], datos["MessageKind"] = respuestaConsulta, "success"
	}
	noCachear(w)
	a.renderTienda(w, "tienda_consultar.html", datos)
}

// reenviarEnlace manda el enlace del pedido al correo registrado en él, si los
// datos coinciden. Nunca devuelve ni muestra si coincidieron.
func (a *App) reenviarEnlace(r *http.Request, conn *sql.DB, numero, email string) {
	id, err := strconv.Atoi(strings.TrimSpace(numero))
	if err != nil || id <= 0 || len(email) > 254 {
		return
	}
	token, nombre, ok, err := tienda.TokenParaReenvio(conn, id, email)
	if err != nil {
		log.Printf("transferencia: buscando el pedido para reenviar el enlace: %v", err)
		return
	}
	if !ok || !permiteReenvio(id) {
		return
	}
	cfg, _ := correo.Load()
	base := urlPublica(r, cfg)
	if base == "" {
		log.Printf("correo: no se reenvió el enlace del pedido %d: falta la dirección pública de la tienda (pantalla Correo)", id)
		return
	}
	a.enviarCorreo(cfg, correoEnlacePedido(nombre, id, strings.TrimSpace(email), base+"/pedido/"+token))
}
