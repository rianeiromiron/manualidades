package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"manualidades/internal/sitio"
)

func pedidoDomicilio(producto int, email string) map[string]any {
	p := pedidoJSON(producto, email, 1)
	p["metodo_entrega"] = "domicilio"
	p["direccion_entrega"] = "6a avenida 1-23, zona 1"
	return p
}

func totales(t *testing.T, e *entorno, id int) (total, envio float64) {
	t.Helper()
	if err := e.Conn.QueryRow(`SELECT total, costo_envio FROM pedidos WHERE id = $1`, id).Scan(&total, &envio); err != nil {
		t.Fatal(err)
	}
	return total, envio
}

func fijaCostoEnvio(t *testing.T, e *entorno, costo float64) {
	t.Helper()
	cfg, err := sitio.Get(e.Conn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.CostoEnvio = costo
	if err := sitio.Save(e.Conn, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestEnvioATransferenciaSeSumaSoloADomicilioYSeCongela(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	c := e.cliente()

	// A domicilio: producto (Q 25) + envío (Q 45 por omisión).
	resp, out := c.postJSON("/checkout/transferencia", pedidoDomicilio(e.producto, "ana@ejemplo.com"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("crear: %d %v", resp.StatusCode, out)
	}
	idDom := int(out["pedido_id"].(float64))
	if total, envio := totales(t, e.entorno, idDom); total != 70 || envio != 45 {
		t.Fatalf("a domicilio: total %v, envío %v; quiero 70 y 45", total, envio)
	}
	m := e.esperaCorreo(t)
	if !strings.Contains(m.Texto, "Q 70.00") || !strings.Contains(m.Texto, "incluye Q 45.00 de envío") {
		t.Errorf("el correo no explica el envío:\n%s", m.Texto)
	}
	token := strings.TrimPrefix(fmt.Sprint(out["redirect"]), "/pedido/")
	_, body := c.get("/pedido/" + token)
	if !strings.Contains(body, "Envío a domicilio") || !strings.Contains(body, "Q 45.00") || !strings.Contains(body, "Q 70.00") {
		t.Error("la página del pedido no muestra el envío y el total")
	}

	// Recoger en tienda: sin envío.
	resp, out = c.postJSON("/checkout/transferencia", pedidoJSON(e.producto, "bea@ejemplo.com", 1))
	idRec := int(out["pedido_id"].(float64))
	if total, envio := totales(t, e.entorno, idRec); total != 25 || envio != 0 {
		t.Fatalf("recoger: total %v, envío %v; quiero 25 y 0", total, envio)
	}
	e.esperaCorreo(t)
	_, body = c.get(fmt.Sprint(out["redirect"]))
	if strings.Contains(body, "Envío a domicilio") {
		t.Error("un pedido para recoger no debe mostrar envío")
	}

	// El navegador no decide el monto: un valor enviado a la fuerza se ignora.
	forzado := pedidoDomicilio(e.producto, "cris@ejemplo.com")
	forzado["costo_envio"] = 0
	forzado["total"] = 1
	resp, out = c.postJSON("/checkout/transferencia", forzado)
	idForzado := int(out["pedido_id"].(float64))
	if total, envio := totales(t, e.entorno, idForzado); total != 70 || envio != 45 {
		t.Fatalf("con monto forzado: total %v, envío %v; quiero 70 y 45", total, envio)
	}
	e.esperaCorreo(t)

	// Cambiar el costo afecta a los pedidos nuevos, no a los ya hechos.
	fijaCostoEnvio(t, e.entorno, 60)
	_, out = c.postJSON("/checkout/transferencia", pedidoDomicilio(e.producto, "dana@ejemplo.com"))
	idNuevo := int(out["pedido_id"].(float64))
	if total, envio := totales(t, e.entorno, idNuevo); total != 85 || envio != 60 {
		t.Fatalf("tras subir el costo: total %v, envío %v; quiero 85 y 60", total, envio)
	}
	if total, envio := totales(t, e.entorno, idDom); total != 70 || envio != 45 {
		t.Fatalf("el pedido anterior cambió: total %v, envío %v; quiero 70 y 45", total, envio)
	}

	// Envío gratis (costo 0): el pedido a domicilio no suma nada.
	fijaCostoEnvio(t, e.entorno, 0)
	_, out = c.postJSON("/checkout/transferencia", pedidoDomicilio(e.producto, "eli@ejemplo.com"))
	idGratis := int(out["pedido_id"].(float64))
	if total, envio := totales(t, e.entorno, idGratis); total != 25 || envio != 0 {
		t.Fatalf("envío gratis: total %v, envío %v; quiero 25 y 0", total, envio)
	}
}

// El cobro con tarjeta debe llevar el envío: se comprueba el monto del pedido y
// del pago creados antes de llamar a la pasarela.
func TestEnvioALaTarjetaSeSumaAlMontoACobrar(t *testing.T) {
	t.Setenv("PASARELA_URL", "http://127.0.0.1:1") // nadie escucha: nunca se cobra de verdad
	e := nuevoEntornoTransferencia(t, false)
	c := e.cliente()

	tarjeta := func(entrega string) map[string]any {
		p := pedidoJSON(e.producto, "", 1)
		p["metodo_entrega"] = entrega
		if entrega == "domicilio" {
			p["direccion_entrega"] = "Zona 1"
		}
		p["tarjeta_numero"], p["tarjeta_titular"], p["tarjeta_expiracion"], p["tarjeta_cvv"] = "4111111111111111", "Ana Pérez", "12/30", "123"
		return p
	}
	if resp, out := c.postJSON("/checkout/confirmar", tarjeta("domicilio")); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("sin pasarela: %d %v, quiero 502", resp.StatusCode, out)
	}
	var total, envio, monto float64
	if err := e.Conn.QueryRow(
		`SELECT p.total, p.costo_envio, g.monto FROM pedidos p JOIN pagos g ON g.pedido_id = p.id`,
	).Scan(&total, &envio, &monto); err != nil {
		t.Fatal(err)
	}
	if total != 70 || envio != 45 || monto != 70 {
		t.Fatalf("tarjeta a domicilio: total %v, envío %v, monto a cobrar %v; quiero 70, 45 y 70", total, envio, monto)
	}

	// Un reintento que pasa a «recoger» (misma cookie) quita el envío del monto.
	if resp, _ := c.postJSON("/checkout/confirmar", tarjeta("recoger")); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("reintento: %d", resp.StatusCode)
	}
	var n int
	e.Conn.QueryRow(`SELECT count(*) FROM pedidos`).Scan(&n)
	e.Conn.QueryRow(`SELECT p.total, p.costo_envio FROM pedidos p`).Scan(&total, &envio)
	if n != 1 || total != 25 || envio != 0 {
		t.Fatalf("tras el reintento: %d pedidos, total %v, envío %v; quiero 1, 25 y 0", n, total, envio)
	}
}

func TestEnvioElAdminLoConfigura(t *testing.T) {
	e := nuevoEntornoTransferencia(t, false)
	c := e.cliente()
	c.entraComoAdmin()

	_, body := c.get("/admin/sitio")
	if !strings.Contains(body, `name="costo_envio" value="45.00"`) {
		t.Fatal("la pantalla del sitio no muestra el costo de envío por omisión (45.00)")
	}
	csrf := c.csrf()
	form := func(costo string) url.Values {
		return url.Values{
			"csrf": {csrf}, "nombre_negocio": {"Manualidades"}, "color_fondo": {"#FBE9EC"}, "color_texto": {"#1F2A44"},
			"color_marco": {"#8A8F98"}, "color_acento": {"#C1592B"}, "costo_envio": {costo},
		}
	}
	costoActual := func() float64 { cfg, _ := sitio.Get(e.Conn); return cfg.CostoEnvio }

	_, body = c.postMultipart("/admin/sitio", form("60,50")) // admite la coma decimal
	if !strings.Contains(body, "Configuración guardada") || costoActual() != 60.5 {
		t.Fatalf("guardar 60,50: costo = %v", costoActual())
	}
	for nombre, malo := range map[string]string{"texto": "abc", "negativo": "-5", "enorme": "100000"} {
		_, body = c.postMultipart("/admin/sitio", form(malo))
		if !strings.Contains(body, "alert-error") || costoActual() != 60.5 {
			t.Errorf("%s: debe rechazarse y conservar 60.50 (costo = %v)", nombre, costoActual())
		}
	}
	// Dejarlo en blanco conserva el actual.
	c.postMultipart("/admin/sitio", form(""))
	if costoActual() != 60.5 {
		t.Errorf("en blanco cambió el costo a %v", costoActual())
	}

	// La tienda lo refleja en el checkout.
	_, body = e.cliente().get("/checkout")
	if !strings.Contains(body, `data-costo="60.50"`) {
		t.Error("el checkout no usa el costo configurado")
	}
}
