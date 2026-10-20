package tienda

import (
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func entregaDe(t *testing.T, conn *sql.DB, pedido int) (metodo, direccion string, envio, total float64) {
	t.Helper()
	if err := conn.QueryRow(`SELECT metodo_entrega, direccion_entrega, costo_envio, total FROM pedidos WHERE id = $1`, pedido).
		Scan(&metodo, &direccion, &envio, &total); err != nil {
		t.Fatal(err)
	}
	return
}

// Los pedidos de prueba son de una unidad a Q 10 (ver linea).
func TestCambiarEntregaRecalculaElMontoSinTocarLaReserva(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	tr := nuevaTransferencia(t, conn, p, "ana@ejemplo.com") // recoger, Q 10
	var vence time.Time
	conn.QueryRow(`SELECT expira_en FROM pedidos WHERE id = $1`, tr.PedidoID).Scan(&vence)

	// Recoger → domicilio: se suma el envío vigente.
	res, err := CambiarEntrega(conn, tr.PedidoID, "domicilio", "  6a avenida 1-23  ", 45)
	if err != nil || !res.Cambio || res.CostoEnvio != 45 || res.Total != 55 {
		t.Fatalf("a domicilio: %+v, %v; quiero cambio, envío 45, total 55", res, err)
	}
	if m, d, e, tot := entregaDe(t, conn, tr.PedidoID); m != "domicilio" || d != "6a avenida 1-23" || e != 45 || tot != 55 {
		t.Fatalf("guardado: %s %q %v %v", m, d, e, tot)
	}

	// Ya a domicilio, cambiar solo la dirección conserva el costo con que se creó,
	// aunque el costo vigente haya subido.
	res, err = CambiarEntrega(conn, tr.PedidoID, "domicilio", "Zona 10", 60)
	if err != nil || !res.Cambio || res.CostoEnvio != 45 || res.Total != 55 {
		t.Fatalf("cambiar la dirección: %+v, %v; quiero conservar el envío de 45", res, err)
	}

	// Mismos datos: no cambia nada.
	if res, err := CambiarEntrega(conn, tr.PedidoID, "domicilio", "Zona 10", 60); err != nil || res.Cambio {
		t.Errorf("sin cambios: %+v, %v", res, err)
	}

	// Domicilio → recoger: sin envío y sin dirección.
	res, err = CambiarEntrega(conn, tr.PedidoID, "recoger", "esto se ignora", 45)
	if err != nil || !res.Cambio || res.CostoEnvio != 0 || res.Total != 10 {
		t.Fatalf("a recoger: %+v, %v", res, err)
	}
	if m, d, e, tot := entregaDe(t, conn, tr.PedidoID); m != "recoger" || d != "" || e != 0 || tot != 10 {
		t.Fatalf("guardado: %s %q %v %v", m, d, e, tot)
	}

	// Volver a domicilio usa el costo de hoy (ya no era a domicilio).
	if res, _ := CambiarEntrega(conn, tr.PedidoID, "domicilio", "Zona 1", 60); res.Total != 70 || res.CostoEnvio != 60 {
		t.Errorf("de nuevo a domicilio: %+v, quiero envío 60 y total 70", res)
	}

	// El plazo no se extendió y la reserva sigue igual.
	var despues time.Time
	conn.QueryRow(`SELECT expira_en FROM pedidos WHERE id = $1`, tr.PedidoID).Scan(&despues)
	if !despues.Equal(vence) {
		t.Errorf("el plazo cambió de %v a %v: cambiar la entrega no debe extenderlo", vence, despues)
	}
	if got := reservado(t, conn, p); got != 1 {
		t.Errorf("reservado = %v, quiero 1", got)
	}
	if got := estadoPedido(t, conn, tr.PedidoID); got != EstadoEsperaTransferencia {
		t.Errorf("estado = %q", got)
	}
	// Y todavía puede subir su comprobante.
	d := datosCompr("BOL-12345")
	d.Monto = 70
	if _, err := RegistrarComprobante(conn, tr.PedidoID, "x.jpg", d, ahora); err != nil {
		t.Errorf("subir el comprobante tras cambiar la entrega: %v", err)
	}
}

func TestCambiarEntregaValidaLosDatos(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	tr := nuevaTransferencia(t, conn, p, "ana@ejemplo.com")

	casos := map[string]struct{ metodo, direccion string }{
		"método desconocido":      {"dron", "Zona 1"},
		"método vacío":            {"", "Zona 1"},
		"domicilio sin dirección": {"domicilio", "   "},
		"dirección larguísima":    {"domicilio", strings.Repeat("a", 256)},
		"dirección con salto":     {"domicilio", "Zona 1\nBcc: x@y.z"},
	}
	for nombre, c := range casos {
		if _, err := CambiarEntrega(conn, tr.PedidoID, c.metodo, c.direccion, 45); !errors.Is(err, ErrEntregaInvalida) {
			t.Errorf("%s: err = %v, quiero ErrEntregaInvalida", nombre, err)
		}
	}
	if m, _, _, tot := entregaDe(t, conn, tr.PedidoID); m != "recoger" || tot != 10 {
		t.Errorf("un cambio inválido modificó el pedido: %s %v", m, tot)
	}
	// Un costo de envío raro no deja el total roto.
	if res, err := CambiarEntrega(conn, tr.PedidoID, "domicilio", "Zona 1", math.NaN()); err != nil || math.IsNaN(res.Total) || res.Total != 10 {
		t.Errorf("costo NaN: %+v, %v", res, err)
	}
}

func TestCambiarEntregaSoloMientrasSeEsperaLaTransferencia(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 20)

	enRevision, _ := pedidoEnRevision(t, conn, p, "a@ejemplo.com", "BOL-11111")
	pagado, comp := pedidoEnRevision(t, conn, p, "b@ejemplo.com", "BOL-22222")
	AprobarTransferencia(conn, pagado, comp, "silvia", ahora)
	cancelado := nuevaTransferencia(t, conn, p, "c@ejemplo.com")
	CancelarPedido(conn, cancelado.PedidoID, nil, "silvia", time.Now())
	vencido := nuevaTransferencia(t, conn, p, "d@ejemplo.com")
	conn.Exec(`UPDATE pedidos SET expira_en = now() - interval '1 minute' WHERE id = $1`, vencido.PedidoID)
	tarjeta, _ := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)

	for nombre, id := range map[string]int{
		"en revisión": enRevision, "pagado": pagado, "cancelado": cancelado.PedidoID, "vencido": vencido.PedidoID, "de tarjeta": tarjeta.PedidoID,
	} {
		antes, _, _, totalAntes := entregaDe(t, conn, id)
		if _, err := CambiarEntrega(conn, id, "domicilio", "Zona 1", 45); !errors.Is(err, ErrPedidoNoModificable) {
			t.Errorf("%s: err = %v, quiero ErrPedidoNoModificable", nombre, err)
		}
		if m, _, _, tot := entregaDe(t, conn, id); m != antes || tot != totalAntes {
			t.Errorf("%s: el pedido cambió a %s / %v", nombre, m, tot)
		}
	}
	if _, err := CambiarEntrega(conn, 99999, "recoger", "", 0); !errors.Is(err, ErrPedidoNoExiste) {
		t.Errorf("inexistente: err = %v", err)
	}

	// Si el negocio rechaza el comprobante y pide otro, vuelve a poder cambiarse.
	rech, compR := pedidoEnRevision(t, conn, p, "e@ejemplo.com", "BOL-33333")
	if err := RechazarTransferencia(conn, rech, compR, "silvia", "No vemos el depósito", false); err != nil {
		t.Fatal(err)
	}
	if res, err := CambiarEntrega(conn, rech, "domicilio", "Zona 1", 45); err != nil || res.Total != 55 {
		t.Errorf("tras rechazar el comprobante: %+v, %v", res, err)
	}
}

