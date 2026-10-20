package tienda

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var ahora = time.Date(2026, 10, 20, 10, 0, 0, 0, time.Local)

func datosCompr(boleta string) DatosComprobante {
	return DatosComprobante{NumeroBoleta: boleta, BancoOrigen: "Banco Industrial", Fecha: ahora, Monto: 10}
}

func nuevaTransferencia(t *testing.T, conn *sql.DB, producto int, email string) Transferencia {
	t.Helper()
	d := datos()
	d.ClienteEmail = email
	tr, err := IniciarTransferencia(conn, d, []LineaPedido{linea(producto, 1)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func filasComprobantes(t *testing.T, conn *sql.DB, pedido int) int {
	t.Helper()
	var n int
	conn.QueryRow(`SELECT count(*) FROM comprobantes WHERE pedido_id = $1`, pedido).Scan(&n)
	return n
}

func TestRegistrarComprobantePasaAPorRevisarYDejaDeVencer(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	tr := nuevaTransferencia(t, conn, p, "ana@ejemplo.com")

	id, err := RegistrarComprobante(conn, tr.PedidoID, "abc.jpg", datosCompr("  BOL-12345 "), ahora)
	if err != nil {
		t.Fatal(err)
	}
	if got := estadoPedido(t, conn, tr.PedidoID); got != EstadoTransferenciaPorRevisar {
		t.Fatalf("estado = %q, quiero transferencia_por_revisar", got)
	}
	var sinFecha bool
	conn.QueryRow(`SELECT expira_en IS NULL FROM pedidos WHERE id = $1`, tr.PedidoID).Scan(&sinFecha)
	if !sinFecha {
		t.Error("en revisión la reserva no debe vencer")
	}
	if got := reservado(t, conn, p); got != 1 {
		t.Errorf("reservado = %v, quiero 1 (la reserva continúa)", got)
	}
	lista, _ := ListComprobantes(conn, tr.PedidoID)
	if len(lista) != 1 || lista[0].ID != id || lista[0].NumeroBoleta != "BOL-12345" || lista[0].Estado != ComprobantePorRevisar ||
		lista[0].Archivo != "abc.jpg" || lista[0].Fecha.Format("2006-01-02") != "2026-10-20" {
		t.Fatalf("comprobante guardado = %+v", lista)
	}
	if n, err := LimpiarExpirados(conn); err != nil || n != 0 {
		t.Errorf("LimpiarExpirados = (%d, %v); un pedido en revisión no vence", n, err)
	}

	// Ya en revisión no admite otro comprobante.
	if _, err := RegistrarComprobante(conn, tr.PedidoID, "otro.jpg", datosCompr("BOL-99999"), ahora); !errors.Is(err, ErrPedidoNoAceptaComprobante) {
		t.Fatalf("segundo comprobante: err = %v, quiero ErrPedidoNoAceptaComprobante", err)
	}
	if filasComprobantes(t, conn, tr.PedidoID) != 1 {
		t.Error("se guardó un segundo comprobante")
	}
}

func TestComprobanteNoAplicaAPedidosVencidosNiDeTarjeta(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)

	vencido := nuevaTransferencia(t, conn, p, "a@ejemplo.com")
	conn.Exec(`UPDATE pedidos SET expira_en = now() - interval '1 minute' WHERE id = $1`, vencido.PedidoID)
	if _, err := RegistrarComprobante(conn, vencido.PedidoID, "x.jpg", datosCompr("BOL-11111"), ahora); !errors.Is(err, ErrPedidoNoAceptaComprobante) {
		t.Errorf("pedido vencido: err = %v", err)
	}
	if got := estadoPedido(t, conn, vencido.PedidoID); got != EstadoEsperaTransferencia {
		t.Errorf("un intento rechazado cambió el estado a %q", got)
	}

	tarjeta, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RegistrarComprobante(conn, tarjeta.PedidoID, "x.jpg", datosCompr("BOL-22222"), ahora); !errors.Is(err, ErrPedidoNoAceptaComprobante) {
		t.Errorf("pedido de tarjeta: err = %v", err)
	}
	if _, err := RegistrarComprobante(conn, 99999, "x.jpg", datosCompr("BOL-33333"), ahora); !errors.Is(err, ErrPedidoNoExiste) {
		t.Errorf("pedido inexistente: err = %v", err)
	}
}

func TestBoletaRepetidaSeRechazaSalvoQueSeHayaRechazado(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	a := nuevaTransferencia(t, conn, p, "a@ejemplo.com")
	b := nuevaTransferencia(t, conn, p, "b@ejemplo.com")

	if _, err := RegistrarComprobante(conn, a.PedidoID, "a.jpg", datosCompr("BOL-55555"), ahora); err != nil {
		t.Fatal(err)
	}
	// Misma boleta y mismo banco (aunque cambie mayúsculas): no.
	rep := datosCompr("bol-55555")
	rep.BancoOrigen = "BANCO INDUSTRIAL"
	if _, err := RegistrarComprobante(conn, b.PedidoID, "b.jpg", rep, ahora); !errors.Is(err, ErrBoletaRepetida) {
		t.Fatalf("err = %v, quiero ErrBoletaRepetida", err)
	}
	if got := estadoPedido(t, conn, b.PedidoID); got != EstadoEsperaTransferencia {
		t.Errorf("un comprobante repetido cambió el estado a %q", got)
	}
	if filasComprobantes(t, conn, b.PedidoID) != 0 {
		t.Error("quedó guardado un comprobante repetido")
	}
	// Otro banco con el mismo número es otra boleta.
	otro := datosCompr("BOL-55555")
	otro.BancoOrigen = "Banrural"
	if _, err := RegistrarComprobante(conn, b.PedidoID, "b.jpg", otro, ahora); err != nil {
		t.Fatalf("otro banco con el mismo número: %v", err)
	}

	// Un comprobante rechazado libera su boleta.
	conn.Exec(`UPDATE comprobantes SET estado = 'rechazado' WHERE pedido_id = $1`, a.PedidoID)
	c := nuevaTransferencia(t, conn, p, "c@ejemplo.com")
	if _, err := RegistrarComprobante(conn, c.PedidoID, "c.jpg", datosCompr("BOL-55555"), ahora); err != nil {
		t.Fatalf("boleta de un comprobante rechazado: %v", err)
	}
}

func TestMaximoDeComprobantesPorPedido(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	tr := nuevaTransferencia(t, conn, p, "a@ejemplo.com")
	for i := 0; i < MaxComprobantesPorPedido; i++ {
		conn.Exec(`INSERT INTO comprobantes (pedido_id, archivo, numero_boleta, banco_origen, fecha_transferencia, estado)
			VALUES ($1, 'x.jpg', $2, 'B', CURRENT_DATE, 'rechazado')`, tr.PedidoID, "R-"+string(rune('a'+i))+"-1234")
	}
	if _, err := RegistrarComprobante(conn, tr.PedidoID, "x.jpg", datosCompr("BOL-77777"), ahora); !errors.Is(err, ErrDemasiadosComprobantes) {
		t.Fatalf("err = %v, quiero ErrDemasiadosComprobantes", err)
	}
}

func TestDosSubidasSimultaneasSoloUnaGana(t *testing.T) {
	conn := nuevaBase(t)
	conn.SetMaxOpenConns(10)
	p := nuevoProducto(t, conn, "Taza", 5)
	tr := nuevaTransferencia(t, conn, p, "a@ejemplo.com")

	var ok, rechazadas, otros atomic.Int32
	var wg sync.WaitGroup
	arranque := make(chan struct{})
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-arranque
			_, err := RegistrarComprobante(conn, tr.PedidoID, "x.jpg", datosCompr("BOL-8000"+string(rune('0'+i))), ahora)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrPedidoNoAceptaComprobante):
				rechazadas.Add(1)
			default:
				otros.Add(1)
			}
		}(i)
	}
	close(arranque)
	wg.Wait()
	if ok.Load() != 1 || rechazadas.Load() != 5 || otros.Load() != 0 {
		t.Fatalf("ok %d, rechazadas %d, otros %d; quiero 1/5/0", ok.Load(), rechazadas.Load(), otros.Load())
	}
	if filasComprobantes(t, conn, tr.PedidoID) != 1 {
		t.Error("debe quedar exactamente un comprobante")
	}
}

