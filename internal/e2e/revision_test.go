package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"manualidades/internal/usuarios"
)

// pedidoConComprobante crea un pedido por transferencia, sube su comprobante y
// vacía los correos que eso genera. Devuelve el token y el id del pedido.
func (e *entornoTransferencia) pedidoConComprobante(t *testing.T, c *cliente, email, boleta string, domicilio bool) (token string, id int) {
	t.Helper()
	body := pedidoJSON(e.producto, email, 1)
	if domicilio {
		body = pedidoDomicilio(e.producto, email)
	}
	resp, out := c.postJSON("/checkout/transferencia", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("crear pedido: %d %v", resp.StatusCode, out)
	}
	token = strings.TrimPrefix(fmt.Sprint(out["redirect"]), "/pedido/")
	id = int(out["pedido_id"].(float64))
	e.esperaCorreo(t)
	if resp, _ := c.subeComprobante(token, camposComprobante(boleta), imagenPNG(t)); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("subir comprobante: %d", resp.StatusCode)
	}
	e.esperaCorreo(t) // aviso al negocio
	return token, id
}

func stockDe(t *testing.T, e *entorno, producto int) float64 {
	t.Helper()
	var s float64
	e.Conn.QueryRow(`SELECT COALESCE(SUM(CASE WHEN tipo = 'ingreso' THEN cantidad ELSE -cantidad END), 0)
		FROM movimientos_inventario WHERE producto_id = $1`, producto).Scan(&s)
	return s
}

