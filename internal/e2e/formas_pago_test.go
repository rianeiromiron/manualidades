package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"

	"manualidades/internal/usuarios"
)

func pedidoConTarjeta(producto int) map[string]any {
	p := pedidoJSON(producto, "", 1)
	p["tarjeta_numero"], p["tarjeta_titular"], p["tarjeta_expiracion"], p["tarjeta_cvv"] = "4111111111111111", "Ana Pérez", "12/30", "123"
	return p
}

func totalPedidos(t *testing.T, e *entorno) int {
	t.Helper()
	var n int
	e.Conn.QueryRow(`SELECT count(*) FROM pedidos`).Scan(&n)
	return n
}

// cambiaForma activa o desactiva una forma de pago desde el admin.
func cambiaForma(t *testing.T, admin *cliente, csrf, clave string, activar bool) {
	t.Helper()
	accion := "desactivar"
	if activar {
		accion = "activar"
	}
	resp, _ := admin.post(fmt.Sprintf("/admin/cuentas/formas-pago/%s/%s", clave, accion), url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/cuentas" {
		t.Fatalf("%s %s: %d → %q", accion, clave, resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestFormasDePagoPorOmisionEstanActivas(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	_, body := e.cliente().get("/checkout")
	if !strings.Contains(body, `value="tarjeta"`) || !strings.Contains(body, `value="transferencia"`) {
		t.Error("por omisión el checkout ofrece tarjeta y transferencia")
	}
	if strings.Contains(body, `t-forma-pago" hidden`) {
		t.Error("con dos formas de pago el selector debe verse")
	}
	admin := e.cliente()
	admin.entraComoAdmin()
	_, body = admin.get("/admin/cuentas")
	for _, quiero := range []string{"Formas de pago", "Tarjeta de crédito", "Transferencia bancaria", "Activa"} {
		if !strings.Contains(body, quiero) {
			t.Errorf("el panel no muestra %q", quiero)
		}
	}
}

func TestSinTarjetaSoloSeOfreceLaTransferencia(t *testing.T) {
	t.Setenv("PASARELA_URL", "http://127.0.0.1:1")
	e := nuevoEntornoTransferencia(t, true)
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	cambiaForma(t, admin, csrf, "tarjeta", false)

	c := e.cliente()
	_, body := c.get("/checkout")
	if strings.Contains(body, `value="tarjeta"`) || !strings.Contains(body, `value="transferencia" checked`) {
		t.Error("sin tarjeta, la transferencia debe ser la única opción y venir marcada")
	}
	if !strings.Contains(body, `t-forma-pago" hidden`) {
		t.Error("con una sola forma de pago el selector queda oculto")
	}
	if strings.Contains(body, "Por ahora no estamos recibiendo pagos") {
		t.Error("todavía hay una forma de pago disponible")
	}

	// El servidor lo exige aunque alguien arme la petición a mano.
	resp, out := c.postJSON("/checkout/confirmar", pedidoConTarjeta(e.producto))
	if resp.StatusCode != http.StatusConflict || !strings.Contains(fmt.Sprint(out["error"]), "tarjeta no está disponible") {
		t.Errorf("cobro con tarjeta desactivada: %d %v", resp.StatusCode, out)
	}
	if n := totalPedidos(t, e.entorno); n != 0 {
		t.Errorf("la tarjeta desactivada dejó %d pedidos creados", n)
	}
	// La transferencia sigue funcionando.
	e.creaPedido(t, c, "ana@ejemplo.com")
	if n := totalPedidos(t, e.entorno); n != 1 {
		t.Errorf("hay %d pedidos, quiero 1", n)
	}

	// Volver a activarla la devuelve al checkout.
	cambiaForma(t, admin, csrf, "tarjeta", true)
	if _, body := c.get("/checkout"); !strings.Contains(body, `value="tarjeta"`) || strings.Contains(body, `t-forma-pago" hidden`) {
		t.Error("al reactivar la tarjeta vuelve el selector con las dos opciones")
	}
}

func TestSinTransferenciaSoloSeOfreceLaTarjeta(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	admin := e.cliente()
	admin.entraComoAdmin()
	cambiaForma(t, admin, admin.csrf(), "transferencia", false)

	c := e.cliente()
	_, body := c.get("/checkout")
	if strings.Contains(body, `value="transferencia"`) || strings.Contains(body, "Transferencia bancaria") || !strings.Contains(body, `value="tarjeta" checked`) {
		t.Error("sin transferencia, la tarjeta es la única opción")
	}
	resp, out := c.postJSON("/checkout/transferencia", pedidoJSON(e.producto, "ana@ejemplo.com", 1))
	if resp.StatusCode != http.StatusConflict || !strings.Contains(fmt.Sprint(out["error"]), "no está disponible") ||
		!strings.Contains(fmt.Sprint(out["error"]), "Puedes pagar con tarjeta") {
		t.Errorf("transferencia desactivada: %d %v", resp.StatusCode, out)
	}
	if n := totalPedidos(t, e.entorno); n != 0 {
		t.Errorf("la transferencia desactivada dejó %d pedidos", n)
	}
}

func TestSinNingunaFormaDePagoElCheckoutAvisa(t *testing.T) {
	t.Setenv("PASARELA_URL", "http://127.0.0.1:1")
	e := nuevoEntornoTransferencia(t, true)
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	cambiaForma(t, admin, csrf, "tarjeta", false)
	cambiaForma(t, admin, csrf, "transferencia", false)

	c := e.cliente()
	resp, body := c.get("/checkout")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Por ahora no estamos recibiendo pagos en línea") ||
		!strings.Contains(body, "Volver al catálogo") {
		t.Fatal("sin formas de pago el checkout debe avisarlo")
	}
	if strings.Contains(body, "tFormCheckout") || strings.Contains(body, "tienda-checkout.js") {
		t.Error("no debe mostrarse el formulario de pago")
	}
	if resp, out := c.postJSON("/checkout/confirmar", pedidoConTarjeta(e.producto)); resp.StatusCode != http.StatusConflict {
		t.Errorf("tarjeta: %d %v", resp.StatusCode, out)
	}
	resp, out := c.postJSON("/checkout/transferencia", pedidoJSON(e.producto, "ana@ejemplo.com", 1))
	if resp.StatusCode != http.StatusConflict || strings.Contains(fmt.Sprint(out["error"]), "Puedes pagar con tarjeta") {
		t.Errorf("transferencia: %d %v (no debe sugerir una tarjeta que tampoco está)", resp.StatusCode, out)
	}
	if n := totalPedidos(t, e.entorno); n != 0 {
		t.Errorf("se crearon %d pedidos sin formas de pago", n)
	}
}

// Desactivar una forma de pago solo frena los pedidos nuevos: quien ya pidió por
// transferencia puede terminar su pago, y el negocio puede revisarlo.
func TestDesactivarLaTransferenciaNoDejaEnElAireLosPedidosYaCreados(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	cli := e.cliente()
	token, id := e.creaPedido(t, cli, "ana@ejemplo.com")
	e.esperaCorreo(t)

	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	cambiaForma(t, admin, csrf, "transferencia", false)

	if resp, body := cli.get("/pedido/" + token); resp.StatusCode != http.StatusOK || !strings.Contains(body, "Enviar comprobante") {
		t.Fatal("el pedido ya creado debe seguir mostrando su formulario")
	}
	if resp, _ := cli.subeComprobante(token, camposComprobante("BOL-12345"), imagenPNG(t)); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("subir el comprobante: %d", resp.StatusCode)
	}
	e.esperaCorreo(t)
	var comp int
	e.Conn.QueryRow(`SELECT id FROM comprobantes WHERE pedido_id = $1`, id).Scan(&comp)
	resp, _ := admin.post(fmt.Sprintf("/admin/pedidos/%d/comprobantes/%d/aprobar", id, comp), url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther || estadoDe(t, e.entorno, id) != "pagado" {
		t.Fatalf("aprobar con la transferencia desactivada: %d, estado %q", resp.StatusCode, estadoDe(t, e.entorno, id))
	}
}

func TestCambiarFormasDePagoExigePermisoCSRFYUnaClaveValida(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()

	if _, err := usuarios.CreateUsuario(e.Conn, "silvia", "clave-de-silvia-1", usuarios.RolAdministrativo, nil); err != nil {
		t.Fatal(err)
	}
	sin := e.cliente()
	sin.login("silvia", "clave-de-silvia-1")
	desactivar := "/admin/cuentas/formas-pago/tarjeta/desactivar"
	if resp, _ := sin.post(desactivar, url.Values{"csrf": {sin.csrf()}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("sin módulo: %d, quiero 403", resp.StatusCode)
	}
	if resp, _ := e.cliente().post(desactivar, url.Values{}); resp.StatusCode != http.StatusSeeOther ||
		!strings.HasPrefix(resp.Header.Get("Location"), "/admin/login") {
		t.Errorf("sin sesión debe llevar al login: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := admin.post(desactivar, url.Values{}); resp.StatusCode == http.StatusSeeOther {
		t.Error("un cambio sin CSRF fue aceptado")
	}
	if resp, _ := admin.get(desactivar); resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
		t.Errorf("GET sobre desactivar respondió %d", resp.StatusCode)
	}
	for _, clave := range []string{"efectivo", "TARJETA", "tarjeta%27%20OR%20%271%27=%271"} {
		if resp, _ := admin.post("/admin/cuentas/formas-pago/"+clave+"/desactivar", url.Values{"csrf": {csrf}}); resp.StatusCode != http.StatusNotFound {
			t.Errorf("clave %q: %d, quiero 404", clave, resp.StatusCode)
		}
	}
	var activa bool
	e.Conn.QueryRow(`SELECT activa FROM formas_pago WHERE clave = 'tarjeta'`).Scan(&activa)
	if !activa {
		t.Fatal("alguno de los intentos inválidos desactivó la tarjeta")
	}

	// Un cambio válido queda anotado: quién y cuándo.
	cambiaForma(t, admin, csrf, "tarjeta", false)
	_, body := admin.get("/admin/cuentas")
	if !strings.Contains(body, "Desactivada") || !strings.Contains(body, "admin · ") {
		t.Error("el panel debe mostrar que está desactivada y quién lo hizo")
	}
}

func TestElPanelAvisaSiLaTransferenciaNoTieneCuentas(t *testing.T) {
	e := nuevoEntornoTransferencia(t, false)
	admin := e.cliente()
	admin.entraComoAdmin()
	_, body := admin.get("/admin/cuentas")
	if !strings.Contains(body, "no hay ninguna cuenta bancaria activa") {
		t.Error("el panel debe avisar que la transferencia no se ofrecerá sin cuentas activas")
	}
}

// Con la tarjeta desactivada, un cliente real puede completar un pedido por
// transferencia sin ver ningún selector ni campo de tarjeta.
func TestNavegadorCheckoutSoloTransferencia(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	if _, err := e.Conn.Exec(`UPDATE formas_pago SET activa = false WHERE clave = 'tarjeta'`); err != nil {
		t.Fatal(err)
	}
	n := nuevoNavegador(t)

	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitVisible(`.t-agregar`), chromedp.Click(`.t-agregar`))
	n.espera(t, "el producto queda en el carrito", `(JSON.parse(localStorage.getItem('manualidades_carrito'))||[]).length === 1`)
	n.correr(t, chromedp.Navigate(e.URL+"/checkout"), chromedp.WaitVisible(`#tFormCheckout`))

	n.espera(t, "el selector está oculto y la transferencia queda elegida",
		`getComputedStyle(document.querySelector('.t-forma-pago')).display === 'none'
		 && document.querySelector('input[name="metodo_pago"]:checked').value === 'transferencia'
		 && document.getElementById('tTarjetaCampos').hidden
		 && document.getElementById('tPagarBtn').textContent === 'Continuar con la transferencia'
		 && document.querySelector('input[name="cliente_email"]').required`)

	n.correr(t,
		chromedp.SendKeys(`input[name="cliente_nombre"]`, "Ana Pérez"),
		chromedp.SendKeys(`input[name="cliente_telefono"]`, "5555-1234"),
		chromedp.SendKeys(`input[name="cliente_email"]`, "ana@ejemplo.com"),
		chromedp.Click(`#tPagarBtn`),
	)
	n.espera(t, "se llega a la página privada del pedido",
		`/^\/pedido\/[0-9a-f-]{36}$/.test(location.pathname) && document.body.innerText.includes('Haz la transferencia')`)
	n.errores.exigeSinErrores(t, "checkout solo con transferencia")
}