func TestValidacionDeDatosDelComprobante(t *testing.T) {
	malos := map[string]func(*DatosComprobante){
		"boleta corta":          func(d *DatosComprobante) { d.NumeroBoleta = "12" },
		"boleta larga":          func(d *DatosComprobante) { d.NumeroBoleta = strings.Repeat("1", 41) },
		"boleta con símbolos":   func(d *DatosComprobante) { d.NumeroBoleta = "123;DROP" },
		"boleta con espacio":    func(d *DatosComprobante) { d.NumeroBoleta = "1234 5678" },
		"banco vacío":           func(d *DatosComprobante) { d.BancoOrigen = " " },
		"banco largo":           func(d *DatosComprobante) { d.BancoOrigen = strings.Repeat("a", 101) },
		"banco con salto":       func(d *DatosComprobante) { d.BancoOrigen = "Banco\nBcc: x@y.z" },
		"fecha vacía":           func(d *DatosComprobante) { d.Fecha = time.Time{} },
		"fecha futura":          func(d *DatosComprobante) { d.Fecha = ahora.AddDate(0, 0, 1) },
		"fecha de hace 31 días": func(d *DatosComprobante) { d.Fecha = ahora.AddDate(0, 0, -31) },
	}
	for nombre, rompe := range malos {
		d := datosCompr("BOL-12345")
		rompe(&d)
		if _, err := d.Limpiar(ahora); !errors.Is(err, ErrComprobanteInvalido) {
			t.Errorf("%s: err = %v, quiero ErrComprobanteInvalido", nombre, err)
		}
	}
	for nombre, fecha := range map[string]time.Time{"hoy": ahora, "hace 30 días": ahora.AddDate(0, 0, -30)} {
		d := datosCompr("BOL-12345")
		d.Fecha = fecha
		if _, err := d.Limpiar(ahora); err != nil {
			t.Errorf("%s debe ser válida: %v", nombre, err)
		}
	}
}

