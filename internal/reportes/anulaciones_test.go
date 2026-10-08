package reportes_test

import (
	"database/sql"
	"testing"
	"time"

	"manualidades/internal/inventario"
	"manualidades/internal/reportes"
	"manualidades/internal/testdb"
	"manualidades/internal/tienda"
)

var dia = time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

func ultimoMovimiento(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var id int
	if err := conn.QueryRow(`SELECT max(id) FROM movimientos_inventario`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Una venta anulada no debe contar en ningún reporte de ventas; el stock sí
// vuelve a su valor y la venta sigue visible (anulada) en el historial.
func TestVentasAnuladasNoCuentanEnLosReportes(t *testing.T) {
	conn := testdb.Nueva(t, inventario.Migrate, tienda.Migrate)
	if err := inventario.CreateCategoria(conn, "General", ""); err != nil {
		t.Fatal(err)
	}
	var cat int
	conn.QueryRow(`SELECT id FROM categorias WHERE nombre = 'General'`).Scan(&cat)
	prod, err := inventario.CreateProducto(conn, inventario.Producto{CategoriaID: cat, Nombre: "Taza", PrecioCompra: 10, PrecioVenta: 25, Activo: true})
	if err != nil {
		t.Fatal(err)
	}
	inventario.CreateMovimiento(conn, prod, "ingreso", 10, "inicial", false, 0, dia)
	// Dos ventas: una se mantiene (1 × 25) y otra se anulará (2 × 25).
	if err := inventario.CreateMovimiento(conn, prod, "consumo", 1, "venta buena", true, 25, dia); err != nil {
		t.Fatal(err)
	}
	if err := inventario.CreateMovimiento(conn, prod, "consumo", 2, "venta equivocada", true, 25, dia); err != nil {
		t.Fatal(err)
	}
	equivocada := ultimoMovimiento(t, conn)

	f := reportes.Filtro{Desde: dia, Hasta: dia}
	totalPorProducto := func() (unidades, ingresos float64) {
		filas, err := reportes.VentasPorProducto(conn, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, fila := range filas {
			unidades += fila.Unidades
			ingresos += fila.Ingresos
		}
		return
	}
	totalPorPeriodo := func() (unidades, total float64) {
		filas, err := reportes.VentasPorPeriodo(conn, f, reportes.AgrupDia)
		if err != nil {
			t.Fatal(err)
		}
		for _, fila := range filas {
			unidades += fila.Unidades
			total += fila.Total()
		}
		return
	}

	if u, i := totalPorProducto(); u != 3 || i != 75 {
		t.Fatalf("antes de anular: por producto %v unidades / Q%v, se esperaba 3 / Q75", u, i)
	}
	if u, tot := totalPorPeriodo(); u != 3 || tot != 75 {
		t.Fatalf("antes de anular: por período %v unidades / Q%v, se esperaba 3 / Q75", u, tot)
	}

	if err := inventario.AnularMovimiento(conn, equivocada, dia); err != nil {
		t.Fatal(err)
	}

	if u, i := totalPorProducto(); u != 1 || i != 25 {
		t.Errorf("tras anular: por producto %v unidades / Q%v, se esperaba 1 / Q25 (la venta anulada sigue contando)", u, i)
	}
	if u, tot := totalPorPeriodo(); u != 1 || tot != 25 {
		t.Errorf("tras anular: por período %v unidades / Q%v, se esperaba 1 / Q25", u, tot)
	}
	if s, _ := inventario.StockActual(conn, prod); s != 9 {
		t.Errorf("stock %v, se esperaba 9 (10 − 1 venta buena)", s)
	}

	// El reporte «solo ventas» del kardex tampoco la incluye…
	soloVentas, err := inventario.ListMovimientosReporte(conn, dia, dia, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range soloVentas {
		if m.ID == equivocada {
			t.Error("el reporte «solo ventas» incluye una venta anulada")
		}
	}
	if len(soloVentas) != 1 {
		t.Errorf("«solo ventas» devolvió %d filas, se esperaba 1", len(soloVentas))
	}
	// …pero el listado completo conserva todo el historial, marcado.
	todos, _ := inventario.ListMovimientosReporte(conn, dia, dia, true, true, false)
	var marcada, anulacion bool
	for _, m := range todos {
		if m.ID == equivocada && m.AnuladoPor != 0 {
			marcada = true
		}
		if m.AnulaA == equivocada {
			anulacion = true
		}
	}
	if !marcada || !anulacion {
		t.Errorf("el historial completo debe mostrar la venta (marcada anulada=%v) y su anulación (presente=%v)", marcada, anulacion)
	}
}
