package inventario

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"manualidades/internal/testdb"
)

var hoy = time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

func nuevaBase(t *testing.T) (*sql.DB, int) {
	t.Helper()
	conn := testdb.Nueva(t, Migrate)
	if err := CreateCategoria(conn, "General", ""); err != nil {
		t.Fatal(err)
	}
	var cat int
	if err := conn.QueryRow(`SELECT id FROM categorias WHERE nombre = 'General'`).Scan(&cat); err != nil {
		t.Fatal(err)
	}
	prod, err := CreateProducto(conn, Producto{CategoriaID: cat, Nombre: "Taza", PrecioCompra: 10, PrecioVenta: 25, Activo: true})
	if err != nil {
		t.Fatal(err)
	}
	return conn, prod
}

// ultimoID devuelve el id del movimiento más reciente del producto.
func ultimoID(t *testing.T, conn *sql.DB, prod int) int {
	t.Helper()
	var id int
	if err := conn.QueryRow(`SELECT max(id) FROM movimientos_inventario WHERE producto_id = $1`, prod).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func stock(t *testing.T, conn *sql.DB, prod int) float64 {
	t.Helper()
	s, err := StockActual(conn, prod)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func buscar(t *testing.T, conn *sql.DB, id int) Movimiento {
	t.Helper()
	lista, err := ListMovimientos(conn, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range lista {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no se encontró el movimiento %d", id)
	return Movimiento{}
}

// El caso real: se registró 11 en vez de 1.
func TestAnularIngresoEquivocadoYRegistrarElCorrecto(t *testing.T) {
	conn, prod := nuevaBase(t)
	if err := CreateMovimiento(conn, prod, "ingreso", 11, "compra", false, 0, hoy); err != nil {
		t.Fatal(err)
	}
	malo := ultimoID(t, conn, prod)
	if got := stock(t, conn, prod); got != 11 {
		t.Fatalf("stock inicial %v, se esperaba 11", got)
	}

	if err := AnularMovimiento(conn, malo, hoy); err != nil {
		t.Fatal(err)
	}
	if got := stock(t, conn, prod); got != 0 {
		t.Errorf("tras anular, stock %v, se esperaba 0", got)
	}
	if err := CreateMovimiento(conn, prod, "ingreso", 1, "compra", false, 0, hoy); err != nil {
		t.Fatal(err)
	}
	if got := stock(t, conn, prod); got != 1 {
		t.Errorf("tras registrar el correcto, stock %v, se esperaba 1", got)
	}

	// El historial se conserva completo: nada se borró ni se editó.
	var total int
	conn.QueryRow(`SELECT count(*) FROM movimientos_inventario WHERE producto_id = $1`, prod).Scan(&total)
	if total != 3 {
		t.Errorf("el historial tiene %d movimientos, se esperaban 3 (original, anulación y correcto)", total)
	}
	orig := buscar(t, conn, malo)
	if orig.Cantidad != 11 || orig.Tipo != "ingreso" || orig.AnuladoPor == 0 {
		t.Errorf("el original debe seguir intacto y marcado como anulado: %+v", orig)
	}
	anul := buscar(t, conn, orig.AnuladoPor)
	if anul.AnulaA != malo || anul.Tipo != "consumo" || anul.Cantidad != 11 || anul.EsVenta {
		t.Errorf("la anulación no es el contraasiento esperado: %+v", anul)
	}
	if orig.Anulable() || anul.Anulable() {
		t.Error("ni el anulado ni la anulación deberían ser anulables")
	}
}

func TestAnularUnaVentaDevuelveElStock(t *testing.T) {
	conn, prod := nuevaBase(t)
	CreateMovimiento(conn, prod, "ingreso", 10, "inicial", false, 0, hoy)
	if err := CreateMovimiento(conn, prod, "consumo", 3, "venta", true, 25, hoy); err != nil {
		t.Fatal(err)
	}
	venta := ultimoID(t, conn, prod)
	if got := stock(t, conn, prod); got != 7 {
		t.Fatalf("stock %v, se esperaba 7", got)
	}
	if err := AnularMovimiento(conn, venta, hoy); err != nil {
		t.Fatal(err)
	}
	if got := stock(t, conn, prod); got != 10 {
		t.Errorf("tras anular la venta, stock %v, se esperaba 10", got)
	}
	if anul := buscar(t, conn, buscar(t, conn, venta).AnuladoPor); anul.Tipo != "ingreso" || anul.EsVenta {
		t.Errorf("anular un consumo debe crear un ingreso que no es venta: %+v", anul)
	}
}

func TestAnulacionesInvalidas(t *testing.T) {
	conn, prod := nuevaBase(t)
	CreateMovimiento(conn, prod, "ingreso", 5, "x", false, 0, hoy)
	ing := ultimoID(t, conn, prod)

	if err := AnularMovimiento(conn, 999999, hoy); !errors.Is(err, ErrMovimientoNoExiste) {
		t.Errorf("movimiento inexistente: %v", err)
	}
	if err := AnularMovimiento(conn, ing, hoy); err != nil {
		t.Fatal(err)
	}
	if err := AnularMovimiento(conn, ing, hoy); !errors.Is(err, ErrMovimientoAnulado) {
		t.Errorf("anular dos veces: %v", err)
	}
	anulacion := buscar(t, conn, ing).AnuladoPor
	if err := AnularMovimiento(conn, anulacion, hoy); !errors.Is(err, ErrEsAnulacion) {
		t.Errorf("anular una anulación: %v", err)
	}

	// Un movimiento que nace de un pedido de la tienda no se anula aquí.
	tx, _ := conn.Begin()
	if err := CreateMovimientoPedidoTx(tx, 77, prod, "ingreso", 2, "Pedido 77", false, 0, hoy); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if err := AnularMovimiento(conn, ultimoID(t, conn, prod), hoy); !errors.Is(err, ErrMovimientoDePedido) {
		t.Errorf("movimiento de pedido: %v", err)
	}
	if got := stock(t, conn, prod); got != 2 {
		t.Errorf("las anulaciones inválidas cambiaron el stock: %v (esperado 2)", got)
	}
}

// Anular un ingreso que ya se consumió dejaría stock negativo: se rechaza y
// no se escribe nada.
func TestNoSePuedeAnularUnIngresoYaConsumido(t *testing.T) {
	conn, prod := nuevaBase(t)
	CreateMovimiento(conn, prod, "ingreso", 10, "x", false, 0, hoy)
	ing := ultimoID(t, conn, prod)
	CreateMovimiento(conn, prod, "consumo", 8, "venta", true, 25, hoy)

	var antes int
	conn.QueryRow(`SELECT count(*) FROM movimientos_inventario`).Scan(&antes)
	if err := AnularMovimiento(conn, ing, hoy); !errors.Is(err, ErrStockInsuficiente) {
		t.Fatalf("se esperaba ErrStockInsuficiente, llegó %v", err)
	}
	var despues int
	conn.QueryRow(`SELECT count(*) FROM movimientos_inventario`).Scan(&despues)
	if antes != despues || stock(t, conn, prod) != 2 {
		t.Errorf("el rechazo dejó rastro: movimientos %d→%d, stock %v", antes, despues, stock(t, conn, prod))
	}
	// Una vez anulada la venta, ya sí se puede anular el ingreso.
	venta := ultimoID(t, conn, prod)
	if err := AnularMovimiento(conn, venta, hoy); err != nil {
		t.Fatal(err)
	}
	if err := AnularMovimiento(conn, ing, hoy); err != nil {
		t.Errorf("tras anular la venta debería poder anularse el ingreso: %v", err)
	}
	if got := stock(t, conn, prod); got != 0 {
		t.Errorf("stock final %v, se esperaba 0", got)
	}
}

// Dos clics simultáneos sobre «Anular» deben producir UNA sola anulación.
func TestAnularConcurrenteSoloUnaVez(t *testing.T) {
	conn, prod := nuevaBase(t)
	CreateMovimiento(conn, prod, "ingreso", 5, "x", false, 0, hoy)
	ing := ultimoID(t, conn, prod)

	const n = 8
	var wg sync.WaitGroup
	resultados := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resultados[i] = AnularMovimiento(conn, ing, hoy)
		}(i)
	}
	wg.Wait()

	var ok, ya int
	for _, err := range resultados {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrMovimientoAnulado):
			ya++
		default:
			t.Errorf("error inesperado: %v", err)
		}
	}
	if ok != 1 || ya != n-1 {
		t.Errorf("anulaciones exitosas: %d, rechazadas por duplicado: %d (esperado 1 y %d)", ok, ya, n-1)
	}
	if got := stock(t, conn, prod); got != 0 {
		t.Errorf("stock %v, se esperaba 0 (la anulación se aplicó más de una vez)", got)
	}
}
