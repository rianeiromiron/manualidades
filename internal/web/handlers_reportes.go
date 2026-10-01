package web

import (
	"encoding/csv"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"manualidades/internal/inventario"
	"manualidades/internal/reportes"
)

const rutaReportes = "/admin/reportes"

var mesesES = []string{"Enero", "Febrero", "Marzo", "Abril", "Mayo", "Junio", "Julio", "Agosto", "Septiembre", "Octubre", "Noviembre", "Diciembre"}

// ---- Estructuras que consume reporte_generico.html ----

type kpiVista struct{ Etiqueta, Valor string }

type tablaVista struct {
	Cols  []string
	Num   []bool // columnas numéricas: se alinean a la derecha y en CSV van sin separador de miles
	Filas [][]string
	Pie   []string // fila de totales (opcional)
}

type graficoVista struct {
	Titulo string
	SVG    template.HTML
}

type filtrosVista struct {
	Fecha, Categoria, Origen, Agrup bool
}

type atajoVista struct{ Texto, URL string }

// reporteVista es todo lo que necesita la plantilla genérica de un reporte.
type reporteVista struct {
	Titulo, Lead, Nota string
	Filtros            filtrosVista
	KPIs               []kpiVista
	Graficos           []graficoVista
	Tabla              tablaVista
	Archivo            string // nombre base del CSV
}

// ReportesHome es la página de entrada con una tarjeta por reporte.
func (a *App) ReportesHome(w http.ResponseWriter, r *http.Request) {
	render(w, r, "reportes_home.html", map[string]any{
		"Title":  "Reportes",
		"Active": "reportes",
	})
}

// parametrosReporte son los filtros ya leídos y validados de la URL.
type parametrosReporte struct {
	Filtro reportes.Filtro
	Agrup  string
}

// leerParametros lee desde/hasta (por defecto, el mes en curso), categoría,
// origen y agrupación. Si algo no es válido devuelve un mensaje para el usuario.
func leerParametros(r *http.Request) (parametrosReporte, string) {
	q := r.URL.Query()
	hoy := time.Now()
	hoyUTC := time.Date(hoy.Year(), hoy.Month(), hoy.Day(), 0, 0, 0, 0, time.UTC)

	p := parametrosReporte{Filtro: reportes.Filtro{
		Desde: time.Date(hoy.Year(), hoy.Month(), 1, 0, 0, 0, 0, time.UTC),
		Hasta: hoyUTC,
	}}
	var err1, err2 error
	if s := q.Get("desde"); s != "" {
		p.Filtro.Desde, err1 = time.Parse("2006-01-02", s)
	}
	if s := q.Get("hasta"); s != "" {
		p.Filtro.Hasta, err2 = time.Parse("2006-01-02", s)
	}
	switch {
	case err1 != nil || err2 != nil:
		return p, "Rango de fechas inválido."
	case p.Filtro.Hasta.Before(p.Filtro.Desde):
		return p, "La fecha \"hasta\" no puede ser anterior a \"desde\"."
	case p.Filtro.Hasta.Sub(p.Filtro.Desde) > 5*366*24*time.Hour:
		return p, "El rango máximo es de 5 años."
	}

	p.Filtro.CategoriaID, _ = strconv.Atoi(q.Get("categoria"))
	switch q.Get("origen") {
	case reportes.OrigenTienda:
		p.Filtro.Origen = reportes.OrigenTienda
	case reportes.OrigenManual:
		p.Filtro.Origen = reportes.OrigenManual
	}
	switch q.Get("agrup") {
	case reportes.AgrupSemana:
		p.Agrup = reportes.AgrupSemana
	case reportes.AgrupMes:
		p.Agrup = reportes.AgrupMes
	default:
		p.Agrup = reportes.AgrupDia
	}
	return p, ""
}

