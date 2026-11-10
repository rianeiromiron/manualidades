package e2e

import (
	"testing"

	"github.com/chromedp/chromedp"

	"manualidades/internal/inventario"
)

// Al guardar un producto nuevo se pregunta por la existencia inicial; si se
// acepta, queda un ingreso "INGRESO INICIAL" y el stock correcto.
func TestNavegadorExistenciaInicialDeProductoNuevo(t *testing.T) {
	e := nuevoEntorno(t, true)
	n := nuevoNavegador(t)
	if err := inventario.CreateCategoria(e.Conn, "General", ""); err != nil {
		t.Fatal(err)
	}
	n.iniciaSesionAdmin(t, e.URL)

	crear := func(nombre, cantidad string, boton string) {
		n.correr(t,
			chromedp.Navigate(e.URL+"/admin/mantenimiento/inventario/productos/nuevo"),
			chromedp.WaitVisible(`input[name="nombre"]`),
			chromedp.SendKeys(`input[name="nombre"]`, nombre),
			chromedp.SetValue(`select[name="categoria_id"]`, "1"),
			chromedp.SetValue(`input[name="precio_compra"]`, "5"),
			chromedp.SetValue(`input[name="precio_venta"]`, "9"),
			chromedp.Click(`form.form-grid button[type="submit"]`),
			chromedp.WaitVisible(`#dlgCantidad`),
		)
		if cantidad != "" {
			n.correr(t, chromedp.SendKeys(`#dlgCantidad`, cantidad))
		}
		n.correr(t, chromedp.Click(boton), chromedp.WaitVisible(`table`))
	}

	crear("Con existencia", "7", `#dlgSi`)
	crear("Sin existencia", "", `#dlgNo`)
	n.errores.exigeSinErrores(t, "alta de productos con existencia inicial")

	var stock float64
	var motivo string
	if err := e.Conn.QueryRow(`SELECT p.id FROM productos p WHERE nombre='Con existencia'`).Scan(new(int)); err != nil {
		t.Fatal(err)
	}
	if err := e.Conn.QueryRow(`SELECT COALESCE(SUM(m.cantidad),0), COALESCE(MAX(m.motivo),'') FROM movimientos_inventario m
		JOIN productos p ON p.id=m.producto_id WHERE p.nombre='Con existencia' AND m.tipo='ingreso'`).Scan(&stock, &motivo); err != nil {
		t.Fatal(err)
	}
	if stock != 7 || motivo != "INGRESO INICIAL" {
		t.Errorf("ingreso inicial = %v %q, quería 7 INGRESO INICIAL", stock, motivo)
	}
	var movs int
	e.Conn.QueryRow(`SELECT count(*) FROM movimientos_inventario m JOIN productos p ON p.id=m.producto_id WHERE p.nombre='Sin existencia'`).Scan(&movs)
	if movs != 0 {
		t.Errorf("el producto sin existencia tiene %d movimientos", movs)
	}
}
