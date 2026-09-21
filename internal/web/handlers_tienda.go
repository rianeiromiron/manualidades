package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"manualidades/internal/inventario"
	"manualidades/internal/pasarela"
	"manualidades/internal/sitio"
	"manualidades/internal/tienda"
)

// renderTienda arma el layout público (con el logo/colores/contacto del
// negocio ya inyectados) y ejecuta la plantilla pedida encima.
func (a *App) renderTienda(w http.ResponseWriter, page string, data map[string]any) {
	cfg := sitio.Config{
		NombreNegocio: "Manualidades",
		ColorFondo:    "#FBE9EC",
		ColorTexto:    "#1F2A44",
		ColorMarco:    "#8A8F98",
		ColorAcento:   "#C1592B",
	}
	logoURL := ""
	if conn := a.DB(); conn != nil {
		if c, err := sitio.Get(conn); err == nil {
			cfg = c
			if cfg.LogoRuta != "" {
				logoURL = a.LogoStorage.URL(cfg.LogoRuta)
			}
		}
	}

	merged := map[string]any{"Sitio": cfg, "LogoURL": logoURL}
	for k, v := range data {
		merged[k] = v
	}

	tmpl := template.Must(template.ParseFiles("web/templates/layout_tienda.html", "web/templates/"+page))
	if err := tmpl.ExecuteTemplate(w, "layout_tienda", merged); err != nil {
		http.Error(w, "error interno: "+err.Error(), http.StatusInternalServerError)
	}
}

// requireTiendaDB devuelve la conexión activa, o una página pública de
// "no disponible" (distinta de la de admin) si todavía no hay base
// configurada.
func (a *App) requireTiendaDB(w http.ResponseWriter) *sql.DB {
	conn := a.DB()
	if conn == nil {
		a.renderTienda(w, "tienda_no_disponible.html", nil)
	}
	return conn
}

// conDisponibilidad llena Producto.Disponible: la existencia física menos lo
// que hoy retienen los pedidos en pago. Es lo que la tienda pública debe
// mostrar y ofrecer, no Stock.
func conDisponibilidad(conn *sql.DB, productos []inventario.Producto) error {
	reservado, err := tienda.ReservadoPorProducto(conn)
	if err != nil {
		return err
	}
	for i := range productos {
		d := productos[i].Stock - reservado[productos[i].ID]
		if d < 0 {
			d = 0
		}
		productos[i].Disponible = d
	}
	return nil
}

func productosActivos(conn *sql.DB) ([]inventario.Producto, error) {
	todos, err := inventario.ListProductos(conn)
	if err != nil {
		return nil, err
	}
	activos := make([]inventario.Producto, 0, len(todos))
	for _, p := range todos {
		if p.Activo {
			activos = append(activos, p)
		}
	}
	if err := conDisponibilidad(conn, activos); err != nil {
		return nil, err
	}
	return activos, nil
}

