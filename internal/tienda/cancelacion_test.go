package tienda

import (
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pedidoPagadoConTarjeta deja un pedido pagado de 2 unidades (Q 30) y devuelve
// su id. El stock del producto baja de su valor inicial en 2.
func pedidoPagadoConTarjeta(t *testing.T, conn *sql.DB, producto int) int {
	t.Helper()
	in, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(producto, 2)}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfirmarPago(conn, in, ResultadoPago{Referencia: "REF-1", Marca: "visa", Ultimos4: "1111"}); err != nil {
		t.Fatal(err)
	}
	return in.PedidoID
}

func reembolsoDe(t *testing.T, conn *sql.DB, pedido int) string {
	t.Helper()
	var r string
	conn.QueryRow(`SELECT reembolso FROM pagos WHERE pedido_id = $1 AND estado = 'aprobado'`, pedido).Scan(&r)
	return r
}

func TestCancelarUnPedidoPagadoDevuelveElInventarioUnaSolaVez(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido := pedidoPagadoConTarjeta(t, conn, p)
	if got := stockFisico(t, conn, p); got != 3 {
		t.Fatalf("stock tras la venta = %v, quiero 3", got)
	}

	res, err := CancelarPedido(conn, pedido, nil, "silvia", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !res.InventarioDevuelto || res.ReembolsoPendiente != 30 {
		t.Errorf("resultado = %+v, quiero inventario devuelto y Q 30 por reembolsar", res)
	}
	if got := stockFisico(t, conn, p); got != 5 {
		t.Errorf("stock tras cancelar = %v, quiero 5", got)
	}
	if got := estadoPedido(t, conn, pedido); got != EstadoCancelado {
		t.Errorf("estado = %q, quiero cancelado", got)
	}
	// El historial se conserva: el consumo sigue y una anulación lo compensa.
	if n := contar(t, conn, `SELECT count(*) FROM movimientos_inventario WHERE pedido_id = $1 AND tipo = 'consumo'`, pedido); n != 1 {
		t.Errorf("consumos del pedido = %d, quiero 1 (el historial no se borra)", n)
	}
	if n := contar(t, conn, `SELECT count(*) FROM movimientos_inventario WHERE pedido_id = $1 AND tipo = 'ingreso' AND anula_a IS NOT NULL`, pedido); n != 1 {
		t.Errorf("anulaciones = %d, quiero 1", n)
	}
	c, err := GetCancelacion(conn, pedido)
	if err != nil || c.Por != "silvia" || c.En == nil || !c.InventarioDevuelto {
		t.Errorf("cancelación = %+v, %v", c, err)
	}
	if got := reembolsoDe(t, conn, pedido); got != ReembolsoPendiente {
		t.Errorf("reembolso = %q, quiero pendiente", got)
	}

	// Es definitivo y no devuelve dos veces.
	if _, err := CancelarPedido(conn, pedido, nil, "beto", time.Now()); !errors.Is(err, ErrPedidoCancelado) {
		t.Errorf("segunda cancelación: err = %v, quiero ErrPedidoCancelado", err)
	}
	for _, e := range []string{EstadoPagado, EstadoProcesando, EstadoEntregado} {
		if err := ActualizarEstado(conn, pedido, e); !errors.Is(err, ErrPedidoCancelado) {
			t.Errorf("reabrir como %s: err = %v, quiero ErrPedidoCancelado", e, err)
		}
	}
	if got := stockFisico(t, conn, p); got != 5 {
		t.Errorf("stock = %v, quiero 5", got)
	}
	if got := estadoPedido(t, conn, pedido); got != EstadoCancelado {
		t.Errorf("el pedido cancelado cambió a %q", got)
	}
}

func TestCancelarDesdeProcesandoTambienDevuelveAunqueSeDigaQueNo(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido := pedidoPagadoConTarjeta(t, conn, p)
	if err := ActualizarEstado(conn, pedido, EstadoProcesando); err != nil {
		t.Fatal(err)
	}
	no := false
	// Para un pedido que aún no sale de bodega, la respuesta del llamador no cuenta.
	res, err := CancelarPedido(conn, pedido, &no, "silvia", time.Now())
	if err != nil || !res.InventarioDevuelto {
		t.Fatalf("resultado = %+v, %v", res, err)
	}
	if got := stockFisico(t, conn, p); got != 5 {
		t.Errorf("stock = %v, quiero 5", got)
	}
}

func TestCancelarUnPedidoEntregadoPreguntaSiElProductoVolvio(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 10)

	// Sin decisión: no se hace nada.
	sinDecidir := pedidoPagadoConTarjeta(t, conn, p)
	ActualizarEstado(conn, sinDecidir, EstadoEntregado)
	if _, err := CancelarPedido(conn, sinDecidir, nil, "silvia", time.Now()); !errors.Is(err, ErrFaltaDecisionDeInventario) {
		t.Fatalf("err = %v, quiero ErrFaltaDecisionDeInventario", err)
	}
	if got := estadoPedido(t, conn, sinDecidir); got != EstadoEntregado {
		t.Errorf("sin decisión el estado cambió a %q", got)
	}
	if got := stockFisico(t, conn, p); got != 8 {
		t.Errorf("sin decisión el stock cambió a %v", got)
	}

	// El producto no volvió: el inventario no se toca, pero el dinero sí queda por devolver.
	no := false
	res, err := CancelarPedido(conn, sinDecidir, &no, "silvia", time.Now())
	if err != nil || res.InventarioDevuelto || res.ReembolsoPendiente != 30 {
		t.Fatalf("resultado = %+v, %v", res, err)
	}
	if got := stockFisico(t, conn, p); got != 8 {
		t.Errorf("stock = %v, quiero 8 (no volvió)", got)
	}
	if c, _ := GetCancelacion(conn, sinDecidir); c.InventarioDevuelto {
		t.Error("debe quedar anotado que el inventario no se devolvió")
	}
	if got := reembolsoDe(t, conn, sinDecidir); got != ReembolsoPendiente {
		t.Errorf("reembolso = %q", got)
	}

	// El producto sí volvió.
	volvio := pedidoPagadoConTarjeta(t, conn, p) // stock 6
	ActualizarEstado(conn, volvio, EstadoEntregado)
	si := true
	if res, err := CancelarPedido(conn, volvio, &si, "silvia", time.Now()); err != nil || !res.InventarioDevuelto {
		t.Fatalf("resultado = %+v, %v", res, err)
	}
	if got := stockFisico(t, conn, p); got != 8 {
		t.Errorf("stock = %v, quiero 8 (volvieron 2)", got)
	}
}