// atajosFecha arma los enlaces "Este mes", "Mes pasado"... conservando el
// resto de filtros de la URL actual.
func atajosFecha(r *http.Request) []atajoVista {
	hoy := time.Now()
	d := func(t time.Time) string { return t.Format("2006-01-02") }
	primero := time.Date(hoy.Year(), hoy.Month(), 1, 0, 0, 0, 0, hoy.Location())
	rangos := []struct {
		texto        string
		desde, hasta time.Time
	}{
		{"Hoy", hoy, hoy},
		{"Últimos 7 días", hoy.AddDate(0, 0, -6), hoy},
		{"Últimos 30 días", hoy.AddDate(0, 0, -29), hoy},
		{"Este mes", primero, hoy},
		{"Mes pasado", primero.AddDate(0, -1, 0), primero.AddDate(0, 0, -1)},
		{"Este año", time.Date(hoy.Year(), 1, 1, 0, 0, 0, 0, hoy.Location()), hoy},
	}
	out := make([]atajoVista, 0, len(rangos))
	for _, rg := range rangos {
		q := r.URL.Query()
		q.Del("formato")
		q.Set("desde", d(rg.desde))
		q.Set("hasta", d(rg.hasta))
		out = append(out, atajoVista{rg.texto, r.URL.Path + "?" + q.Encode()})
	}
	return out
}

// renderReporte pinta la plantilla genérica o, si se pidió formato=csv,
// descarga la tabla como CSV.
func (a *App) renderReporte(w http.ResponseWriter, r *http.Request, v reporteVista, p parametrosReporte, aviso string) {
	if r.URL.Query().Get("formato") == "csv" && aviso == "" {
		escribirCSV(w, v)
		return
	}

	categorias := []inventario.Categoria(nil)
	if v.Filtros.Categoria {
		if conn := a.DB(); conn != nil {
			categorias, _ = inventario.ListCategorias(conn)
		}
	}
	csvQ := r.URL.Query()
	csvQ.Set("formato", "csv")

	render(w, r, "reporte_generico.html", map[string]any{
		"Title":       v.Titulo,
		"Active":      "reportes",
		"R":           v,
		"Aviso":       aviso,
		"Desde":       p.Filtro.Desde.Format("2006-01-02"),
		"Hasta":       p.Filtro.Hasta.Format("2006-01-02"),
		"DesdeTexto":  p.Filtro.Desde.Format("02/01/2006"),
		"HastaTexto":  p.Filtro.Hasta.Format("02/01/2006"),
		"CategoriaID": p.Filtro.CategoriaID,
		"Origen":      p.Filtro.Origen,
		"Agrup":       p.Agrup,
		"Categorias":  categorias,
		"Atajos":      atajosFecha(r),
		"RutaForm":    r.URL.Path,
		"CSVURL":      r.URL.Path + "?" + csvQ.Encode(),
	})
}

func escribirCSV(w http.ResponseWriter, v reporteVista) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.csv"`, v.Archivo, time.Now().Format("2006-01-02")))
	w.Write([]byte("\xef\xbb\xbf")) // BOM: Excel reconoce así el UTF-8
	cw := csv.NewWriter(w)
	cw.Write(v.Tabla.Cols)
	limpio := func(fila []string) []string {
		out := make([]string, len(fila))
		for i, c := range fila {
			if i < len(v.Tabla.Num) && v.Tabla.Num[i] {
				c = strings.ReplaceAll(c, ",", "")
			}
			out[i] = c
		}
		return out
	}
	for _, f := range v.Tabla.Filas {
		cw.Write(limpio(f))
	}
	if v.Tabla.Pie != nil {
		cw.Write(limpio(v.Tabla.Pie))
	}
	cw.Flush()
}

// ---- Ventas por período ----

func etiquetaPeriodo(t time.Time, agrup string, larga bool) string {
	switch agrup {
	case reportes.AgrupMes:
		if larga {
			return fmt.Sprintf("%s %d", mesesES[t.Month()-1], t.Year())
		}
		return fmt.Sprintf("%s %02d", mesesES[t.Month()-1][:3], t.Year()%100)
	case reportes.AgrupSemana:
		if larga {
			return "Semana del " + t.Format("02/01/2006")
		}
		return t.Format("02/01")
	}
	if larga {
		return t.Format("02/01/2006")
	}
	return t.Format("02/01")
}