func TestCancelarPorElClienteSoloAntesDelComprobante(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)

	tr := nuevaTransferencia(t, conn, p, "ana@ejemplo.com")
	if err := CancelarPorElCliente(conn, tr.PedidoID); err != nil {
		t.Fatal(err)
	}
	if got := estadoPedido(t, conn, tr.PedidoID); got != EstadoCancelado {
		t.Errorf("estado = %q, quiero cancelado", got)
	}
	if got := reservado(t, conn, p); got != 0 {
		t.Errorf("reservado = %v, quiero 0", got)
	}
	if c, _ := GetCancelacion(conn, tr.PedidoID); c.Por != "cliente" || c.En == nil || !c.SinPago {
		t.Errorf("cancelación = %+v", c)
	}
	if err := CancelarPorElCliente(conn, tr.PedidoID); !errors.Is(err, ErrPedidoCancelado) {
		t.Errorf("segunda vez: err = %v", err)
	}

	enRevision, _ := pedidoEnRevision(t, conn, p, "b@ejemplo.com", "BOL-11111")
	if err := CancelarPorElCliente(conn, enRevision); !errors.Is(err, ErrEnRevision) {
		t.Errorf("en revisión: err = %v, quiero ErrEnRevision", err)
	}
	pagado, comp := pedidoEnRevision(t, conn, p, "c@ejemplo.com", "BOL-22222")
	AprobarTransferencia(conn, pagado, comp, "silvia", ahora)
	if err := CancelarPorElCliente(conn, pagado); !errors.Is(err, ErrNoSePuedeCancelar) {
		t.Errorf("pagado: err = %v, quiero ErrNoSePuedeCancelar", err)
	}
	tarjeta, _ := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)
	if err := CancelarPorElCliente(conn, tarjeta.PedidoID); !errors.Is(err, ErrNoSePuedeCancelar) {
		t.Errorf("de tarjeta: err = %v, quiero ErrNoSePuedeCancelar", err)
	}
	for _, id := range []int{enRevision, pagado, tarjeta.PedidoID} {
		if got := estadoPedido(t, conn, id); got == EstadoCancelado {
			t.Errorf("el pedido %d quedó cancelado", id)
		}
	}
	if err := CancelarPorElCliente(conn, 99999); !errors.Is(err, ErrPedidoNoExiste) {
		t.Errorf("inexistente: err = %v", err)
	}
}

