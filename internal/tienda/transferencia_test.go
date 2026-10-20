package tienda

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"manualidades/internal/inventario"
)

func TestTransferenciaReservaStockPor48Horas(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)

	tr, err := IniciarTransferencia(conn, datos(), []LineaPedido{linea(p, 3)}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Token == "" {
		t.Error("el pedido debe tener token para que el cliente vuelva a él")
	}
	if got := estadoPedido(t, conn, tr.PedidoID); got != EstadoEsperaTransferencia {
		t.Fatalf("estado = %q, quiero espera_transferencia", got)
	}

	// Plazo: 48 horas desde ahora (tolerancia de un minuto).
	var horas float64
	if err := conn.QueryRow(
		`SELECT EXTRACT(EPOCH FROM (expira_en - now())) / 3600 FROM pedidos WHERE id = $1`, tr.PedidoID,
	).Scan(&horas); err != nil {
		t.Fatal(err)
	}
	if horas < 47.98 || horas > 48.0 {
		t.Fatalf("la reserva vence en %.3f h, quiero 48", horas)
	}

	// Reserva, pero el kardex no se toca y no hay cobro registrado.
	if got := reservado(t, conn, p); got != 3 {
		t.Fatalf("reservado = %v, quiero 3", got)
	}
	if got := stockFisico(t, conn, p); got != 5 {
		t.Fatalf("stock físico = %v, quiero 5 (la transferencia aún no se confirma)", got)
	}
	var pagos int
	conn.QueryRow(`SELECT count(*) FROM pagos WHERE pedido_id = $1`, tr.PedidoID).Scan(&pagos)
	if pagos != 0 {
		t.Fatalf("hay %d pagos, quiero 0", pagos)
	}

	// Otro cliente solo puede tomar lo que queda.
	if _, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 3)}, 30); !errors.Is(err, inventario.ErrStockInsuficiente) {
		t.Fatalf("pedir 3 con 2 disponibles: err = %v, quiero ErrStockInsuficiente", err)
	}
	if _, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 2)}, 20); err != nil {
		t.Fatalf("pedir 2 con 2 disponibles: %v", err)
	}

	// La reserva de tarjeta (20 min) no aplica: la limpieza no toca este pedido.
	if n, err := LimpiarExpirados(conn); err != nil || n != 0 {
		t.Fatalf("LimpiarExpirados = (%d, %v), quiero (0, nil)", n, err)
	}
	if got := estadoPedido(t, conn, tr.PedidoID); got != EstadoEsperaTransferencia {
		t.Fatalf("estado tras limpieza = %q, quiero espera_transferencia", got)
	}
}

func TestTransferenciaSinStockNoGuardaNada(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 2)

	_, err := IniciarTransferencia(conn, datos(), []LineaPedido{linea(p, 3)}, 30)
	if !errors.Is(err, inventario.ErrStockInsuficiente) {
		t.Fatalf("err = %v, quiero ErrStockInsuficiente", err)
	}
	var n int
	conn.QueryRow(`SELECT count(*) FROM pedidos`).Scan(&n)
	if n != 0 {
		t.Fatalf("quedaron %d pedidos, quiero 0", n)
	}
	if _, err := IniciarTransferencia(conn, datos(), nil, 0); !errors.Is(err, ErrCarritoVacio) {
		t.Fatalf("carrito vacío: err = %v, quiero ErrCarritoVacio", err)
	}
}