func TestPedidoPorTokenSoloAbrePedidosDeTransferencia(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	tr := nuevaTransferencia(t, conn, p, "ana@ejemplo.com")
	tarjeta, _ := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)

	ped, err := PedidoPorToken(conn, tr.Token)
	if err != nil || ped.ID != tr.PedidoID || ped.MetodoPago != MetodoPagoTransferencia || len(ped.Items) != 1 {
		t.Fatalf("pedido = %+v, err = %v", ped, err)
	}
	for nombre, token := range map[string]string{"de tarjeta": tarjeta.Token, "desconocido": "00000000-0000-0000-0000-000000000000", "vacío": ""} {
		if _, err := PedidoPorToken(conn, token); !errors.Is(err, ErrPedidoNoExiste) {
			t.Errorf("token %s: err = %v, quiero ErrPedidoNoExiste", nombre, err)
		}
	}
}

func TestTokenParaReenvio(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)
	tr := nuevaTransferencia(t, conn, p, "Ana@Ejemplo.com")
	d := datos()
	d.ClienteEmail = "ana@ejemplo.com"
	tarjeta, _ := IniciarPago(conn, "", d, []LineaPedido{linea(p, 1)}, 10)

	token, nombre, ok, err := TokenParaReenvio(conn, tr.PedidoID, " ana@EJEMPLO.com ")
	if err != nil || !ok || token != tr.Token || nombre == "" {
		t.Fatalf("coincidencia sin distinguir mayúsculas: %q %q %v %v", token, nombre, ok, err)
	}
	casos := map[string]struct {
		id    int
		email string
	}{
		"otro correo":        {tr.PedidoID, "otra@ejemplo.com"},
		"correo vacío":       {tr.PedidoID, ""},
		"pedido inexistente": {99999, "ana@ejemplo.com"},
		"pedido de tarjeta":  {tarjeta.PedidoID, "ana@ejemplo.com"},
	}
	for nombre, c := range casos {
		if tk, _, ok, err := TokenParaReenvio(conn, c.id, c.email); ok || tk != "" || err != nil {
			t.Errorf("%s: (%q, %v, %v), quiero no coincidencia", nombre, tk, ok, err)
		}
	}
}

func TestTransferenciasAbiertasPorCorreo(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 10)
	a := nuevaTransferencia(t, conn, p, "ana@ejemplo.com")
	nuevaTransferencia(t, conn, p, "ANA@ejemplo.com")
	nuevaTransferencia(t, conn, p, "otra@ejemplo.com")
	if n, _ := TransferenciasAbiertas(conn, "ana@ejemplo.com"); n != 2 {
		t.Fatalf("abiertas = %d, quiero 2", n)
	}
	// Una vencida ya no cuenta.
	conn.Exec(`UPDATE pedidos SET estado = 'expirado', expira_en = NULL WHERE id = $1`, a.PedidoID)
	if n, _ := TransferenciasAbiertas(conn, "ana@ejemplo.com"); n != 1 {
		t.Fatalf("abiertas tras vencer una = %d, quiero 1", n)
	}
}
