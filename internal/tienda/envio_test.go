package tienda

import (
	"database/sql"
	"testing"
)

func costoGuardado(t *testing.T, conn *sql.DB, pedido int) (total, envio float64) {
	t.Helper()
	if err := conn.QueryRow(`SELECT total, costo_envio FROM pedidos WHERE id = $1`, pedido).Scan(&total, &envio); err != nil {
		t.Fatal(err)
	}
	return total, envio
}

func TestElCostoDeEnvioQuedaGuardadoEnElPedidoDeTarjeta(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)

	d := datos()
	d.MetodoEntrega = "domicilio"
	d.DireccionEntrega = "Zona 1"
	d.CostoEnvio = 45
	in, err := IniciarPago(conn, "", d, []LineaPedido{linea(p, 2)}, 75) // 30 de producto + 45 de envío
	if err != nil {
		t.Fatal(err)
	}
	if total, envio := costoGuardado(t, conn, in.PedidoID); total != 75 || envio != 45 {
		t.Fatalf("total %v, envío %v; quiero 75 y 45", total, envio)
	}
	var monto float64
	conn.QueryRow(`SELECT monto FROM pagos WHERE id = $1`, in.PagoID).Scan(&monto)
	if monto != 75 {
		t.Errorf("el pago se registró por %v, quiero 75 (producto + envío)", monto)
	}

	// Un reintento que cambia a «recoger» (otra tarjeta, otro método de entrega)
	// reutiliza el pedido y debe quitar el envío.
	if err := RegistrarCobroFallido(conn, in, EstadoPagoRechazado, "R", "x"); err != nil {
		t.Fatal(err)
	}
	d.MetodoEntrega = "recoger"
	d.CostoEnvio = 0
	in2, err := IniciarPago(conn, in.Token, d, []LineaPedido{linea(p, 2)}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if in2.PedidoID != in.PedidoID {
		t.Fatal("debía reutilizar el pedido")
	}
	if total, envio := costoGuardado(t, conn, in.PedidoID); total != 30 || envio != 0 {
		t.Fatalf("tras cambiar a recoger: total %v, envío %v; quiero 30 y 0", total, envio)
	}
}

func TestElCostoDeEnvioQuedaGuardadoEnElPedidoDeTransferencia(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)

	d := datos()
	d.MetodoEntrega = "domicilio"
	d.DireccionEntrega = "Zona 1"
	d.CostoEnvio = 45
	tr, err := IniciarTransferencia(conn, d, []LineaPedido{linea(p, 1)}, 60)
	if err != nil {
		t.Fatal(err)
	}
	if total, envio := costoGuardado(t, conn, tr.PedidoID); total != 60 || envio != 45 {
		t.Fatalf("total %v, envío %v; quiero 60 y 45", total, envio)
	}
	ped, err := GetPedido(conn, tr.PedidoID)
	if err != nil || ped.CostoEnvio != 45 || ped.Total != 60 {
		t.Fatalf("GetPedido = %+v, %v", ped, err)
	}
	// Un pedido sin envío lo deja en cero.
	sin, _ := IniciarTransferencia(conn, datos(), []LineaPedido{linea(p, 1)}, 15)
	if _, envio := costoGuardado(t, conn, sin.PedidoID); envio != 0 {
		t.Errorf("envío = %v, quiero 0", envio)
	}
}