func (a *App) ReporteVentas(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "reportes")
	if conn == nil {
		return
	}
	p, aviso := leerParametros(r)
	v := reporteVista{
		Titulo:  "Ventas por período",
		Lead:    "Cuánto se vendió, separando lo que entró por la tienda de lo registrado a mano.",
		Filtros: filtrosVista{Fecha: true, Categoria: true, Origen: true, Agrup: true},
		Archivo: "ventas-por-periodo",
	}
	if aviso != "" {
		a.renderReporte(w, r, v, p, aviso)
		return
	}

	// Demasiadas barras no se leen: se agrupa más grueso y se avisa.
	original := p.Agrup
	for reportes.NumPeriodos(p.Filtro, p.Agrup) > 120 && p.Agrup != reportes.AgrupMes {
		if p.Agrup == reportes.AgrupDia {
			p.Agrup = reportes.AgrupSemana
		} else {
			p.Agrup = reportes.AgrupMes
		}
	}
	if p.Agrup != original {
		v.Nota = "El rango es largo para mostrarlo por " + nombreAgrup(original) + ": se agrupó por " + nombreAgrup(p.Agrup) + "."
	}

	filas, err := reportes.VentasPorPeriodo(conn, p.Filtro, p.Agrup)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var tienda, manual, unidades float64
	var pedidos int
	etiquetas := make([]string, len(filas))
	valTienda := make([]float64, len(filas))
	valManual := make([]float64, len(filas))
	for i, f := range filas {
		tienda += f.Tienda
		manual += f.Manual
		unidades += f.Unidades
		pedidos += f.Pedidos
		etiquetas[i] = etiquetaPeriodo(f.Periodo, p.Agrup, false)
		valTienda[i], valManual[i] = f.Tienda, f.Manual
		v.Tabla.Filas = append(v.Tabla.Filas, []string{
			etiquetaPeriodo(f.Periodo, p.Agrup, true), miles(f.Tienda), miles(f.Manual), miles(f.Total()), cantidadTexto(f.Unidades), strconv.Itoa(f.Pedidos),
		})
	}
	ticket := 0.0
	if pedidos > 0 {
		ticket = tienda / float64(pedidos)
	}

	v.KPIs = []kpiVista{
		{"Total vendido", quetzales(tienda + manual)},
		{"Ventas de tienda", quetzales(tienda)},
		{"Ventas manuales", quetzales(manual)},
		{"Pedidos de tienda", strconv.Itoa(pedidos)},
		{"Ticket promedio (tienda)", quetzales(ticket)},
	}
	v.Tabla.Cols = []string{"Período", "Tienda (Q)", "Manual (Q)", "Total (Q)", "Unidades", "Pedidos tienda"}
	v.Tabla.Num = []bool{false, true, true, true, true, true}
	v.Tabla.Pie = []string{"Total", miles(tienda), miles(manual), miles(tienda + manual), cantidadTexto(unidades), strconv.Itoa(pedidos)}

	var series []serieGrafico
	if p.Filtro.Origen != reportes.OrigenManual {
		series = append(series, serieGrafico{"Tienda", colorTerracota, valTienda})
	}
	if p.Filtro.Origen != reportes.OrigenTienda {
		series = append(series, serieGrafico{"Manual", colorOro, valManual})
	}
	v.Graficos = append(v.Graficos, graficoVista{"Ventas por " + nombreAgrup(p.Agrup) + " (Q)", barrasApiladas(etiquetas, series, quetzales)})
	if p.Filtro.Origen == reportes.OrigenTodos {
		v.Graficos = append(v.Graficos, graficoVista{"Origen de las ventas", dona([]tajada{
			{"Tienda", tienda, quetzales(tienda), colorTerracota},
			{"Manual", manual, quetzales(manual), colorOro},
		})})
	}
	a.renderReporte(w, r, v, p, "")
}

func nombreAgrup(agrup string) string {
	switch agrup {
	case reportes.AgrupSemana:
		return "semana"
	case reportes.AgrupMes:
		return "mes"
	}
	return "día"
}

// ---- Productos más vendidos ----

