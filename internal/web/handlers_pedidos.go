package web

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"manualidades/internal/tienda"
)

// PedidosList muestra los pedidos hechos desde la tienda pública.
func (a *App) PedidosList(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "pedidos")
	if conn == nil {
		return
	}
	pedidos, err := tienda.ListPedidos(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, "pedidos_list.html", map[string]any{
		"Title":   "Pedidos",
		"Active":  "pedidos",
		"Pedidos": pedidos,
	})
}

// PagosList muestra todos los intentos de pago recientes, incluidos los
// que nunca llegaron a generar un pedido (rechazados o con falla técnica
// de la pasarela) — esos no aparecen en PedidosList porque no tienen
// pedido asociado.
func (a *App) PagosList(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "pedidos")
	if conn == nil {
		return
	}
	pagos, err := tienda.ListPagos(conn, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, "pagos_list.html", map[string]any{
		"Title":  "Intentos de pago",
		"Active": "pedidos",
		"Pagos":  pagos,
	})
}

func (a *App) PedidoDetalle(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "pedidos")
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := tienda.ActualizarEstado(conn, id, r.FormValue("estado")); err != nil {
			http.Error(w, "no se pudo actualizar el estado: "+err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/admin/pedidos/"+strconv.Itoa(id), http.StatusSeeOther)
		return
	}

	pedido, err := tienda.GetPedido(conn, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var pago *tienda.Pago
	if p, err := tienda.GetPagoPorPedido(conn, id); err == nil {
		pago = &p
	}
	render(w, r, "pedido_detalle.html", map[string]any{
		"Title":          "Pedido",
		"Active":         "pedidos",
		"Pedido":         pedido,
		"EstadosValidos": tienda.EstadosValidos,
		"Pago":           pago,
	})
}
