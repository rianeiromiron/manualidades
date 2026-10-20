package tienda

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"manualidades/internal/inventario"
)

// pedidoEnRevision deja un pedido de transferencia con su comprobante subido.
func pedidoEnRevision(t *testing.T, conn *sql.DB, producto int, email, boleta string) (pedido, comprobante int) {
	t.Helper()
	tr := nuevaTransferencia(t, conn, producto, email)
	id, err := RegistrarComprobante(conn, tr.PedidoID, "x.jpg", datosCompr(boleta), ahora)
	if err != nil {
		t.Fatal(err)
	}
	return tr.PedidoID, id
}

func contar(t *testing.T, conn *sql.DB, consulta string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(consulta, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAprobarTransferenciaDescuentaInventarioYRegistraElPago(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido, comp := pedidoEnRevision(t, conn, p, "ana@ejemplo.com", "BOL-12345")

	if err := AprobarTransferencia(conn, pedido, comp, "silvia", ahora); err != nil {
		t.Fatal(err)
	}
	if got := estadoPedido(t, conn, pedido); got != EstadoPagado {
		t.Fatalf("estado = %q, quiero pagado", got)
	}
	if got := stockFisico(t, conn, p); got != 4 {
		t.Errorf("stock físico = %v, quiero 4 (se vendió 1)", got)
	}
	if got := reservado(t, conn, p); got != 0 {
		t.Errorf("reservado = %v, quiero 0 (ya es una venta, no una reserva)", got)
	}
	if n := contar(t, conn, `SELECT count(*) FROM movimientos_inventario WHERE producto_id = $1 AND tipo = 'consumo'`, p); n != 1 {
		t.Errorf("movimientos de consumo = %d, quiero 1", n)
	}

	var metodo, estado, ref, nota string
	var monto float64
	if err := conn.QueryRow(`SELECT metodo, estado, referencia, motivo_rechazo, monto FROM pagos WHERE pedido_id = $1`, pedido).
		Scan(&metodo, &estado, &ref, &nota, &monto); err != nil {
		t.Fatal(err)
	}
	if metodo != "transferencia" || estado != EstadoPagoAprobado || monto != 10 ||
		!strings.Contains(ref, "BOL-12345") || !strings.Contains(ref, "Banco Industrial") || !strings.Contains(nota, "silvia") {
		t.Errorf("pago = %s %s %q %q %v", metodo, estado, ref, nota, monto)
	}

	lista, _ := ListComprobantes(conn, pedido)
	if lista[0].Estado != ComprobanteAceptado || lista[0].RevisadoPor != "silvia" || lista[0].RevisadoEn == nil {
		t.Errorf("comprobante = %+v", lista[0])
	}

	// Ahora es un pedido real: bodega lo ve y puede avanzar su estado.
	if reales, _ := ListPedidos(conn); len(reales) != 1 {
		t.Errorf("ListPedidos = %d, quiero 1", len(reales))
	}
	if err := ActualizarEstado(conn, pedido, EstadoProcesando); err != nil {
		t.Errorf("pasar a procesando: %v", err)
	}
}

func TestAprobarDosVecesNoDescuentaDosVeces(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido, comp := pedidoEnRevision(t, conn, p, "ana@ejemplo.com", "BOL-12345")
	if err := AprobarTransferencia(conn, pedido, comp, "silvia", ahora); err != nil {
		t.Fatal(err)
	}
	if err := AprobarTransferencia(conn, pedido, comp, "beto", ahora); !errors.Is(err, ErrPedidoNoRevisable) {
		t.Fatalf("segunda aprobación: err = %v, quiero ErrPedidoNoRevisable", err)
	}
	if got := stockFisico(t, conn, p); got != 4 {
		t.Errorf("stock = %v, quiero 4", got)
	}
	if n := contar(t, conn, `SELECT count(*) FROM pagos WHERE pedido_id = $1`, pedido); n != 1 {
		t.Errorf("hay %d pagos, quiero 1", n)
	}
}

func TestAprobarSinStockFisicoNoCambiaNada(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 1)
	pedido, comp := pedidoEnRevision(t, conn, p, "ana@ejemplo.com", "BOL-12345")

	// El stock se consumió a mano mientras el pedido esperaba revisión.
	if err := inventario.CreateMovimiento(conn, p, "consumo", 1, "se rompió", false, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	err := AprobarTransferencia(conn, pedido, comp, "silvia", ahora)
	if !errors.Is(err, inventario.ErrStockInsuficiente) {
		t.Fatalf("err = %v, quiero ErrStockInsuficiente", err)
	}
	if got := estadoPedido(t, conn, pedido); got != EstadoTransferenciaPorRevisar {
		t.Errorf("estado = %q, quiero que siga en revisión", got)
	}
	if n := contar(t, conn, `SELECT count(*) FROM pagos WHERE pedido_id = $1`, pedido); n != 0 {
		t.Errorf("hay %d pagos registrados, quiero 0", n)
	}
	lista, _ := ListComprobantes(conn, pedido)
	if lista[0].Estado != ComprobantePorRevisar {
		t.Errorf("el comprobante pasó a %q", lista[0].Estado)
	}
	if got := stockFisico(t, conn, p); got != 0 {
		t.Errorf("stock = %v, quiero 0", got)
	}
}

func TestRechazarPidiendoOtroComprobanteDaPlazoNuevo(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido, comp := pedidoEnRevision(t, conn, p, "ana@ejemplo.com", "BOL-12345")

	if err := RechazarTransferencia(conn, pedido, comp, "silvia", "  No encontramos el depósito  ", false); err != nil {
		t.Fatal(err)
	}
	if got := estadoPedido(t, conn, pedido); got != EstadoEsperaTransferencia {
		t.Fatalf("estado = %q, quiero espera_transferencia", got)
	}
	var horas float64
	conn.QueryRow(`SELECT EXTRACT(EPOCH FROM (expira_en - now())) / 3600 FROM pedidos WHERE id = $1`, pedido).Scan(&horas)
	if horas < 47.9 || horas > 48.0 {
		t.Errorf("plazo nuevo = %.2f h, quiero 48", horas)
	}
	if got := reservado(t, conn, p); got != 1 {
		t.Errorf("reservado = %v, quiero 1 (el cliente conserva su reserva)", got)
	}
	lista, _ := ListComprobantes(conn, pedido)
	if lista[0].Estado != ComprobanteRechazado || lista[0].Nota != "No encontramos el depósito" || lista[0].RevisadoPor != "silvia" {
		t.Errorf("comprobante = %+v", lista[0])
	}
	if got := stockFisico(t, conn, p); got != 5 {
		t.Errorf("stock = %v: rechazar no debe tocar el inventario", got)
	}

	// El cliente puede volver a subir, incluso la misma boleta (la rechazada se libera).
	if _, err := RegistrarComprobante(conn, pedido, "y.jpg", datosCompr("BOL-12345"), ahora); err != nil {
		t.Fatalf("subir de nuevo: %v", err)
	}
	if got := estadoPedido(t, conn, pedido); got != EstadoTransferenciaPorRevisar {
		t.Errorf("estado = %q, quiero por revisar otra vez", got)
	}
}

func TestRechazarYCancelarLiberaElInventario(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido, comp := pedidoEnRevision(t, conn, p, "ana@ejemplo.com", "BOL-12345")

	if err := RechazarTransferencia(conn, pedido, comp, "silvia", "Depósito no encontrado", true); err != nil {
		t.Fatal(err)
	}
	if got := estadoPedido(t, conn, pedido); got != EstadoExpirado {
		t.Fatalf("estado = %q, quiero expirado", got)
	}
	if got := reservado(t, conn, p); got != 0 {
		t.Errorf("reservado = %v, quiero 0", got)
	}
	var sinFecha bool
	conn.QueryRow(`SELECT expira_en IS NULL FROM pedidos WHERE id = $1`, pedido).Scan(&sinFecha)
	if !sinFecha {
		t.Error("un pedido cancelado no debe conservar expira_en")
	}
	if _, err := RegistrarComprobante(conn, pedido, "z.jpg", datosCompr("BOL-99999"), ahora); !errors.Is(err, ErrPedidoNoAceptaComprobante) {
		t.Errorf("subir a un pedido cancelado: err = %v", err)
	}
}

func TestRechazarExigeUnMotivoValido(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido, comp := pedidoEnRevision(t, conn, p, "ana@ejemplo.com", "BOL-12345")

	for nombre, motivo := range map[string]string{
		"vacío": "", "solo espacios": "    ", "muy corto": "no", "con salto de línea": "mal\nBcc: x@y.z", "larguísimo": strings.Repeat("a", 256),
	} {
		if err := RechazarTransferencia(conn, pedido, comp, "silvia", motivo, false); !errors.Is(err, ErrComprobanteInvalido) {
			t.Errorf("%s: err = %v, quiero ErrComprobanteInvalido", nombre, err)
		}
	}
	if got := estadoPedido(t, conn, pedido); got != EstadoTransferenciaPorRevisar {
		t.Errorf("un rechazo inválido cambió el estado a %q", got)
	}
}

func TestRevisionNoMezclaPedidosNiAcepta(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 10)
	a, compA := pedidoEnRevision(t, conn, p, "a@ejemplo.com", "BOL-11111")
	b, _ := pedidoEnRevision(t, conn, p, "b@ejemplo.com", "BOL-22222")
	tarjeta, _ := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)

	// Un comprobante de otro pedido no sirve para aprobar este.
	if err := AprobarTransferencia(conn, b, compA, "silvia", ahora); !errors.Is(err, ErrComprobanteNoRevisable) {
		t.Errorf("comprobante ajeno: err = %v, quiero ErrComprobanteNoRevisable", err)
	}
	if err := AprobarTransferencia(conn, tarjeta.PedidoID, compA, "silvia", ahora); !errors.Is(err, ErrPedidoNoRevisable) {
		t.Errorf("pedido de tarjeta: err = %v, quiero ErrPedidoNoRevisable", err)
	}
	if err := AprobarTransferencia(conn, 99999, compA, "silvia", ahora); !errors.Is(err, ErrPedidoNoExiste) {
		t.Errorf("pedido inexistente: err = %v, quiero ErrPedidoNoExiste", err)
	}
	if err := RechazarTransferencia(conn, b, compA, "silvia", "motivo válido", false); !errors.Is(err, ErrComprobanteNoRevisable) {
		t.Errorf("rechazar con comprobante ajeno: err = %v", err)
	}
	for _, id := range []int{a, b} {
		if got := estadoPedido(t, conn, id); got != EstadoTransferenciaPorRevisar {
			t.Errorf("pedido %d quedó en %q", id, got)
		}
	}
}

