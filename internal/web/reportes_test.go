package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"manualidades/internal/reportes"
)

func TestFormatoNumeros(t *testing.T) {
	for v, quiero := range map[float64]string{0: "0.00", 12.5: "12.50", 1234.567: "1,234.57", -1234.5: "-1,234.50", 1234567: "1,234,567.00"} {
		if got := miles(v); got != quiero {
			t.Errorf("miles(%v) = %q, quiero %q", v, got, quiero)
		}
	}
	if got := cantidadTexto(3); got != "3" {
		t.Errorf("cantidadTexto(3) = %q", got)
	}
	if got := cantidadTexto(2.5); got != "2.50" {
		t.Errorf("cantidadTexto(2.5) = %q", got)
	}
}

func TestGraficosEscapanTextoYNoDibujanVacios(t *testing.T) {
	svg := string(barrasHorizontales([]barraH{{Etiqueta: `<script>alert(1)</script>`, Valor: 5, Texto: "Q 5.00"}}))
	if strings.Contains(svg, "<script>") || !strings.Contains(svg, "&lt;script&gt;") {
		t.Errorf("la etiqueta no se escapó: %s", svg)
	}
	if barrasHorizontales([]barraH{{Etiqueta: "x", Valor: 0}}) != "" {
		t.Error("un gráfico con todo en cero debe omitirse")
	}
	if barrasApiladas([]string{"a"}, []serieGrafico{{"s", colorOro, []float64{0}}}, quetzales) != "" {
		t.Error("barras apiladas en cero deben omitirse")
	}
	if dona([]tajada{{"a", 0, "", colorOro}}) != "" {
		t.Error("una dona sin datos debe omitirse")
	}
	// Valores negativos no deben romper la escala.
	if neg := barrasHorizontales([]barraH{{Etiqueta: "a", Valor: -3, Texto: "-3"}, {Etiqueta: "b", Valor: 5, Texto: "5"}}); neg == "" {
		t.Error("barras con negativos deben dibujarse")
	}
}

func TestPlantillaReporteGenerico(t *testing.T) {
	v := reporteVista{
		Titulo:   "Ventas por período",
		Lead:     "lead",
		Filtros:  filtrosVista{Fecha: true, Categoria: true, Origen: true, Agrup: true},
		KPIs:     []kpiVista{{"Total vendido", "Q 10.00"}},
		Graficos: []graficoVista{{"Ventas", barrasApiladas([]string{"01/01"}, []serieGrafico{{"Tienda", colorTerracota, []float64{10}}}, quetzales)}},
		Tabla: tablaVista{
			Cols:  []string{"Período", "Total"},
			Num:   []bool{false, true},
			Filas: [][]string{{"01/01/2026", "10.00"}},
			Pie:   []string{"Total", "10.00"},
		},
		Nota: "una nota",
	}
	out := ejecutarContent(t, "reporte_generico.html", map[string]any{
		"R": v, "Aviso": "", "Desde": "2026-01-01", "Hasta": "2026-01-31", "DesdeTexto": "01/01/2026", "HastaTexto": "31/01/2026",
		"CategoriaID": 0, "Origen": "", "Agrup": "day", "Categorias": nil, "Atajos": atajosFecha(httptest.NewRequest("GET", "/admin/reportes/ventas", nil)),
		"RutaForm": "/admin/reportes/ventas", "CSVURL": "/admin/reportes/ventas?formato=csv",
	})
	for _, quiero := range []string{"Total vendido", "<svg", "01/01/2026", "Descargar CSV", "una nota", "Este mes", "<tfoot>"} {
		if !strings.Contains(out, quiero) {
			t.Errorf("falta %q en el reporte", quiero)
		}
	}

	// Con un aviso de error no se muestran datos.
	err := ejecutarContent(t, "reporte_generico.html", map[string]any{
		"R": v, "Aviso": "Rango de fechas inválido.", "Desde": "", "Hasta": "", "CategoriaID": 0, "Origen": "", "Agrup": "day",
		"Categorias": nil, "Atajos": nil, "RutaForm": "/x", "CSVURL": "/x",
	})
	if !strings.Contains(err, "Rango de fechas inválido.") || strings.Contains(err, "<svg") {
		t.Error("un aviso debe mostrarse sin datos")
	}
}

func TestPlantillaReportesHome(t *testing.T) {
	out := ejecutarContent(t, "reportes_home.html", map[string]any{})
	if !strings.Contains(out, "/admin/reportes/ventas") || !strings.Contains(out, "/admin/reportes/movimientos") {
		t.Error("faltan tarjetas en la entrada de reportes")
	}
}

func TestEscribirCSVQuitaSeparadorDeMiles(t *testing.T) {
	w := httptest.NewRecorder()
	escribirCSV(w, reporteVista{Archivo: "x", Tabla: tablaVista{
		Cols: []string{"Producto", "Monto"}, Num: []bool{false, true},
		Filas: [][]string{{"Collar, grande", "1,234.50"}}, Pie: []string{"Total", "1,234.50"},
	}})
	body := w.Body.String()
	if !strings.Contains(body, `"Collar, grande",1234.50`) || strings.Contains(body, "1,234") {
		t.Errorf("CSV mal formado: %q", body)
	}
	if !strings.HasPrefix(body, "\xef\xbb\xbf") {
		t.Error("falta el BOM UTF-8")
	}
}

func TestLeerParametros(t *testing.T) {
	casos := map[string]bool{ // url → ¿válido?
		"/x":                                   true,
		"/x?desde=2026-01-01&hasta=2026-01-31": true,
		"/x?desde=2026-02-01&hasta=2026-01-31": false,
		"/x?desde=nope&hasta=2026-01-31":       false,
		"/x?desde=2000-01-01&hasta=2026-01-31": false,
	}
	for u, valido := range casos {
		_, aviso := leerParametros(httptest.NewRequest("GET", u, nil))
		if (aviso == "") != valido {
			t.Errorf("%s: aviso = %q, válido esperado %v", u, aviso, valido)
		}
	}
	p, _ := leerParametros(httptest.NewRequest("GET", "/x?origen=tienda&agrup=month&categoria=3", nil))
	if p.Filtro.Origen != reportes.OrigenTienda || p.Agrup != reportes.AgrupMes || p.Filtro.CategoriaID != 3 {
		t.Errorf("parámetros = %+v", p)
	}
	// Valores desconocidos caen a los de por defecto (nada se inyecta en el SQL).
	p, _ = leerParametros(httptest.NewRequest("GET", "/x?origen=x'--&agrup=year;drop", nil))
	if p.Filtro.Origen != "" || p.Agrup != reportes.AgrupDia {
		t.Errorf("valores no reconocidos = %+v", p)
	}
}

func TestInicioPeriodo(t *testing.T) {
	jueves := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	if got := reportes.InicioPeriodo(jueves, reportes.AgrupSemana); got.Weekday() != time.Monday || got.Day() != 21 {
		t.Errorf("inicio de semana = %v", got)
	}
	if got := reportes.InicioPeriodo(jueves, reportes.AgrupMes); got.Day() != 1 {
		t.Errorf("inicio de mes = %v", got)
	}
}
