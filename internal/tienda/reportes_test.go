package tienda

import (
	"testing"
	"time"

	"manualidades/internal/inventario"
	"manualidades/internal/reportes"
)

// Las consultas de reportes leen tablas de inventario y de tienda a la vez,
// así que se prueban aquí, donde ya existe la base temporal con ambos esquemas.
func TestReportesVentasTiendaYManual(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Collar", 20)
	hoy := time.Now()
	dia := time.Date(hoy.Year(), hoy.Month(), hoy.Day(), 0, 0, 0, 0, time.UTC)

	// Venta de tienda: 3 × Q10 (el helper cotiza a Q10).
	in, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 3)}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfirmarPago(conn, in, ResultadoPago{Referencia: "R1"}); err != nil {
		t.Fatal(err)
	}
	// El movimiento debe quedar enlazado a su pedido.
	var pedidoID int
	if err := conn.QueryRow(`SELECT pedido_id FROM movimientos_inventario WHERE es_venta`).Scan(&pedidoID); err != nil || pedidoID != in.PedidoID {
		t.Fatalf("pedido_id del movimiento = %d (err %v), quiero %d", pedidoID, err, in.PedidoID)
	}

	// Venta manual: 2 × Q15.
	if err := inventario.CreateMovimiento(conn, p, "consumo", 2, "mostrador", true, 15, hoy); err != nil {
		t.Fatal(err)
	}
	// Consumo manual que NO es venta: no debe contar.
	if err := inventario.CreateMovimiento(conn, p, "consumo", 1, "merma", false, 0, hoy); err != nil {
		t.Fatal(err)
	}

	f := reportes.Filtro{Desde: dia, Hasta: dia}
	filas, err := reportes.VentasPorPeriodo(conn, f, reportes.AgrupDia)
	if err != nil {
		t.Fatal(err)
	}
	if len(filas) != 1 {
		t.Fatalf("filas = %d, quiero 1", len(filas))
	}
	if v := filas[0]; v.Tienda != 30 || v.Manual != 30 || v.Unidades != 5 || v.Pedidos != 1 {
		t.Fatalf("ventas = %+v, quiero tienda 30, manual 30, 5 unidades, 1 pedido", v)
	}

	// Filtro por origen.
	f.Origen = reportes.OrigenManual
	filas, _ = reportes.VentasPorPeriodo(conn, f, reportes.AgrupDia)
	if filas[0].Tienda != 0 || filas[0].Manual != 30 {
		t.Fatalf("solo manual = %+v", filas[0])
	}
	f.Origen = reportes.OrigenTienda
	filas, _ = reportes.VentasPorPeriodo(conn, f, reportes.AgrupDia)
	if filas[0].Tienda != 30 || filas[0].Manual != 0 {
		t.Fatalf("solo tienda = %+v", filas[0])
	}

	// Un pedido cancelado deja de contar como venta.
	if err := ActualizarEstado(conn, in.PedidoID, EstadoCancelado); err != nil {
		t.Fatal(err)
	}
	f.Origen = reportes.OrigenTodos
	filas, _ = reportes.VentasPorPeriodo(conn, f, reportes.AgrupDia)
	if filas[0].Tienda != 0 || filas[0].Manual != 30 {
		t.Fatalf("tras cancelar = %+v, quiero tienda 0 y manual 30", filas[0])
	}

	// Rango de varios días: se rellenan los días sin ventas.
	f.Desde = dia.AddDate(0, 0, -2)
	filas, _ = reportes.VentasPorPeriodo(conn, f, reportes.AgrupDia)
	if len(filas) != 3 || filas[0].Total() != 0 || filas[2].Manual != 30 {
		t.Fatalf("rango de 3 días = %+v", filas)
	}
	for _, agrup := range []string{reportes.AgrupSemana, reportes.AgrupMes} {
		if filas, err = reportes.VentasPorPeriodo(conn, f, agrup); err != nil || len(filas) == 0 {
			t.Fatalf("agrupación %s: %v %+v", agrup, err, filas)
		}
	}

	// Productos y utilidad: costo = unidades × precio de compra actual.
	if _, err := conn.Exec(`UPDATE productos SET precio_compra = 4 WHERE id = $1`, p); err != nil {
		t.Fatal(err)
	}
	prods, err := reportes.VentasPorProducto(conn, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(prods) != 1 || prods[0].Unidades != 2 || prods[0].Ingresos != 30 || prods[0].Costo != 8 || prods[0].Utilidad() != 22 {
		t.Fatalf("productos = %+v", prods)
	}

	// Stock valorizado: 20 ingresos − 3 (tienda) − 2 − 1 = 14.
	stock, err := reportes.StockValorizado(conn, 0)
	if err != nil || len(stock) != 1 || stock[0].Stock != 14 || stock[0].ValorCosto() != 56 {
		t.Fatalf("stock = %+v (err %v)", stock, err)
	}

	// Pedidos por estado y por entrega.
	estados, err := reportes.PedidosPorEstado(conn, dia, dia)
	if err != nil || len(estados) != 1 || estados[0].Estado != EstadoCancelado || estados[0].Pedidos != 1 {
		t.Fatalf("estados = %+v (err %v)", estados, err)
	}
	if _, err := reportes.PedidosPorEntrega(conn, dia, dia); err != nil {
		t.Fatal(err)
	}
}

// El número de pedido de ventas anteriores a la columna pedido_id se rescata
// del motivo al migrar, y la migración puede repetirse sin efectos.
func TestMigrarPedidoIDDesdeMotivo(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Collar", 5)
	if err := inventario.CreateMovimiento(conn, p, "consumo", 1, "Venta online #42", true, 10, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := inventario.CreateMovimiento(conn, p, "consumo", 1, "Venta online #7 (a mano)", true, 10, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := inventario.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(`SELECT pedido_id FROM movimientos_inventario WHERE motivo = 'Venta online #42'`).Scan(&n); err != nil || n != 42 {
		t.Fatalf("pedido_id = %d (err %v), quiero 42", n, err)
	}
	var otros int
	conn.QueryRow(`SELECT COUNT(*) FROM movimientos_inventario WHERE pedido_id IS NOT NULL`).Scan(&otros)
	if otros != 1 {
		t.Fatalf("movimientos con pedido_id = %d, quiero 1 (el motivo con texto extra no cuenta)", otros)
	}
}