func (a *App) ReporteProductos(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "reportes")
	if conn == nil {
		return
	}
	p, aviso := leerParametros(r)
	v := reporteVista{
		Titulo:  "Productos más vendidos",
		Lead:    "Ranking de productos por lo vendido en el rango. Al final van los que no se vendieron.",
		Filtros: filtrosVista{Fecha: true, Categoria: true, Origen: true},
		Archivo: "productos-mas-vendidos",
	}
	if aviso != "" {
		a.renderReporte(w, r, v, p, aviso)
		return
	}
	filas, err := reportes.VentasPorProducto(conn, p.Filtro)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var ingresos, unidades float64
	conVentas := 0
	for _, f := range filas {
		ingresos += f.Ingresos
		unidades += f.Unidades
		if f.Unidades > 0 {
			conVentas++
		}
	}
	var barras []barraH
	for i, f := range filas {
		pct := 0.0
		if ingresos > 0 {
			pct = f.Ingresos / ingresos * 100
		}
		v.Tabla.Filas = append(v.Tabla.Filas, []string{
			strconv.Itoa(i + 1), f.Nombre, f.Categoria, cantidadTexto(f.Unidades), miles(f.Ingresos), fmt.Sprintf("%.1f", pct),
		})
		if f.Ingresos > 0 && len(barras) < 10 {
			barras = append(barras, barraH{Etiqueta: f.Nombre, Valor: f.Ingresos, Texto: quetzales(f.Ingresos)})
		}
	}
	v.KPIs = []kpiVista{
		{"Total vendido", quetzales(ingresos)},
		{"Unidades vendidas", cantidadTexto(unidades)},
		{"Productos con ventas", strconv.Itoa(conVentas)},
		{"Productos sin ventas", strconv.Itoa(len(filas) - conVentas)},
	}
	v.Tabla.Cols = []string{"#", "Producto", "Categoría", "Unidades", "Ingresos (Q)", "% del total"}
	v.Tabla.Num = []bool{true, false, false, true, true, true}
	v.Graficos = []graficoVista{{"Top 10 por ingresos (Q)", barrasHorizontales(barras)}}
	a.renderReporte(w, r, v, p, "")
}

// ---- Utilidad por producto ----

func (a *App) ReporteUtilidad(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "reportes")
	if conn == nil {
		return
	}
	p, aviso := leerParametros(r)
	v := reporteVista{
		Titulo:  "Utilidad por producto",
		Lead:    "Lo vendido menos lo que costó, según el precio de compra de cada producto.",
		Filtros: filtrosVista{Fecha: true, Categoria: true, Origen: true},
		Archivo: "utilidad-por-producto",
	}
	if aviso != "" {
		a.renderReporte(w, r, v, p, aviso)
		return
	}
	todas, err := reportes.VentasPorProducto(conn, p.Filtro)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Solo productos con ventas, ordenados por utilidad (no por ingreso).
	var filas []reportes.ProductoVenta
	for _, f := range todas {
		if f.Unidades > 0 {
			filas = append(filas, f)
		}
	}
	ordenarPorUtilidad(filas)

	var ingresos, costo float64
	sinCosto := 0
	var barras []barraH
	for i, f := range filas {
		ingresos += f.Ingresos
		costo += f.Costo
		if f.Costo == 0 {
			sinCosto++
		}
		v.Tabla.Filas = append(v.Tabla.Filas, []string{
			f.Nombre, f.Categoria, cantidadTexto(f.Unidades), miles(f.Ingresos), miles(f.Costo), miles(f.Utilidad()), fmt.Sprintf("%.1f", f.Margen()),
		})
		if i < 12 {
			color := colorSalvia
			if f.Utilidad() < 0 {
				color = colorTerracota
			}
			barras = append(barras, barraH{Etiqueta: f.Nombre, Valor: f.Utilidad(), Texto: quetzales(f.Utilidad()), Color: color})
		}
	}
	utilidad := ingresos - costo
	margen := 0.0
	if ingresos > 0 {
		margen = utilidad / ingresos * 100
	}
	v.KPIs = []kpiVista{
		{"Vendido", quetzales(ingresos)},
		{"Costo", quetzales(costo)},
		{"Utilidad", quetzales(utilidad)},
		{"Margen", fmt.Sprintf("%.1f %%", margen)},
	}
	v.Tabla.Cols = []string{"Producto", "Categoría", "Unidades", "Vendido (Q)", "Costo (Q)", "Utilidad (Q)", "Margen %"}
	v.Tabla.Num = []bool{false, false, true, true, true, true, true}
	v.Tabla.Pie = []string{"Total", "", "", miles(ingresos), miles(costo), miles(utilidad), fmt.Sprintf("%.1f", margen)}
	v.Graficos = []graficoVista{{"Utilidad por producto, los 12 mejores (Q)", barrasHorizontales(barras)}}
	v.Nota = "El costo usa el precio de compra actual de cada producto: si lo cambias, el costo de ventas pasadas se recalcula."
	if sinCosto > 0 {
		v.Nota += fmt.Sprintf(" %d producto(s) tienen precio de compra 0 y muestran 100%% de margen.", sinCosto)
	}
	a.renderReporte(w, r, v, p, "")
}

