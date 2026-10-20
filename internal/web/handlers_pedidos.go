package web

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"manualidades/internal/tienda"
)

// PedidosList muestra los pedidos pagados de la tienda pública, y aparte los
// que hoy están reservando stock mientras esperan el pago (esos todavía no
// se despachan).
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
	pendientes, err := tienda.ListPedidosPendientes(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	porRevisar := 0
	for _, p := range pendientes {
		if p.Estado == tienda.EstadoTransferenciaPorRevisar {
			porRevisar++
		}
	}
	render(w, r, "pedidos_list.html", map[string]any{
		"Title":      "Pedidos",
		"Active":     "pedidos",
		"Pedidos":    pedidos,
		"Pendientes": pendientes,
		"PorRevisar": porRevisar,
	})
}

// PagosList muestra todos los intentos de pago recientes, incluidos los
// rechazados, con falla técnica de la pasarela o por conciliar — los que no
// terminaron en un pedido pagado no aparecen en PedidosList.
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
			switch {
			case errors.Is(err, tienda.ErrPedidoCancelado), errors.Is(err, tienda.ErrUsarCancelacion),
				errors.Is(err, tienda.ErrEstadoNoModificable), errors.Is(err, tienda.ErrEstadoInvalido):
				// Mensajes propios, pensados para mostrarse.
				a.renderPedidoDetalle(w, r, conn, id, "No se pudo cambiar el estado: "+err.Error()+".", "error", http.StatusConflict)
			default:
				log.Printf("pedidos: cambiando el estado del pedido %d: %v", id, err)
				a.renderPedidoDetalle(w, r, conn, id, "No se pudo cambiar el estado. Inténtalo de nuevo.", "error", http.StatusInternalServerError)
			}
			return
		}
		http.Redirect(w, r, "/admin/pedidos/"+strconv.Itoa(id), http.StatusSeeOther)
		return
	}

	msg, kind := "", ""
	if m, ok := mensajesRevision[r.URL.Query().Get("r")]; ok {
		msg, kind = m, "success"
	}
	a.renderPedidoDetalle(w, r, conn, id, msg, kind, http.StatusOK)
}