func TestSoloSeCancelaUnPedidoPagado(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 10)

	tarjeta, _ := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)
	espera := nuevaTransferencia(t, conn, p, "a@ejemplo.com")
	enRevision, _ := pedidoEnRevision(t, conn, p, "b@ejemplo.com", "BOL-12345")
	vencido := nuevaTransferencia(t, conn, p, "c@ejemplo.com")
	conn.Exec(`UPDATE pedidos SET estado = 'expirado', expira_en = NULL WHERE id = $1`, vencido.PedidoID)

	for nombre, id := range map[string]int{"cobrando": tarjeta.PedidoID, "esperando": espera.PedidoID, "en revisión": enRevision, "vencido": vencido.PedidoID} {
		antes := estadoPedido(t, conn, id)
		if _, err := CancelarPedido(conn, id, nil, "silvia", time.Now()); !errors.Is(err, ErrNoSePuedeCancelar) {
			t.Errorf("%s: err = %v, quiero ErrNoSePuedeCancelar", nombre, err)
		}
		if got := estadoPedido(t, conn, id); got != antes {
			t.Errorf("%s: el estado cambió de %q a %q", nombre, antes, got)
		}
	}
	if _, err := CancelarPedido(conn, 99999, nil, "silvia", time.Now()); !errors.Is(err, ErrPedidoNoExiste) {
		t.Errorf("inexistente: err = %v", err)
	}
}