func TestRevisionAprobarTransferenciaDeExtremoAExtremo(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	cli := e.cliente()
	token, id := e.pedidoConComprobante(t, cli, "ana@ejemplo.com", "BOL-12345", true)

	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()

	// El listado avisa de lo pendiente.
	_, body := admin.get("/admin/pedidos")
	if !strings.Contains(body, "1 transferencia por revisar") || !strings.Contains(body, "Transferencia por revisar") {
		t.Error("el listado no avisa que hay una transferencia por revisar")
	}

	// El detalle muestra todo lo necesario para decidir.
	ruta := fmt.Sprintf("/admin/pedidos/%d", id)
	_, body = admin.get(ruta)
	for _, quiero := range []string{"BOL-12345", "Banco Industrial", "Q 70.00", "Aprobar transferencia", "Rechazar comprobante", "Envío a domicilio", "La imagen sola no prueba el pago"} {
		if !strings.Contains(body, quiero) {
			t.Errorf("el detalle no muestra %q", quiero)
		}
	}
	var compID int
	e.Conn.QueryRow(`SELECT id FROM comprobantes WHERE pedido_id = $1`, id).Scan(&compID)
	archivo := fmt.Sprintf("%s/comprobantes/%d/archivo", ruta, compID)

	// La imagen: solo para el admin, con cabeceras seguras.
	resp, img := admin.get(archivo)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(img, "\x89PNG") {
		t.Fatalf("imagen: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("faltan cabeceras de seguridad en la imagen: %v", resp.Header)
	}
	if resp, _ := e.cliente().get(archivo); resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/admin/login") {
		t.Errorf("sin sesión la imagen respondió %d", resp.StatusCode)
	}
	if resp, _ := cli.get(archivo); resp.StatusCode == http.StatusOK {
		t.Error("el cliente pudo ver la imagen por la ruta del admin")
	}
	for nombre, r := range map[string]string{
		"pedido que no corresponde":      fmt.Sprintf("/admin/pedidos/%d/comprobantes/%d/archivo", id+1000, compID),
		"comprobante que no corresponde": fmt.Sprintf("%s/comprobantes/%d/archivo", ruta, compID+1000),
		"id no numérico":                 ruta + "/comprobantes/abc/archivo",
	} {
		if resp, _ := admin.get(r); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, quiero 404", nombre, resp.StatusCode)
		}
	}

	// Un usuario sin el módulo de pedidos no ve la imagen ni puede aprobar.
	if _, err := usuarios.CreateUsuario(e.Conn, "silvia", "clave-de-silvia-1", usuarios.RolAdministrativo, nil); err != nil {
		t.Fatal(err)
	}
	sin := e.cliente()
	sin.login("silvia", "clave-de-silvia-1")
	if resp, _ := sin.get(archivo); resp.StatusCode != http.StatusForbidden {
		t.Errorf("imagen sin módulo: %d, quiero 403", resp.StatusCode)
	}
	aprobar := fmt.Sprintf("%s/comprobantes/%d/aprobar", ruta, compID)
	if resp, _ := sin.post(aprobar, url.Values{"csrf": {sin.csrf()}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("aprobar sin módulo: %d, quiero 403", resp.StatusCode)
	}
	// Sin token CSRF no se aprueba ni con permiso.
	if resp, _ := admin.post(aprobar, url.Values{}); resp.StatusCode == http.StatusSeeOther {
		t.Error("una aprobación sin CSRF fue aceptada")
	}
	// Aprobar no se hace con GET.
	if resp, _ := admin.get(aprobar); resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
		t.Errorf("GET sobre aprobar respondió %d", resp.StatusCode)
	}
	if got := estadoDe(t, e.entorno, id); got != "transferencia_por_revisar" {
		t.Fatalf("antes de aprobar el estado cambió a %q", got)
	}

	// Aprobar.
	resp, _ = admin.post(aprobar, url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != ruta+"?r=aprobado" {
		t.Fatalf("aprobar: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := estadoDe(t, e.entorno, id); got != "pagado" {
		t.Fatalf("estado = %q, quiero pagado", got)
	}
	if got := stockDe(t, e.entorno, e.producto); got != 9 {
		t.Errorf("stock = %v, quiero 9 (se vendió 1 de 10)", got)
	}
	_, body = admin.get(ruta + "?r=aprobado")
	for _, quiero := range []string{"Transferencia aprobada", "Aceptado", "Transferencia", "Cambiar estado", "admin"} {
		if !strings.Contains(body, quiero) {
			t.Errorf("tras aprobar el detalle no muestra %q", quiero)
		}
	}
	if strings.Contains(body, "Aprobar transferencia") {
		t.Error("tras aprobar ya no deben ofrecerse los botones de revisión")
	}

	// Correo al cliente con el enlace a su pedido.
	m := e.esperaCorreo(t)
	if m.Para[0] != "ana@ejemplo.com" || !strings.Contains(m.Asunto, "Confirmamos tu pago") ||
		!strings.Contains(m.Texto, "6a avenida 1-23") || !strings.Contains(m.Texto, "https://tienda.ejemplo.com/pedido/"+token) {
		t.Errorf("correo de aprobación incorrecto: %v %q\n%s", m.Para, m.Asunto, m.Texto)
	}
	// El cliente ve su pedido pagado.
	if _, body := cli.get("/pedido/" + token); !strings.Contains(body, "Pago confirmado") {
		t.Error("la página del cliente no muestra el pago confirmado")
	}

	// Una segunda aprobación no repite nada.
	resp, body = admin.post(aprobar, url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "ya fue revisado") {
		t.Errorf("segunda aprobación: %d", resp.StatusCode)
	}
	if got := stockDe(t, e.entorno, e.producto); got != 9 {
		t.Errorf("la segunda aprobación cambió el stock a %v", got)
	}
	e.sinCorreo(t, "una aprobación repetida no avisa")
}

func TestRevisionRechazarYVolverASubir(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	cli := e.cliente()
	token, id := e.pedidoConComprobante(t, cli, "ana@ejemplo.com", "BOL-12345", false)

	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	ruta := fmt.Sprintf("/admin/pedidos/%d", id)
	var compID int
	e.Conn.QueryRow(`SELECT id FROM comprobantes WHERE pedido_id = $1`, id).Scan(&compID)
	rechazar := fmt.Sprintf("%s/comprobantes/%d/rechazar", ruta, compID)

	// Sin motivo no se rechaza.
	resp, body := admin.post(rechazar, url.Values{"csrf": {csrf}, "motivo": {"  "}, "accion": {"reintentar"}})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Escribe el motivo del rechazo") {
		t.Errorf("sin motivo: %d", resp.StatusCode)
	}
	if got := estadoDe(t, e.entorno, id); got != "transferencia_por_revisar" {
		t.Fatalf("un rechazo inválido cambió el estado a %q", got)
	}
	e.sinCorreo(t, "rechazo inválido")

	// Rechazo que da otra oportunidad. El motivo con HTML se muestra como texto.
	motivo := `No vemos el depósito <script>alert(1)</script>`
	resp, _ = admin.post(rechazar, url.Values{"csrf": {csrf}, "motivo": {motivo}, "accion": {"reintentar"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != ruta+"?r=rechazado" {
		t.Fatalf("rechazar: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := estadoDe(t, e.entorno, id); got != "espera_transferencia" {
		t.Fatalf("estado = %q, quiero espera_transferencia", got)
	}
	if got := stockDe(t, e.entorno, e.producto); got != 10 {
		t.Errorf("rechazar no debe tocar el inventario (stock %v)", got)
	}
	m := e.esperaCorreo(t)
	if !strings.Contains(m.Texto, "No vemos el depósito") || !strings.Contains(m.Texto, "https://tienda.ejemplo.com/pedido/"+token) ||
		!strings.Contains(m.Asunto, "No pudimos confirmar") {
		t.Errorf("correo de rechazo incorrecto:\n%s", m.Texto)
	}
	for nombre, c := range map[string]*cliente{"admin": admin, "cliente": cli} {
		ruta := ruta
		if nombre == "cliente" {
			ruta = "/pedido/" + token
		}
		_, pagina := c.get(ruta)
		if strings.Contains(pagina, "<script>alert(1)") {
			t.Errorf("%s: el motivo se pintó sin escapar", nombre)
		}
		if !strings.Contains(pagina, "No vemos el depósito") {
			t.Errorf("%s: no muestra el motivo del rechazo", nombre)
		}
	}
	// El cliente puede subir otro comprobante.
	_, pagina := cli.get("/pedido/" + token)
	if !strings.Contains(pagina, "Enviar comprobante") {
		t.Fatal("tras el rechazo el cliente debe poder volver a subir")
	}
	if resp, _ := cli.subeComprobante(token, camposComprobante("BOL-67890"), imagenPNG(t)); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("segunda subida: %d", resp.StatusCode)
	}
	e.esperaCorreo(t) // aviso al negocio

	// Esta vez se cancela el pedido.
	var comp2 int
	e.Conn.QueryRow(`SELECT id FROM comprobantes WHERE pedido_id = $1 AND estado = 'por_revisar'`, id).Scan(&comp2)
	resp, _ = admin.post(fmt.Sprintf("%s/comprobantes/%d/rechazar", ruta, comp2),
		url.Values{"csrf": {csrf}, "motivo": {"Pago no recibido"}, "accion": {"cancelar"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != ruta+"?r=cancelado" {
		t.Fatalf("cancelar: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := estadoDe(t, e.entorno, id); got != "expirado" {
		t.Fatalf("estado = %q, quiero expirado", got)
	}
	m = e.esperaCorreo(t)
	if !strings.Contains(m.Asunto, "fue cancelado") || !strings.Contains(m.Texto, "Pago no recibido") {
		t.Errorf("correo de cancelación incorrecto:\n%s", m.Texto)
	}
	if _, pagina := cli.get("/pedido/" + token); strings.Contains(pagina, "Enviar comprobante") || !strings.Contains(pagina, "venció") {
		t.Error("tras cancelar, el cliente no debe poder subir y debe ver que el pedido ya no está activo")
	}
	// El inventario está libre otra vez.
	if _, body := cli.get("/"); !strings.Contains(body, `data-stock="10"`) {
		t.Error("tras cancelar, la tienda debe volver a mostrar las 10 unidades")
	}
}
