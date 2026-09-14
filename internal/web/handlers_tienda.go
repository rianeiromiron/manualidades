package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"

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

// TiendaCheckoutConfirmar recibe el carrito completo (fetch/JSON) al
// presionar "Pagar". Cotiza contra el catálogo actual, le pide el cobro a
// la pasarela simulada (proyecto hermano "pasarela-simulada"), y solo si
// aprueba crea el pedido. Un rechazo o una falla del proveedor no crean
// pedido, pero sí quedan registrados en `pagos` para que el admin los vea.
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
	case errors.Is(err, inventario.ErrStockInsuficiente):
		respondJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Ya no hay stock suficiente: " + err.Error()})
		return
	case err != nil:
		respondJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No se pudo cotizar el pedido."})
		return
	}

	cobro, err := pasarela.Cobrar(pasarela.SolicitudCobro{
		Monto:         total,
		NumeroTarjeta: body.TarjetaNumero,
		NombreTitular: body.TarjetaTitular,
		Expiracion:    body.TarjetaExpiracion,
		CVV:           body.TarjetaCVV,
	})
	if err != nil {
		motivo := "No se pudo conectar con la pasarela de pago. Intenta de nuevo."
		if _, regErr := tienda.RegistrarPago(conn, nil, tienda.MetodoTarjeta, total, tienda.EstadoPagoFallo, "", "", "", motivo); regErr != nil {
			http.Error(w, regErr.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": motivo})
		return
	}

	if !cobro.Aprobado {
		if _, regErr := tienda.RegistrarPago(conn, nil, tienda.MetodoTarjeta, total, tienda.EstadoPagoRechazado, cobro.Referencia, "", "", cobro.Motivo); regErr != nil {
			http.Error(w, regErr.Error(), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusPaymentRequired, map[string]any{"ok": false, "error": "Pago rechazado: " + cobro.Motivo})
		return
	}

	pedidoID, err := tienda.CrearPedido(conn, tienda.DatosPedido{
		ClienteNombre:    body.ClienteNombre,
		ClienteTelefono:  body.ClienteTelefono,
		ClienteEmail:     body.ClienteEmail,
		ClienteNIT:       body.ClienteNIT,
		MetodoEntrega:    body.MetodoEntrega,
		DireccionEntrega: body.DireccionEntrega,
		Notas:            body.Notas,
	}, lineas, total, tienda.ResultadoPago{Referencia: cobro.Referencia, Marca: cobro.Marca, Ultimos4: cobro.Ultimos4})
	switch {
	case errors.Is(err, inventario.ErrStockInsuficiente):
		respondJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Ya no hay stock suficiente: " + err.Error()})
	case err != nil:
		respondJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "El pago se aprobó, pero no se pudo registrar el pedido. Contacta al negocio con la referencia " + cobro.Referencia + "."})
	default:
		respondJSON(w, http.StatusOK, map[string]any{"ok": true, "pedido_id": pedidoID})
	}
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