func TestDosRevisoresSimultaneosSoloUnoGana(t *testing.T) {
	conn := nuevaBase(t)
	conn.SetMaxOpenConns(10)
	p := nuevoProducto(t, conn, "Taza", 5)
	pedido, comp := pedidoEnRevision(t, conn, p, "ana@ejemplo.com", "BOL-12345")

	var ok, perdieron, otros atomic.Int32
	var wg sync.WaitGroup
	arranque := make(chan struct{})
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-arranque
			var err error
			if i%2 == 0 {
				err = AprobarTransferencia(conn, pedido, comp, "revisor", ahora)
			} else {
				err = RechazarTransferencia(conn, pedido, comp, "revisor", "motivo válido", true)
			}
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrPedidoNoRevisable), errors.Is(err, ErrComprobanteNoRevisable):
				perdieron.Add(1)
			default:
				otros.Add(1)
			}
		}(i)
	}
	close(arranque)
	wg.Wait()
	if ok.Load() != 1 || perdieron.Load() != 5 || otros.Load() != 0 {
		t.Fatalf("ok %d, perdieron %d, otros %d; quiero 1/5/0", ok.Load(), perdieron.Load(), otros.Load())
	}
	// Sea cual haya sido el resultado, el inventario y los pagos son coherentes.
	switch estadoPedido(t, conn, pedido) {
	case EstadoPagado:
		if stockFisico(t, conn, p) != 4 || contar(t, conn, `SELECT count(*) FROM pagos WHERE pedido_id = $1`, pedido) != 1 {
			t.Error("pagado, pero el inventario o los pagos no cuadran")
		}
	case EstadoExpirado:
		if stockFisico(t, conn, p) != 5 || contar(t, conn, `SELECT count(*) FROM pagos WHERE pedido_id = $1`, pedido) != 0 {
			t.Error("cancelado, pero se tocó el inventario o se registró un pago")
		}
	default:
		t.Errorf("estado final inesperado: %q", estadoPedido(t, conn, pedido))
	}
}