func ordenarPorUtilidad(f []reportes.ProductoVenta) {
	// Inserción: las listas son cortas (catálogo de una tienda).
	for i := 1; i < len(f); i++ {
		for j := i; j > 0 && f[j].Utilidad() > f[j-1].Utilidad(); j-- {
			f[j], f[j-1] = f[j-1], f[j]
		}
	}
}

// ---- Stock valorizado ----

func (a *App) ReporteStock(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "reportes")
	if conn == nil {
		return
	}
	// Este reporte no usa fechas, así que un rango inválido en la URL no importa.
	var p parametrosReporte
	p.Filtro.CategoriaID, _ = strconv.Atoi(r.URL.Query().Get("categoria"))
	p.Filtro.Desde, p.Filtro.Hasta = time.Now(), time.Now()
	v := reporteVista{
		Titulo:  "Stock valorizado",
		Lead:    "Existencia actual de cada producto activo, a precio de compra y a precio de venta.",
		Filtros: filtrosVista{Categoria: true},
		Archivo: "stock-valorizado",
	}
	filas, err := reportes.StockValorizado(conn, p.Filtro.CategoriaID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var unidades, costo, venta float64
	porCategoria := map[string]float64{}
	var ordenCat []string
	for _, f := range filas {
		unidades += f.Stock
		costo += f.ValorCosto()
		venta += f.ValorVenta()
		if _, ok := porCategoria[f.Categoria]; !ok {
			ordenCat = append(ordenCat, f.Categoria)
		}
		porCategoria[f.Categoria] += f.ValorCosto()
		v.Tabla.Filas = append(v.Tabla.Filas, []string{
			f.Nombre, f.Categoria, cantidadTexto(f.Stock), miles(f.PrecioCompra), miles(f.ValorCosto()), miles(f.PrecioVenta), miles(f.ValorVenta()),
		})
	}
	var barras []barraH
	for _, c := range ordenCat {
		barras = append(barras, barraH{Etiqueta: c, Valor: porCategoria[c], Texto: quetzales(porCategoria[c])})
	}
	v.KPIs = []kpiVista{
		{"Unidades en bodega", cantidadTexto(unidades)},
		{"Valor al costo", quetzales(costo)},
		{"Valor a precio de venta", quetzales(venta)},
		{"Utilidad potencial", quetzales(venta - costo)},
	}
	v.Tabla.Cols = []string{"Producto", "Categoría", "Stock", "P. compra (Q)", "Valor costo (Q)", "P. venta (Q)", "Valor venta (Q)"}
	v.Tabla.Num = []bool{false, false, true, true, true, true, true}
	v.Tabla.Pie = []string{"Total", "", cantidadTexto(unidades), "", miles(costo), "", miles(venta)}
	v.Graficos = []graficoVista{{"Valor al costo por categoría (Q)", barrasHorizontales(barras)}}
	a.renderReporte(w, r, v, p, "")
}

// ---- Pedidos por estado ----

