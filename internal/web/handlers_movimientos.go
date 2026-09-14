package web

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"manualidades/internal/inventario"
)

func (a *App) MovimientosList(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "inventario")
	if conn == nil {
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		productoID, err1 := strconv.Atoi(r.FormValue("producto_id"))
		cantidad, err2 := strconv.ParseFloat(r.FormValue("cantidad"), 64)
		fecha, err3 := time.Parse("2006-01-02", r.FormValue("fecha"))
		tipo := r.FormValue("tipo")

		var msg, kind string
		switch {
		case err1 != nil || err2 != nil || err3 != nil || (tipo != "ingreso" && tipo != "consumo"):
			msg, kind = "Datos inválidos en el formulario.", "error"
		default:
			// "fecha" es la fecha de negocio elegida por el usuario;
			// "creado_en" (cuándo se registró de verdad) lo pone la base
			// de datos automáticamente al insertar. "es_venta"/"precio_venta"
			// solo aplican a consumos; CreateMovimiento los ignora si tipo
			// es ingreso o si es_venta viene desmarcado.
			esVenta := r.FormValue("es_venta") == "on"
			precioVenta, _ := strconv.ParseFloat(r.FormValue("precio_venta"), 64)
			err := inventario.CreateMovimiento(conn, productoID, tipo, cantidad, r.FormValue("motivo"), esVenta, precioVenta, fecha)
			switch {
			case errors.Is(err, inventario.ErrStockInsuficiente):
				msg, kind = "No hay stock suficiente para ese consumo.", "error"
			case err != nil:
				msg, kind = "No se pudo registrar: "+err.Error(), "error"
			default:
				msg, kind = "Movimiento registrado.", "success"
			}
		}
		a.renderMovimientos(w, r, conn, msg, kind)
		return
	}

	a.renderMovimientos(w, r, conn, "", "")
}

func (a *App) renderMovimientos(w http.ResponseWriter, r *http.Request, conn *sql.DB, message, kind string) {
	movimientos, err := inventario.ListMovimientos(conn, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	productos, err := inventario.ListProductos(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	render(w, r, "movimientos_list.html", map[string]any{
		"Title":       "Ingresos y consumos",
		"Active":      "inventario",
		"Movimientos": movimientos,
		"Productos":   productos,
		"Message":     message,
		"MessageKind": kind,
		"Hoy":         time.Now().Format("2006-01-02"),
	})
}

type movimientoConSaldo struct {
	Fecha       time.Time
	CreadoEn    time.Time
	Tipo        string
	EsVenta     bool
	PrecioVenta float64
	Cantidad    float64
	Saldo       float64
	Motivo      string
}

// ProductoMovimientosFragment devuelve el historial de un producto como
// HTML suelto (sin layout), para inyectarlo por fetch() al abrir la fila
// expandible de "Movimientos" en la lista de productos.
func (a *App) ProductoMovimientosFragment(w http.ResponseWriter, r *http.Request) {
	conn := a.DB()
	if conn == nil {
		http.Error(w, "Todavía no hay conexión a la base de datos.", http.StatusServiceUnavailable)
		return
	}

	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}

	producto, err := inventario.GetProducto(conn, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	movs, err := inventario.ListMovimientosProducto(conn, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var saldo float64
	vista := make([]movimientoConSaldo, 0, len(movs))
	for _, m := range movs {
		if m.Tipo == "ingreso" {
			saldo += m.Cantidad
		} else {
			saldo -= m.Cantidad
		}
		vista = append(vista, movimientoConSaldo{
			Fecha: m.Fecha, CreadoEn: m.CreadoEn, Tipo: m.Tipo, EsVenta: m.EsVenta, PrecioVenta: m.PrecioVenta,
			Cantidad: m.Cantidad, Saldo: saldo, Motivo: m.Motivo,
		})
	}

	renderFragment(w, "movimientos_fragment.html", map[string]any{
		"Producto":    producto,
		"Movimientos": vista,
	})
}

// movimientoReporteVista añade el total (cantidad × precio de venta) a un
// movimiento, calculado en Go porque html/template no tiene aritmética.
type movimientoReporteVista struct {
	inventario.Movimiento
	Total float64
}

// Reporte muestra un listado de movimientos filtrado por rango de fechas,
// tipo (ingresos/egresos) y, opcionalmente, solo ventas. Usa GET con
// query params para que el resultado sea enlazable/recargable.
func (a *App) Reporte(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "inventario")
	if conn == nil {
		return
	}

	q := r.URL.Query()
	hoy := time.Now().Format("2006-01-02")

	desdeStr := q.Get("desde")
	if desdeStr == "" {
		desdeStr = hoy
	}
	hastaStr := q.Get("hasta")
	if hastaStr == "" {
		hastaStr = hoy
	}

	// El campo oculto "f" marca que el formulario ya se envió al menos
	// una vez; sin él, los checkboxes sin marcar en la primera visita se
	// interpretarían como "el usuario los desmarcó" en vez de "todavía no
	// ha elegido nada".
	incluirIngresos, incluirEgresos, soloVentas := true, true, false
	if q.Has("f") {
		incluirIngresos = q.Get("ingresos") == "on"
		incluirEgresos = q.Get("egresos") == "on"
		soloVentas = q.Get("solo_ventas") == "on"
	}

	desde, err1 := time.Parse("2006-01-02", desdeStr)
	hasta, err2 := time.Parse("2006-01-02", hastaStr)

	data := map[string]any{
		"Title":           "Reporte",
		"Active":          "inventario",
		"Desde":           desdeStr,
		"Hasta":           hastaStr,
		"IncluirIngresos": incluirIngresos,
		"IncluirEgresos":  incluirEgresos,
		"SoloVentas":      soloVentas,
	}

	if err1 != nil || err2 != nil {
		data["Message"] = "Rango de fechas inválido."
		render(w, r, "reporte.html", data)
		return
	}

	movimientos, err := inventario.ListMovimientosReporte(conn, desde, hasta, incluirIngresos, incluirEgresos, soloVentas)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var totalIngresos, totalEgresos, totalVentas, totalVendido float64
	vista := make([]movimientoReporteVista, 0, len(movimientos))
	for _, m := range movimientos {
		total := 0.0
		switch {
		case m.Tipo == "ingreso":
			totalIngresos += m.Cantidad
		case m.EsVenta:
			total = m.Cantidad * m.PrecioVenta
			totalVentas += m.Cantidad
			totalEgresos += m.Cantidad
			totalVendido += total
		default:
			totalEgresos += m.Cantidad
		}
		vista = append(vista, movimientoReporteVista{Movimiento: m, Total: total})
	}

	data["Movimientos"] = vista
	data["TotalIngresos"] = totalIngresos
	data["TotalEgresos"] = totalEgresos
	data["TotalVentas"] = totalVentas
	data["TotalVendido"] = totalVendido

	render(w, r, "reporte.html", data)
}
