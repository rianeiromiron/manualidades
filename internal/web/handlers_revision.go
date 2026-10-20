package web

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/mux"

	"manualidades/internal/correo"
	"manualidades/internal/inventario"
	"manualidades/internal/tienda"
)

// Mensajes que muestra el detalle de un pedido tras una revisión. Se pasan por
// un código en la dirección (?r=...) y nunca como texto, para que nadie pueda
// hacer que la página muestre un mensaje cualquiera.
var mensajesRevision = map[string]string{
	"cancelado_liberado":     "Pedido cancelado. Se liberó la reserva de sus productos y se avisó al cliente por correo.",
	"cancelado_devuelto":     "Pedido cancelado. Sus productos volvieron al inventario.",
	"cancelado_sin_devolver": "Pedido cancelado. El inventario no se tocó porque el producto no volvió a bodega.",
	"reembolsado":            "Reembolso anotado.",
	"aprobado":               "Transferencia aprobada: el pedido quedó pagado y el inventario se descontó. Se avisó al cliente por correo.",
	"rechazado":              "Comprobante rechazado. El pedido espera un nuevo comprobante con un plazo nuevo de 48 horas, y se avisó al cliente por correo.",
	"cancelado":              "Comprobante rechazado y pedido cancelado: el inventario volvió a estar disponible y se avisó al cliente por correo.",
}

func idsDeLaRuta(r *http.Request) (pedido, comprobante int, ok bool) {
	v := mux.Vars(r)
	p, err1 := strconv.Atoi(v["id"])
	c, err2 := strconv.Atoi(v["cid"])
	return p, c, err1 == nil && err2 == nil
}