func (a *App) TiendaHome(w http.ResponseWriter, r *http.Request) {
	conn := a.requireTiendaDB(w)
	if conn == nil {
		return
	}
	productos, err := productosActivos(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	categorias, err := inventario.ListCategorias(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.renderTienda(w, "tienda_home.html", map[string]any{
		"Title":      "",
		"Productos":  productos,
		"Categorias": categorias,
	})
}

func (a *App) TiendaProducto(w http.ResponseWriter, r *http.Request) {
	conn := a.requireTiendaDB(w)
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	producto, err := inventario.GetProducto(conn, id)
	if err != nil || !producto.Activo {
		http.NotFound(w, r)
		return
	}
	unico := []inventario.Producto{producto}
	if err := conDisponibilidad(conn, unico); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	producto = unico[0]
	a.renderTienda(w, "tienda_producto.html", map[string]any{
		"Title":    producto.Nombre,
		"Producto": producto,
	})
}

func (a *App) TiendaCarrito(w http.ResponseWriter, r *http.Request) {
	if a.requireTiendaDB(w) == nil {
		return
	}
	a.renderTienda(w, "tienda_carrito.html", map[string]any{"Title": "Carrito"})
}

func (a *App) TiendaCheckout(w http.ResponseWriter, r *http.Request) {
	if a.requireTiendaDB(w) == nil {
		return
	}
	a.renderTienda(w, "tienda_checkout.html", map[string]any{"Title": "Checkout"})
}

type itemConfirmarJSON struct {
	ProductoID int     `json:"producto_id"`
	Cantidad   float64 `json:"cantidad"`
}

type checkoutConfirmarJSON struct {
	ClienteNombre     string              `json:"cliente_nombre"`
	ClienteTelefono   string              `json:"cliente_telefono"`
	ClienteEmail      string              `json:"cliente_email"`
	ClienteNIT        string              `json:"cliente_nit"`
	MetodoEntrega     string              `json:"metodo_entrega"`
	DireccionEntrega  string              `json:"direccion_entrega"`
	Notas             string              `json:"notas"`
	Items             []itemConfirmarJSON `json:"items"`
	TarjetaNumero     string              `json:"tarjeta_numero"`
	TarjetaTitular    string              `json:"tarjeta_titular"`
	TarjetaExpiracion string              `json:"tarjeta_expiracion"`
	TarjetaCVV        string              `json:"tarjeta_cvv"`
}

// cookiePedido guarda el token del pedido pendiente de este navegador, para
// que un reintento de pago (otra tarjeta, corregir un dato) reutilice el
// mismo pedido en vez de crear otro. Es HttpOnly: el JavaScript de la página
// no la necesita ni debe leerla.
const cookiePedido = "pedido_token"

func setCookiePedido(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookiePedido,
		Value:    token,
		Path:     "/",
		MaxAge:   int((tienda.TTLReserva + time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

func borrarCookiePedido(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookiePedido,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

// TiendaCheckoutConfirmar recibe el carrito completo (fetch/JSON) al
// presionar "Pagar" y ejecuta el flujo descrito en internal/tienda/checkout.go:
// primero RESERVA el stock (pedido en estado pagando), después le pide el
// cobro a la pasarela simulada (proyecto hermano "pasarela-simulada") y solo
// si aprueba confirma el pedido y descarga el kardex. Si el cobro falla, el
// pedido queda pendiente_pago con la reserva vigente y una cookie lo recuerda
// para que el cliente reintente sin crear otro pedido.
func (a *App) TiendaCheckoutConfirmar(w http.ResponseWriter, r *http.Request) {
	conn := a.DB()
	if conn == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Tienda no disponible."})
		return
	}

	var body checkoutConfirmarJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Solicitud inválida."})
		return
	}

	if body.ClienteNombre == "" || body.ClienteTelefono == "" {
		respondJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Nombre y teléfono son obligatorios."})
		return
	}
	if body.MetodoEntrega != "recoger" && body.MetodoEntrega != "domicilio" {
		respondJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Método de entrega inválido."})
		return
	}
	if body.MetodoEntrega == "domicilio" && body.DireccionEntrega == "" {
		respondJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Falta la dirección de entrega."})
		return
	}
	if body.TarjetaNumero == "" || body.TarjetaTitular == "" || body.TarjetaExpiracion == "" || body.TarjetaCVV == "" {
		respondJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Faltan datos de la tarjeta."})
		return
	}

	items := make([]tienda.ItemCarrito, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, tienda.ItemCarrito{ProductoID: it.ProductoID, Cantidad: it.Cantidad})
	}

	lineas, total, err := tienda.CotizarCarrito(conn, items)
	switch {
	case errors.Is(err, tienda.ErrCarritoVacio):
		respondJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El carrito está vacío."})
		return
	case err != nil:
		respondJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No se pudo cotizar el pedido."})
		return
	}

	// 1. Reservar el stock. Todavía no se cobra nada.
	tokenPrevio := ""
	if c, err := r.Cookie(cookiePedido); err == nil {
		tokenPrevio = c.Value
	}
	intento, err := tienda.IniciarPago(conn, tokenPrevio, tienda.DatosPedido{
		ClienteNombre:    body.ClienteNombre,
		ClienteTelefono:  body.ClienteTelefono,
		ClienteEmail:     body.ClienteEmail,
		ClienteNIT:       body.ClienteNIT,
		MetodoEntrega:    body.MetodoEntrega,
		DireccionEntrega: body.DireccionEntrega,
		Notas:            body.Notas,
	}, lineas, total)
	switch {
	case errors.Is(err, inventario.ErrStockInsuficiente):
		respondJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Ya no hay stock suficiente: " + err.Error() + ". No se te cobró nada."})
		return
	case errors.Is(err, tienda.ErrPedidoEnProceso):
		respondJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Tu pago ya se está procesando. Espera unos segundos."})
		return
	case err != nil:
		log.Printf("tienda: no se pudo reservar el pedido: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No se pudo iniciar el pago. No se te cobró nada."})
		return
	}
	setCookiePedido(w, r, intento.Token)

	// 2. Cobrar — nunca dentro de una transacción SQL.
	cobro, err := pasarela.Cobrar(pasarela.SolicitudCobro{
		Monto:         total,
		NumeroTarjeta: body.TarjetaNumero,
		NombreTitular: body.TarjetaTitular,
		Expiracion:    body.TarjetaExpiracion,
		CVV:           body.TarjetaCVV,
	})
	if err != nil {
		motivo := "No se pudo conectar con la pasarela de pago. Intenta de nuevo."
		if regErr := tienda.RegistrarCobroFallido(conn, intento, tienda.EstadoPagoFallo, "", motivo); regErr != nil {
			log.Printf("tienda: no se pudo registrar el fallo del cobro (pedido %d, pago %d): %v", intento.PedidoID, intento.PagoID, regErr)
		}
		respondJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": motivo + " Tu pedido sigue reservado unos minutos."})
		return
	}

	if !cobro.Aprobado {
		if regErr := tienda.RegistrarCobroFallido(conn, intento, tienda.EstadoPagoRechazado, cobro.Referencia, cobro.Motivo); regErr != nil {
			log.Printf("tienda: no se pudo registrar el rechazo (pedido %d, pago %d): %v", intento.PedidoID, intento.PagoID, regErr)
		}
		respondJSON(w, http.StatusPaymentRequired, map[string]any{
			"ok":    false,
			"error": "Pago rechazado: " + cobro.Motivo + ". Tu pedido sigue reservado unos minutos: puedes intentar con otra tarjeta.",
		})
		return
	}

	// 3. Confirmar: kardex + pago aprobado + pedido pagado, todo o nada.
	pago := tienda.ResultadoPago{Referencia: cobro.Referencia, Marca: cobro.Marca, Ultimos4: cobro.Ultimos4}
	if err := tienda.ConfirmarPago(conn, intento, pago); err != nil {
		// El cliente ya pagó pero el pedido no se pudo confirmar (ConfirmarPago
		// hizo rollback completo). Se deja constancia del cobro como "por
		// conciliar" para que el negocio lo reembolse o lo complete a mano.
		motivo := "Pedido no confirmado: " + err.Error()
		if regErr := tienda.MarcarPorConciliar(conn, intento, pago, motivo); regErr != nil {
			log.Printf("tienda: cobro aprobado sin pedido NI registro (ref %s, Q%.2f, pedido %d): confirmar: %v; registrar: %v",
				cobro.Referencia, total, intento.PedidoID, err, regErr)
		}
		borrarCookiePedido(w, r)
		msg := "El pago se aprobó, pero no se pudo confirmar el pedido. Contacta al negocio con la referencia " + cobro.Referencia + "."
		status := http.StatusInternalServerError
		if errors.Is(err, inventario.ErrStockInsuficiente) {
			msg = "Ya no hay stock suficiente (" + err.Error() + "). Tu pago se aprobó pero el pedido no se creó: el negocio te lo reembolsará. Referencia " + cobro.Referencia + "."
			status = http.StatusConflict
		}
		respondJSON(w, status, map[string]any{"ok": false, "error": msg})
		return
	}

	borrarCookiePedido(w, r)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "pedido_id": intento.PedidoID})
}

func (a *App) TiendaConfirmacion(w http.ResponseWriter, r *http.Request) {
	conn := a.requireTiendaDB(w)
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pedido, err := tienda.GetPedido(conn, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Un pedido que aún no está pagado (o que expiró) no tiene confirmación
	// que mostrar.
	switch pedido.Estado {
	case tienda.EstadoPendientePago, tienda.EstadoPagando, tienda.EstadoExpirado:
		http.NotFound(w, r)
		return
	}
	a.renderTienda(w, "tienda_confirmacion.html", map[string]any{
		"Title":  "Gracias por tu compra",
		"Pedido": pedido,
	})
}

func respondJSON(w http.ResponseWriter, status int, data map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