func TestActualizarEstadoYaNoCancela(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido := pedidoPagadoConTarjeta(t, conn, p)
	if err := ActualizarEstado(conn, pedido, EstadoCancelado); !errors.Is(err, ErrUsarCancelacion) {
		t.Fatalf("err = %v, quiero ErrUsarCancelacion", err)
	}
	if got := estadoPedido(t, conn, pedido); got != EstadoPagado {
		t.Errorf("estado = %q: cambiar a cancelado sin devolver inventario dejaría el kardex descuadrado", got)
	}
}

func TestMarcarReembolsado(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido := pedidoPagadoConTarjeta(t, conn, p)

	if err := MarcarReembolsado(conn, pedido, "silvia"); !errors.Is(err, ErrSinReembolsoPendiente) {
		t.Fatalf("antes de cancelar: err = %v, quiero ErrSinReembolsoPendiente", err)
	}
	CancelarPedido(conn, pedido, nil, "silvia", time.Now())
	if err := MarcarReembolsado(conn, pedido, "beto"); err != nil {
		t.Fatal(err)
	}
	pg, _ := GetPagoPorPedido(conn, pedido)
	if pg.Reembolso != ReembolsoHecho || pg.ReembolsoPor != "beto" || pg.ReembolsoEn == nil {
		t.Errorf("pago = %+v", pg)
	}
	if err := MarcarReembolsado(conn, pedido, "beto"); !errors.Is(err, ErrSinReembolsoPendiente) {
		t.Errorf("repetir: err = %v, quiero ErrSinReembolsoPendiente", err)
	}
}

func TestCancelarUnaTransferenciaAprobadaMarcaElReembolso(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido, comp := pedidoEnRevision(t, conn, p, "ana@ejemplo.com", "BOL-12345")
	if err := AprobarTransferencia(conn, pedido, comp, "silvia", ahora); err != nil {
		t.Fatal(err)
	}
	res, err := CancelarPedido(conn, pedido, nil, "silvia", time.Now())
	if err != nil || res.ReembolsoPendiente != 10 || !res.InventarioDevuelto {
		t.Fatalf("resultado = %+v, %v", res, err)
	}
	if got := stockFisico(t, conn, p); got != 5 {
		t.Errorf("stock = %v, quiero 5", got)
	}
	if got := reembolsoDe(t, conn, pedido); got != ReembolsoPendiente {
		t.Errorf("reembolso = %q", got)
	}
}

func TestDosCancelacionesSimultaneasDevuelvenUnaSolaVez(t *testing.T) {
	conn := nuevaBase(t)
	conn.SetMaxOpenConns(10)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido := pedidoPagadoConTarjeta(t, conn, p)

	var ok, perdieron, otros atomic.Int32
	var wg sync.WaitGroup
	arranque := make(chan struct{})
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-arranque
			_, err := CancelarPedido(conn, pedido, nil, "revisor", time.Now())
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrPedidoCancelado):
				perdieron.Add(1)
			default:
				otros.Add(1)
			}
		}()
	}
	close(arranque)
	wg.Wait()
	if ok.Load() != 1 || perdieron.Load() != 5 || otros.Load() != 0 {
		t.Fatalf("ok %d, perdieron %d, otros %d; quiero 1/5/0", ok.Load(), perdieron.Load(), otros.Load())
	}
	if got := stockFisico(t, conn, p); got != 5 {
		t.Errorf("stock = %v, quiero 5 (devuelto una sola vez)", got)
	}
}

func TestCancelarVariosProductosDevuelveCadaUno(t *testing.T) {
	conn := nuevaBase(t)
	a := nuevoProducto(t, conn, "Taza", 5)
	b := nuevoProducto(t, conn, "Plato", 7)
	in, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(a, 2), linea(b, 3)}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfirmarPago(conn, in, ResultadoPago{Referencia: "R"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CancelarPedido(conn, in.PedidoID, nil, "silvia", time.Now()); err != nil {
		t.Fatal(err)
	}
	if stockFisico(t, conn, a) != 5 || stockFisico(t, conn, b) != 7 {
		t.Errorf("stocks = %v y %v, quiero 5 y 7", stockFisico(t, conn, a), stockFisico(t, conn, b))
	}
}