var estadosPedido = []struct{ Clave, Nombre, Color string }{
	{"pendiente_pago", "Pendiente de pago", colorOro},
	{"pagando", "Cobrando", colorOro},
	{"expirado", "Expirado", colorTinta},
	{"pagado", "Pagado", colorSalvia},
	{"procesando", "Procesando en bodega", colorSalvia},
	{"entregado", "Entregado", colorSalvia},
	{"cancelado", "Cancelado", colorTerracota},
}

func (a *App) ReportePedidos(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "reportes")
	if conn == nil {
		return
	}
	p, aviso := leerParametros(r)
	v := reporteVista{
		Titulo:  "Pedidos por estado",
		Lead:    "Qué pasó con los pedidos creados en el rango, incluidos los que nunca llegaron a pagarse.",
		Filtros: filtrosVista{Fecha: true},
		Archivo: "pedidos-por-estado",
	}
	if aviso != "" {
		a.renderReporte(w, r, v, p, aviso)
		return
	}
	estados, err := reportes.PedidosPorEstado(conn, p.Filtro.Desde, p.Filtro.Hasta)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	entregas, err := reportes.PedidosPorEntrega(conn, p.Filtro.Desde, p.Filtro.Hasta)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	por := map[string]reportes.PedidosEstado{}
	creados := 0
	for _, e := range estados {
		por[e.Estado] = e
		creados += e.Pedidos
	}
	pagados, montoPagado := 0, 0.0
	var barras []barraH
	for _, e := range estadosPedido {
		fila := por[e.Clave]
		pct := 0.0
		if creados > 0 {
			pct = float64(fila.Pedidos) / float64(creados) * 100
		}
		if e.Clave == "pagado" || e.Clave == "procesando" || e.Clave == "entregado" {
			pagados += fila.Pedidos
			montoPagado += fila.Total
		}
		v.Tabla.Filas = append(v.Tabla.Filas, []string{e.Nombre, strconv.Itoa(fila.Pedidos), miles(fila.Total), fmt.Sprintf("%.1f", pct)})
		barras = append(barras, barraH{Etiqueta: e.Nombre, Valor: float64(fila.Pedidos), Texto: strconv.Itoa(fila.Pedidos), Color: e.Color})
	}
	tasa := 0.0
	if creados > 0 {
		tasa = float64(pagados) / float64(creados) * 100
	}
	v.KPIs = []kpiVista{
		{"Pedidos creados", strconv.Itoa(creados)},
		{"Pedidos pagados", strconv.Itoa(pagados)},
		{"Tasa de pago", fmt.Sprintf("%.1f %%", tasa)},
		{"Monto pagado", quetzales(montoPagado)},
	}
	v.Tabla.Cols = []string{"Estado", "Pedidos", "Monto (Q)", "% de los creados"}
	v.Tabla.Num = []bool{false, true, true, true}
	v.Tabla.Pie = []string{"Total", strconv.Itoa(creados), "", "100.0"}
	v.Graficos = []graficoVista{{"Pedidos por estado", barrasHorizontales(barras)}}

	var tajadas []tajada
	for i, e := range entregas {
		nombre := "Recoger en tienda"
		if e.Metodo == "domicilio" {
			nombre = "Entrega a domicilio"
		}
		color := colorTerracota
		if i%2 == 1 {
			color = colorOro
		}
		tajadas = append(tajadas, tajada{nombre, float64(e.Pedidos), fmt.Sprintf("%d pedidos", e.Pedidos), color})
	}
	v.Graficos = append(v.Graficos, graficoVista{"Cómo se entregan los pedidos pagados", dona(tajadas)})
	v.Nota = "Un pedido pendiente de pago o cobrando reserva stock unos minutos; si no se paga pasa a expirado."
	a.renderReporte(w, r, v, p, "")
}

// ReporteMovimientosRedirect mantiene vivo el enlace antiguo (cuando el
// reporte de movimientos vivía dentro de Inventario).
func (a *App) ReporteMovimientosRedirect(w http.ResponseWriter, r *http.Request) {
	dest := rutaReportes + "/movimientos"
	if r.URL.RawQuery != "" {
		dest += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