func TestElMontoTransferidoSeValidaYSeGuarda(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)

	for nombre, monto := range map[string]float64{"cero": 0, "negativo": -5, "enorme": 1000000, "NaN": math.NaN(), "infinito": math.Inf(1)} {
		d := datosCompr("BOL-12345")
		d.Monto = monto
		if _, err := d.Limpiar(ahora); !errors.Is(err, ErrComprobanteInvalido) {
			t.Errorf("%s: err = %v, quiero ErrComprobanteInvalido", nombre, err)
		}
	}
	d := datosCompr("BOL-12345")
	d.Monto = 70.456
	if lim, err := d.Limpiar(ahora); err != nil || lim.Monto != 70.46 {
		t.Errorf("redondeo: %+v, %v; quiero 70.46", lim, err)
	}

	// Se guarda aunque no coincida con el total: el negocio lo compara al revisar.
	tr := nuevaTransferencia(t, conn, p, "ana@ejemplo.com") // total 10
	d.Monto = 7.5
	if _, err := RegistrarComprobante(conn, tr.PedidoID, "x.jpg", d, ahora); err != nil {
		t.Fatal(err)
	}
	lista, _ := ListComprobantes(conn, tr.PedidoID)
	if len(lista) != 1 || lista[0].Monto != 7.5 {
		t.Fatalf("comprobante = %+v, quiero monto 7.50", lista)
	}
}
