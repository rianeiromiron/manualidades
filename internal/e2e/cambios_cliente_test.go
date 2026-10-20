package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

func (c *cliente) postForm(path string, datos url.Values) (*http.Response, string) {
	c.t.Helper()
	return c.post(path, datos)
}

func totalYEnvio(t *testing.T, e *entorno, id int) (total, envio float64, metodo, direccion string) {
	t.Helper()
	e.Conn.QueryRow(`SELECT total, costo_envio, metodo_entrega, direccion_entrega FROM pedidos WHERE id = $1`, id).Scan(&total, &envio, &metodo, &direccion)
	return
}

func TestElClienteCambiaLaEntregaDesdeSuPedido(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	c := e.cliente()
	token, id := e.creaPedido(t, c, "ana@ejemplo.com") // recoger, Q 25
	e.esperaCorreo(t)
	ruta := "/pedido/" + token

	_, body := c.get(ruta)
	for _, quiero := range []string{"¿Quieres cambiar algo?", "Guardar cambio de entrega", "Cancelar mi pedido", "no cambies la entrega", `name="monto"`} {
		if !strings.Contains(body, quiero) {
			t.Errorf("la página del pedido no muestra %q", quiero)
		}
	}

	// Recoger → domicilio: suma el envío, recalcula y avisa por correo.
	resp, _ := c.postForm(ruta+"/entrega", url.Values{"metodo_entrega": {"domicilio"}, "direccion_entrega": {"6a avenida 1-23, zona 1"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != ruta+"?r=entrega" {
		t.Fatalf("cambiar a domicilio: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if total, envio, metodo, dir := totalYEnvio(t, e.entorno, id); total != 70 || envio != 45 || metodo != "domicilio" || dir != "6a avenida 1-23, zona 1" {
		t.Fatalf("tras cambiar: total %v, envío %v, %s %q", total, envio, metodo, dir)
	}
	m := e.esperaCorreo(t)
	for _, quiero := range []string{"Q 70.00", "6a avenida 1-23", "incluye Q 45.00", "https://tienda.ejemplo.com/pedido/" + token} {
		if !strings.Contains(m.Texto, quiero) {
			t.Errorf("el correo del cambio no contiene %q:\n%s", quiero, m.Texto)
		}
	}
	if m.Para[0] != "ana@ejemplo.com" || !strings.Contains(m.Asunto, "nuevo monto") {
		t.Errorf("correo: %v %q", m.Para, m.Asunto)
	}
	_, body = c.get(ruta + "?r=entrega")
	for _, quiero := range []string{"Actualizamos la forma de entrega", "Envío a domicilio", "Q 70.00", `value="70.00"`} {
		if !strings.Contains(body, quiero) {
			t.Errorf("tras el cambio la página no muestra %q", quiero)
		}
	}

	// El costo vigente cambia, pero un pedido que ya es a domicilio conserva el suyo.
	fijaCostoEnvio(t, e.entorno, 80)
	c.postForm(ruta+"/entrega", url.Values{"metodo_entrega": {"domicilio"}, "direccion_entrega": {"Zona 10"}})
	if total, envio, _, dir := totalYEnvio(t, e.entorno, id); total != 70 || envio != 45 || dir != "Zona 10" {
		t.Fatalf("al cambiar solo la dirección: total %v, envío %v, %q; quiero conservar el envío de 45", total, envio, dir)
	}
	e.esperaCorreo(t)

	// Mismos datos: no pasa nada y no hay correo.
	resp, _ = c.postForm(ruta+"/entrega", url.Values{"metodo_entrega": {"domicilio"}, "direccion_entrega": {"Zona 10"}})
	if resp.Header.Get("Location") != ruta+"?r=sin-cambio" {
		t.Errorf("sin cambios: %q", resp.Header.Get("Location"))
	}
	e.sinCorreo(t, "no hubo cambios")

	// Domicilio → recoger: sin envío ni dirección.
	c.postForm(ruta+"/entrega", url.Values{"metodo_entrega": {"recoger"}, "direccion_entrega": {"se ignora"}})
	if total, envio, metodo, dir := totalYEnvio(t, e.entorno, id); total != 25 || envio != 0 || metodo != "recoger" || dir != "" {
		t.Fatalf("de vuelta a recoger: total %v, envío %v, %s %q", total, envio, metodo, dir)
	}
	e.esperaCorreo(t)
	// Volver a domicilio usa el costo de hoy.
	c.postForm(ruta+"/entrega", url.Values{"metodo_entrega": {"domicilio"}, "direccion_entrega": {"Zona 1"}})
	if total, envio, _, _ := totalYEnvio(t, e.entorno, id); total != 105 || envio != 80 {
		t.Fatalf("con el costo nuevo: total %v, envío %v; quiero 105 y 80", total, envio)
	}
	e.esperaCorreo(t)

	// Datos inválidos: se explica y no se guarda.
	for nombre, f := range map[string]url.Values{
		"sin dirección":       {"metodo_entrega": {"domicilio"}, "direccion_entrega": {"  "}},
		"método desconocido":  {"metodo_entrega": {"dron"}},
		"dirección con salto": {"metodo_entrega": {"domicilio"}, "direccion_entrega": {"Zona 1\nBcc: x@y.z"}},
	} {
		resp, body := c.postForm(ruta+"/entrega", f)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "alert-error") {
			t.Errorf("%s: %d, quiero 400 con un mensaje", nombre, resp.StatusCode)
		}
	}
	if total, _, _, dir := totalYEnvio(t, e.entorno, id); total != 105 || dir != "Zona 1" {
		t.Errorf("un cambio inválido modificó el pedido: %v %q", total, dir)
	}
	e.sinCorreo(t, "cambios inválidos")

	// Token desconocido.
	if resp, _ := c.postForm("/pedido/"+strings.Repeat("c", 36)+"/entrega", url.Values{"metodo_entrega": {"recoger"}}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("token desconocido: %d, quiero 404", resp.StatusCode)
	}

	// Con el comprobante subido ya no se puede cambiar: el monto debe coincidir.
	if resp, _ := c.subeComprobante(token, func() map[string]string { m := camposComprobante("BOL-12345"); m["monto"] = "105.00"; return m }(), imagenPNG(t)); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("subir comprobante: %d", resp.StatusCode)
	}
	e.esperaCorreo(t)
	resp, body = c.postForm(ruta+"/entrega", url.Values{"metodo_entrega": {"recoger"}})
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "ya no admite cambios") {
		t.Errorf("cambiar con comprobante en revisión: %d", resp.StatusCode)
	}
	if total, _, _, _ := totalYEnvio(t, e.entorno, id); total != 105 {
		t.Errorf("el total cambió a %v con el comprobante en revisión", total)
	}
	if _, body := c.get(ruta); strings.Contains(body, "Guardar cambio de entrega") || strings.Contains(body, "Cancelar mi pedido") {
		t.Error("en revisión ya no deben ofrecerse cambios ni cancelar")
	}
}

func TestElClienteCancelaSuPropioPedido(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	c := e.cliente()
	token, id := e.creaPedido(t, c, "ana@ejemplo.com")
	e.esperaCorreo(t)
	ruta := "/pedido/" + token

	if _, body := c.get("/"); !strings.Contains(body, `data-stock="9"`) {
		t.Fatal("el pedido debe estar reservando una unidad")
	}
	resp, _ := c.postForm(ruta+"/cancelar", url.Values{})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != ruta+"?r=cancelado" {
		t.Fatalf("cancelar: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := estadoDe(t, e.entorno, id); got != "cancelado" {
		t.Fatalf("estado = %q, quiero cancelado", got)
	}
	if _, body := c.get("/"); !strings.Contains(body, `data-stock="10"`) {
		t.Error("tras cancelar, la reserva se libera")
	}
	_, body := c.get(ruta + "?r=cancelado")
	if !strings.Contains(body, "Cancelaste tu pedido") || !strings.Contains(body, "fue cancelado") || strings.Contains(body, "Enviar comprobante") {
		t.Error("la página debe confirmar la cancelación y no ofrecer más acciones")
	}
	// Queda anotado que lo canceló el cliente, y el admin lo ve.
	var por string
	e.Conn.QueryRow(`SELECT cancelado_por FROM pedidos WHERE id = $1`, id).Scan(&por)
	if por != "cliente" {
		t.Errorf("cancelado_por = %q, quiero cliente", por)
	}
	admin := e.cliente()
	admin.entraComoAdmin()
	if _, body := admin.get(fmt.Sprintf("/admin/pedidos/%d", id)); !strings.Contains(body, "por cliente") {
		t.Error("el admin debe ver que lo canceló el cliente")
	}
	// No se repite, ni se puede subir un comprobante después.
	if resp, _ := c.postForm(ruta+"/cancelar", url.Values{}); resp.StatusCode != http.StatusConflict {
		t.Errorf("segunda cancelación: %d, quiero 409", resp.StatusCode)
	}
	if resp, _ := c.subeComprobante(token, camposComprobante("BOL-12345"), imagenPNG(t)); resp.StatusCode != http.StatusConflict {
		t.Errorf("subir tras cancelar: %d, quiero 409", resp.StatusCode)
	}
	if resp, _ := c.postForm("/pedido/"+strings.Repeat("d", 36)+"/cancelar", url.Values{}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("token desconocido: %d, quiero 404", resp.StatusCode)
	}

	// Con un comprobante en revisión no puede cancelar por su cuenta.
	token2, id2 := e.pedidoConComprobante(t, e.cliente(), "bea@ejemplo.com", "BOL-67890", false)
	resp, body = c.postForm("/pedido/"+token2+"/cancelar", url.Values{})
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "lo está revisando") {
		t.Errorf("cancelar en revisión: %d", resp.StatusCode)
	}
	if got := estadoDe(t, e.entorno, id2); got != "transferencia_por_revisar" {
		t.Errorf("estado = %q: el negocio debe decidir", got)
	}
}

func TestElMontoDelComprobanteSeComparaConElTotalAlRevisar(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	cli := e.cliente()
	admin := e.cliente()
	admin.entraComoAdmin()

	// Un pedido a domicilio (Q 70) donde el cliente transfirió solo Q 25.
	resp, out := cli.postJSON("/checkout/transferencia", pedidoDomicilio(e.producto, "ana@ejemplo.com"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("crear pedido: %d %v", resp.StatusCode, out)
	}
	token := strings.TrimPrefix(fmt.Sprint(out["redirect"]), "/pedido/")
	id := int(out["pedido_id"].(float64))
	e.esperaCorreo(t)

	// Sin monto, o con uno inválido, no se acepta.
	for nombre, monto := range map[string]string{"sin monto": "", "texto": "abc", "cero": "0", "negativo": "-5"} {
		campos := camposComprobante("BOL-12345")
		if monto == "" {
			delete(campos, "monto")
		} else {
			campos["monto"] = monto
		}
		resp, body := cli.subeComprobante(token, campos, imagenPNG(t))
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "monto que transferiste") {
			t.Errorf("%s: %d, quiero 400 pidiendo el monto", nombre, resp.StatusCode)
		}
	}
	if got := estadoDe(t, e.entorno, id); got != "espera_transferencia" {
		t.Fatalf("un monto inválido cambió el estado a %q", got)
	}

	// Monto distinto del total: se acepta, pero se avisa a todos.
	resp, _ = cli.subeComprobante(token, camposComprobante("BOL-12345"), imagenPNG(t)) // Q 25.00
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/pedido/"+token+"?enviado=diferencia" {
		t.Fatalf("subir con diferencia: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, body := cli.get("/pedido/" + token + "?enviado=diferencia")
	if !strings.Contains(body, "distinto al total del pedido") || !strings.Contains(body, "Q 25.00") {
		t.Error("al cliente se le avisa que el monto no coincide")
	}
	aviso := e.esperaCorreo(t)
	for _, quiero := range []string{"Monto transferido: Q 25.00", "Monto del pedido: Q 70.00", "OJO"} {
		if !strings.Contains(aviso.Texto, quiero) {
			t.Errorf("el aviso al negocio no contiene %q:\n%s", quiero, aviso.Texto)
		}
	}
	_, body = admin.get(fmt.Sprintf("/admin/pedidos/%d", id))
	for _, quiero := range []string{"Monto indicado", "Q 25.00", "≠ total Q 70.00", "El monto no coincide", "Q 70.00"} {
		if !strings.Contains(body, quiero) {
			t.Errorf("el detalle del admin no muestra %q", quiero)
		}
	}

	// Un monto que sí coincide no genera alarma (acepta la coma decimal).
	token2, id2 := e.creaPedido(t, e.cliente(), "bea@ejemplo.com") // recoger, Q 25
	e.esperaCorreo(t)
	campos := camposComprobante("BOL-67890")
	campos["monto"] = "25,00"
	resp, _ = e.cliente().subeComprobante(token2, campos, imagenPNG(t))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/pedido/"+token2+"?enviado=1" {
		t.Fatalf("subir con el monto correcto: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	ok := e.esperaCorreo(t)
	if strings.Contains(ok.Texto, "OJO") {
		t.Error("el aviso no debe alarmar si el monto coincide")
	}
	_, body = admin.get(fmt.Sprintf("/admin/pedidos/%d", id2))
	if strings.Contains(body, "El monto no coincide") || strings.Contains(body, "≠ total") {
		t.Error("el detalle no debe marcar diferencia si el monto coincide")
	}
}

// El formulario de la página del pedido, en un navegador real y con la CSP activa.
func TestNavegadorElClienteCambiaLaEntregaEnSuPedido(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	token, id := e.creaPedido(t, e.cliente(), "ana@ejemplo.com")
	e.esperaCorreo(t)
	n := nuevoNavegador(t)

	n.correr(t, chromedp.Navigate(e.URL+"/pedido/"+token), chromedp.WaitVisible(`#tFormEntrega`))
	n.espera(t, "recoger: la dirección está oculta",
		`document.getElementById('tDireccionRow').hidden && document.querySelector('input[name="metodo_entrega"]:checked').value === 'recoger'`)
	n.correr(t, chromedp.Click(`#tFormEntrega input[value="domicilio"]`))
	n.espera(t, "domicilio: aparece la dirección, obligatoria",
		`!document.getElementById('tDireccionRow').hidden && document.querySelector('#tDireccionRow input').required`)
	n.correr(t,
		chromedp.SendKeys(`#tDireccionRow input`, "6a avenida 1-23, zona 1"),
		chromedp.Click(`#tFormEntrega button[type="submit"]`),
	)
	n.espera(t, "la página muestra el monto nuevo",
		`location.search === '?r=entrega' && document.body.innerText.includes('Q 70.00') && document.body.innerText.includes('Actualizamos la forma de entrega')`)
	if total, envio, _, _ := totalYEnvio(t, e.entorno, id); total != 70 || envio != 45 {
		t.Errorf("total %v, envío %v; quiero 70 y 45", total, envio)
	}
	n.errores.exigeSinErrores(t, "cambio de entrega del pedido")
}