// renderPedidoDetalle arma el detalle de un pedido para el admin, con los
// comprobantes de transferencia si los tiene.
func (a *App) renderPedidoDetalle(w http.ResponseWriter, r *http.Request, conn *sql.DB, id int, message, kind string, status int) {
	pedido, err := tienda.GetPedido(conn, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var pago *tienda.Pago
	if p, err := tienda.GetPagoPorPedido(conn, id); err == nil {
		pago = &p
	}
	comprobantes, err := tienda.ListComprobantes(conn, id)
	if err != nil {
		log.Printf("pedidos: leyendo comprobantes del pedido %d: %v", id, err)
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return
	}
	var porRevisar *tienda.Comprobante
	if pedido.Estado == tienda.EstadoTransferenciaPorRevisar {
		for i := range comprobantes {
			if comprobantes[i].Estado == tienda.ComprobantePorRevisar {
				porRevisar = &comprobantes[i]
			}
		}
	}
	var cancelacion tienda.Cancelacion
	if pedido.Estado == tienda.EstadoCancelado {
		cancelacion, _ = tienda.GetCancelacion(conn, id)
	}
	// Cancelar tiene su propio botón (devuelve el inventario): no se ofrece en
	// la lista de estados.
	avanzables := make([]string, 0, len(tienda.EstadosValidos))
	for _, e := range tienda.EstadosValidos {
		if e != tienda.EstadoCancelado {
			avanzables = append(avanzables, e)
		}
	}
	w.WriteHeader(status)
	render(w, r, "pedido_detalle.html", map[string]any{
		"Title":          "Pedido",
		"Active":         "pedidos",
		"Pedido":         pedido,
		"EstadosValidos": avanzables,
		"Cancelacion":    cancelacion,
		"Pago":           pago,
		"Comprobantes":   comprobantes,
		"PorRevisar":     porRevisar,
		"Message":        message,
		"MessageKind":    kind,
	})
}

// PedidoComprobanteArchivo entrega la imagen de un comprobante. Solo llega aquí
// una sesión del admin con acceso a Pedidos; la imagen nunca se sirve desde una
// carpeta pública. Se busca por pedido y comprobante en la base (no por nombre
// de archivo recibido) y se entrega con cabeceras que impiden que el navegador
// la trate como otra cosa o la guarde.
func (a *App) PedidoComprobanteArchivo(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "pedidos")
	if conn == nil {
		return
	}
	pedidoID, compID, ok := idsDeLaRuta(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	lista, err := tienda.ListComprobantes(conn, pedidoID)
	if err != nil {
		log.Printf("pedidos: leyendo comprobantes del pedido %d: %v", pedidoID, err)
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return
	}
	var archivo string
	for _, c := range lista {
		if c.ID == compID {
			archivo = c.Archivo
		}
	}
	tipo := map[string]string{".jpg": "image/jpeg", ".png": "image/png"}[strings.ToLower(filepath.Ext(archivo))]
	if archivo == "" || tipo == "" {
		http.NotFound(w, r)
		return
	}
	almacen, err := a.almacenComprobantes()
	abridor, ok := almacen.(interface {
		Abrir(string) (*os.File, error)
	})
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	f, err := abridor.Abrir(archivo)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", tipo)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", "inline")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// ---- Aprobar / rechazar ----

func (a *App) PedidoTransferenciaAprobar(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "pedidos")
	if conn == nil {
		return
	}
	pedidoID, compID, ok := idsDeLaRuta(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	quien := quienCambia(r)
	err := tienda.AprobarTransferencia(conn, pedidoID, compID, quien.Usuario, time.Now())
	switch {
	case err == nil:
	case errors.Is(err, inventario.ErrStockInsuficiente):
		a.renderPedidoDetalle(w, r, conn, pedidoID,
			"No se puede aprobar: ya no hay stock suficiente ("+err.Error()+"). El pedido sigue en revisión. Si ya no puedes cumplirlo, recházalo y cancélalo, y devuelve el dinero al cliente.",
			"error", http.StatusConflict)
		return
	case errors.Is(err, tienda.ErrPedidoNoRevisable), errors.Is(err, tienda.ErrComprobanteNoRevisable), errors.Is(err, tienda.ErrPedidoNoExiste):
		a.renderPedidoDetalle(w, r, conn, pedidoID, "Este comprobante ya fue revisado o el pedido cambió. Revisa su estado actual.", "error", http.StatusConflict)
		return
	default:
		log.Printf("pedidos: aprobando la transferencia del pedido %d: %v", pedidoID, err)
		a.renderPedidoDetalle(w, r, conn, pedidoID, "No se pudo aprobar la transferencia. Inténtalo de nuevo.", "error", http.StatusInternalServerError)
		return
	}

	log.Printf("pedidos: transferencia del pedido %d aprobada por %q desde %s", pedidoID, quien.Usuario, quien.IP)
	if p, err := tienda.GetPedido(conn, pedidoID); err == nil {
		cfg, _ := correo.Load()
		a.enviarCorreo(cfg, correoTransferenciaAprobada(p, tokenEnlace(conn, pedidoID, urlPublica(r, cfg))))
	}
	http.Redirect(w, r, "/admin/pedidos/"+strconv.Itoa(pedidoID)+"?r=aprobado", http.StatusSeeOther)
}

func (a *App) PedidoTransferenciaRechazar(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "pedidos")
	if conn == nil {
		return
	}
	pedidoID, compID, ok := idsDeLaRuta(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulario inválido.", http.StatusBadRequest)
		return
	}
	cancelar := r.FormValue("accion") == "cancelar"
	motivo := strings.TrimSpace(r.FormValue("motivo"))
	quien := quienCambia(r)

	err := tienda.RechazarTransferencia(conn, pedidoID, compID, quien.Usuario, motivo, cancelar)
	switch {
	case err == nil:
	case errors.Is(err, tienda.ErrComprobanteInvalido):
		a.renderPedidoDetalle(w, r, conn, pedidoID, tienda.TextoComprobante(err), "error", http.StatusBadRequest)
		return
	case errors.Is(err, tienda.ErrPedidoNoRevisable), errors.Is(err, tienda.ErrComprobanteNoRevisable), errors.Is(err, tienda.ErrPedidoNoExiste):
		a.renderPedidoDetalle(w, r, conn, pedidoID, "Este comprobante ya fue revisado o el pedido cambió. Revisa su estado actual.", "error", http.StatusConflict)
		return
	default:
		log.Printf("pedidos: rechazando la transferencia del pedido %d: %v", pedidoID, err)
		a.renderPedidoDetalle(w, r, conn, pedidoID, "No se pudo rechazar el comprobante. Inténtalo de nuevo.", "error", http.StatusInternalServerError)
		return
	}

	resultado := "rechazado"
	if cancelar {
		resultado = "cancelado"
	}
	log.Printf("pedidos: comprobante del pedido %d rechazado (%s) por %q desde %s", pedidoID, resultado, quien.Usuario, quien.IP)
	if p, err := tienda.GetPedido(conn, pedidoID); err == nil {
		cfg, _ := correo.Load()
		a.enviarCorreo(cfg, correoTransferenciaRechazada(p, motivo, tokenEnlace(conn, pedidoID, urlPublica(r, cfg)), cancelar))
	}
	http.Redirect(w, r, "/admin/pedidos/"+strconv.Itoa(pedidoID)+"?r="+resultado, http.StatusSeeOther)
}