func TestTransferenciaVencidaLiberaElStock(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 3)

	tr, err := IniciarTransferencia(conn, datos(), []LineaPedido{linea(p, 3)}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE pedidos SET expira_en = now() - interval '1 minute' WHERE id = $1`, tr.PedidoID); err != nil {
		t.Fatal(err)
	}

	// Aunque la limpieza no haya corrido, el stock ya vuelve a estar libre.
	if got := reservado(t, conn, p); got != 0 {
		t.Fatalf("reservado con plazo vencido = %v, quiero 0", got)
	}
	if n, err := LimpiarExpirados(conn); err != nil || n != 1 {
		t.Fatalf("LimpiarExpirados = (%d, %v), quiero (1, nil)", n, err)
	}
	if got := estadoPedido(t, conn, tr.PedidoID); got != EstadoExpirado {
		t.Fatalf("estado = %q, quiero expirado", got)
	}
	var sinFecha bool
	conn.QueryRow(`SELECT expira_en IS NULL FROM pedidos WHERE id = $1`, tr.PedidoID).Scan(&sinFecha)
	if !sinFecha {
		t.Error("un pedido expirado no debe conservar expira_en")
	}
}

// Con el comprobante subido la reserva ya no vence: el negocio puede tardar en
// revisarlo sin que el cliente que ya pagó pierda su pedido.
func TestTransferenciaPorRevisarNoVenceYSigueReservando(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 3)

	tr, err := IniciarTransferencia(conn, datos(), []LineaPedido{linea(p, 3)}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(
		`UPDATE pedidos SET estado = $1, expira_en = NULL WHERE id = $2`, EstadoTransferenciaPorRevisar, tr.PedidoID,
	); err != nil {
		t.Fatal(err)
	}

	if got := reservado(t, conn, p); got != 3 {
		t.Fatalf("reservado = %v, quiero 3", got)
	}
	if n, err := LimpiarExpirados(conn); err != nil || n != 0 {
		t.Fatalf("LimpiarExpirados = (%d, %v), quiero (0, nil)", n, err)
	}
	if got := estadoPedido(t, conn, tr.PedidoID); got != EstadoTransferenciaPorRevisar {
		t.Fatalf("estado = %q, quiero transferencia_por_revisar", got)
	}
	if _, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10); !errors.Is(err, inventario.ErrStockInsuficiente) {
		t.Fatalf("otro cliente no debe poder tomar lo reservado: err = %v", err)
	}

	// El admin lo ve entre los pendientes, y bodega no (aún no está pagado).
	pend, err := ListPedidosPendientes(conn)
	if err != nil || len(pend) != 1 || pend[0].ID != tr.PedidoID {
		t.Fatalf("pendientes = %+v, %v; quiero solo el de transferencia", pend, err)
	}
	reales, _ := ListPedidos(conn)
	if len(reales) != 0 {
		t.Fatalf("ListPedidos trajo %d pedidos; uno sin pagar no es un pedido real", len(reales))
	}
	if err := ActualizarEstado(conn, tr.PedidoID, EstadoEntregado); !errors.Is(err, ErrEstadoNoModificable) {
		t.Fatalf("entregar un pedido sin pagar: err = %v, quiero ErrEstadoNoModificable", err)
	}
}

// Sin sobreventa aunque compitan clientes de tarjeta y de transferencia por
// la misma unidad.
func TestTransferenciaYTarjetaNoSobrevenden(t *testing.T) {
	conn := nuevaBase(t)
	conn.SetMaxOpenConns(20)
	p := nuevoProducto(t, conn, "Unico", 1)

	var ok, sinStock, otros atomic.Int32
	var wg sync.WaitGroup
	arranque := make(chan struct{})
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-arranque
			var err error
			if i%2 == 0 {
				_, err = IniciarTransferencia(conn, datos(), []LineaPedido{linea(p, 1)}, 10)
			} else {
				_, err = IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)
			}
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, inventario.ErrStockInsuficiente):
				sinStock.Add(1)
			default:
				otros.Add(1)
			}
		}(i)
	}
	close(arranque)
	wg.Wait()

	if ok.Load() != 1 || sinStock.Load() != 11 || otros.Load() != 0 {
		t.Fatalf("reservaron %d, sin stock %d, otros errores %d; quiero 1/11/0", ok.Load(), sinStock.Load(), otros.Load())
	}
}

// 'transferencia_por_revisar' mide 25 caracteres: la columna debe admitirlo, y
// migrar de nuevo no debe fallar ni cambiar nada.
func TestMigrateAmpliaLaColumnaEstadoYEsIdempotente(t *testing.T) {
	conn := nuevaBase(t)
	if err := Migrate(conn); err != nil {
		t.Fatalf("segunda migración: %v", err)
	}
	var largo int
	if err := conn.QueryRow(
		`SELECT character_maximum_length FROM information_schema.columns
		 WHERE table_name = 'pedidos' AND column_name = 'estado'`,
	).Scan(&largo); err != nil {
		t.Fatal(err)
	}
	if largo < len(EstadoTransferenciaPorRevisar) {
		t.Fatalf("estado admite %d caracteres, necesito %d", largo, len(EstadoTransferenciaPorRevisar))
	}
	var indices int
	conn.QueryRow(`SELECT count(*) FROM pg_indexes WHERE tablename = 'pedidos' AND indexname LIKE 'idx_pedidos_reserva%'`).Scan(&indices)
	if indices != 1 {
		t.Fatalf("hay %d índices de reserva, quiero 1", indices)
	}
}
