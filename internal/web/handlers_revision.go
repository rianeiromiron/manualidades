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

	"github.com/gorilla/mux"

	"manualidades/internal/correo"
	"manualidades/internal/inventario"
	"manualidades/internal/tienda"
)

// Mensajes que muestra el detalle de un pedido tras una revisión. Se pasan por
// un código en la dirección (?r=...) y nunca como texto, para que nadie pueda
// hacer que la página muestre un mensaje cualquiera.
var mensajesRevision = map[string]string{
	"aprobado":  "Transferencia aprobada: el pedido quedó pagado y el inventario se descontó. Se avisó al cliente por correo.",
	"rechazado": "Comprobante rechazado. El pedido espera un nuevo comprobante con un plazo nuevo de 48 horas, y se avisó al cliente por correo.",
	"cancelado": "Comprobante rechazado y pedido cancelado: el inventario volvió a estar disponible y se avisó al cliente por correo.",
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
	w.WriteHeader(status)
	render(w, r, "pedido_detalle.html", map[string]any{
		"Title":          "Pedido",
		"Active":         "pedidos",
		"Pedido":         pedido,
		"EstadosValidos": tienda.EstadosValidos,
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