// tokenEnlace arma el enlace privado del pedido, o "" si no hay dirección
// pública configurada o el pedido no tiene token.
func tokenEnlace(conn *sql.DB, pedidoID int, base string) string {
	if base == "" {
		return ""
	}
	token, err := tienda.TokenDePedido(conn, pedidoID)
	if err != nil || token == "" {
		return ""
	}
	return base + "/pedido/" + token
}

// PedidoCancelar cancela un pedido ya pagado: devuelve sus productos al
// inventario (salvo que estuviera entregado y el producto no haya vuelto),
// deja pendiente el reembolso y no se puede deshacer.
func (a *App) PedidoCancelar(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "pedidos")
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulario inválido.", http.StatusBadRequest)
		return
	}
	var volvio *bool
	switch r.FormValue("volvio_a_bodega") {
	case "si":
		v := true
		volvio = &v
	case "no":
		v := false
		volvio = &v
	}

	// Motivo opcional, solo para el aviso al cliente cuando se cancela un pedido
	// que aún no se había pagado.
	motivo := strings.TrimSpace(r.FormValue("motivo"))
	if utf8.RuneCountInString(motivo) > 255 || !campoSimple(motivo, 255) {
		a.renderPedidoDetalle(w, r, conn, id, "El motivo es demasiado largo o tiene saltos de línea.", "error", http.StatusBadRequest)
		return
	}

	quien := quienCambia(r)
	res, err := tienda.CancelarPedido(conn, id, volvio, quien.Usuario, time.Now())
	switch {
	case err == nil:
	case errors.Is(err, tienda.ErrPedidoNoExiste):
		http.NotFound(w, r)
		return
	case errors.Is(err, tienda.ErrFaltaDecisionDeInventario):
		a.renderPedidoDetalle(w, r, conn, id, "Indica si el producto volvió a bodega para poder cancelar el pedido entregado.", "error", http.StatusBadRequest)
		return
	case errors.Is(err, tienda.ErrPedidoCancelado), errors.Is(err, tienda.ErrNoSePuedeCancelar), errors.Is(err, tienda.ErrEnRevision):
		a.renderPedidoDetalle(w, r, conn, id, "No se pudo cancelar: "+err.Error()+".", "error", http.StatusConflict)
		return
	default:
		log.Printf("pedidos: cancelando el pedido %d: %v", id, err)
		a.renderPedidoDetalle(w, r, conn, id, "No se pudo cancelar el pedido. Inténtalo de nuevo.", "error", http.StatusInternalServerError)
		return
	}

	log.Printf("pedidos: pedido %d cancelado por %q desde %s (inventario devuelto: %v, reembolso pendiente: Q%.2f)",
		id, quien.Usuario, quien.IP, res.InventarioDevuelto, res.ReembolsoPendiente)
	codigo := "cancelado_sin_devolver"
	switch {
	case res.ReservaLiberada:
		codigo = "cancelado_liberado"
		// El cliente no había pagado: se le avisa que su pedido se canceló.
		if p, err := tienda.GetPedido(conn, id); err == nil && p.ClienteEmail != "" {
			cfg, _ := correo.Load()
			a.enviarCorreo(cfg, correoPedidoCancelado(p, motivo))
		}
	case res.InventarioDevuelto:
		codigo = "cancelado_devuelto"
	}
	http.Redirect(w, r, "/admin/pedidos/"+strconv.Itoa(id)+"?r="+codigo, http.StatusSeeOther)
}

// PedidoReembolsado anota que ya se devolvió el dinero de un pedido cancelado.
func (a *App) PedidoReembolsado(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "pedidos")
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	quien := quienCambia(r)
	switch err := tienda.MarcarReembolsado(conn, id, quien.Usuario); {
	case err == nil:
	case errors.Is(err, tienda.ErrSinReembolsoPendiente):
		a.renderPedidoDetalle(w, r, conn, id, "Este pedido no tiene un reembolso pendiente.", "error", http.StatusConflict)
		return
	default:
		log.Printf("pedidos: anotando el reembolso del pedido %d: %v", id, err)
		a.renderPedidoDetalle(w, r, conn, id, "No se pudo anotar el reembolso. Inténtalo de nuevo.", "error", http.StatusInternalServerError)
		return
	}
	log.Printf("pedidos: reembolso del pedido %d anotado por %q desde %s", id, quien.Usuario, quien.IP)
	http.Redirect(w, r, "/admin/pedidos/"+strconv.Itoa(id)+"?r=reembolsado", http.StatusSeeOther)
}
