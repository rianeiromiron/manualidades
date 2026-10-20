package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"manualidades/internal/usuarios"
)

// pedidoPagado crea un pedido por transferencia, lo aprueba desde el admin y
// vacía los correos. Devuelve el id.
func (e *entornoTransferencia) pedidoPagado(t *testing.T, admin *cliente, csrf, email, boleta string) int {
	t.Helper()
	_, id := e.pedidoConComprobante(t, e.cliente(), email, boleta, false)
	var comp int
	e.Conn.QueryRow(`SELECT id FROM comprobantes WHERE pedido_id = $1`, id).Scan(&comp)
	resp, _ := admin.post(fmt.Sprintf("/admin/pedidos/%d/comprobantes/%d/aprobar", id, comp), url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("aprobar: %d", resp.StatusCode)
	}
	e.esperaCorreo(t)
	return id
}

func TestCancelarUnPedidoPagadoDesdeElAdmin(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	id := e.pedidoPagado(t, admin, csrf, "ana@ejemplo.com", "BOL-12345")
	ruta := fmt.Sprintf("/admin/pedidos/%d", id)
	if got := stockDe(t, e.entorno, e.producto); got != 9 {
		t.Fatalf("stock tras la venta = %v, quiero 9", got)
	}

	// El detalle ofrece cancelar (sin preguntar por bodega: aún no se entrega) y ya
	// no ofrece «cancelado» en la lista de estados.
	_, body := admin.get(ruta)
	if !strings.Contains(body, "Cancelar pedido") || strings.Contains(body, `name="volvio_a_bodega"`) {
		t.Error("un pedido pagado debe poder cancelarse sin preguntar si volvió a bodega")
	}
	if strings.Contains(body, `<option value="cancelado"`) {
		t.Error("«cancelado» no debe estar en la lista de estados")
	}

	// Seguridad: sin CSRF, sin permiso y por GET no se cancela.
	cancelar := ruta + "/cancelar"
	if resp, _ := admin.post(cancelar, url.Values{}); resp.StatusCode == http.StatusSeeOther {
		t.Error("una cancelación sin CSRF fue aceptada")
	}
	if resp, _ := admin.get(cancelar); resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
		t.Errorf("GET sobre cancelar respondió %d", resp.StatusCode)
	}
	if _, err := usuarios.CreateUsuario(e.Conn, "silvia", "clave-de-silvia-1", usuarios.RolAdministrativo, nil); err != nil {
		t.Fatal(err)
	}
	sin := e.cliente()
	sin.login("silvia", "clave-de-silvia-1")
	if resp, _ := sin.post(cancelar, url.Values{"csrf": {sin.csrf()}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("cancelar sin módulo: %d, quiero 403", resp.StatusCode)
	}
	if resp, _ := sin.post(ruta+"/reembolsado", url.Values{"csrf": {sin.csrf()}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("reembolsar sin módulo: %d, quiero 403", resp.StatusCode)
	}
	if got := estadoDe(t, e.entorno, id); got != "pagado" {
		t.Fatalf("antes de cancelar el estado cambió a %q", got)
	}

	// Cancelar.
	resp, _ := admin.post(cancelar, url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != ruta+"?r=cancelado_devuelto" {
		t.Fatalf("cancelar: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := estadoDe(t, e.entorno, id); got != "cancelado" {
		t.Fatalf("estado = %q, quiero cancelado", got)
	}
	if got := stockDe(t, e.entorno, e.producto); got != 10 {
		t.Errorf("stock = %v, quiero 10 (volvió al inventario)", got)
	}
	_, body = e.cliente().get("/")
	if !strings.Contains(body, `data-stock="10"`) {
		t.Error("la tienda debe volver a mostrar las 10 unidades")
	}

	// El detalle explica qué pasó y qué falta.
	_, body = admin.get(ruta + "?r=cancelado_devuelto")
	for _, quiero := range []string{"Pedido cancelado", "volvieron al inventario", "Falta devolver Q 25.00", "Marcar como reembolsado", "no se puede reabrir"} {
		if !strings.Contains(body, quiero) {
			t.Errorf("el detalle no muestra %q", quiero)
		}
	}
	if strings.Contains(body, "Actualizar estado") {
		t.Error("un pedido cancelado no debe permitir cambiar su estado")
	}
	_, body = admin.get("/admin/pedidos/pagos")
	if !strings.Contains(body, "Reembolso pendiente") {
		t.Error("la lista de pagos debe marcar el reembolso pendiente")
	}

	// Es definitivo: ni por el formulario de estado ni por el de cancelar.
	for _, estado := range []string{"pagado", "entregado", "cancelado"} {
		resp, body := admin.post(ruta, url.Values{"csrf": {csrf}, "estado": {estado}})
		if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "alert-error") {
			t.Errorf("estado %q sobre un cancelado: %d", estado, resp.StatusCode)
		}
	}
	if got := estadoDe(t, e.entorno, id); got != "cancelado" {
		t.Errorf("el pedido cancelado cambió a %q", got)
	}
	resp, body = admin.post(cancelar, url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "cancelado") {
		t.Errorf("segunda cancelación: %d", resp.StatusCode)
	}
	if got := stockDe(t, e.entorno, e.producto); got != 10 {
		t.Errorf("la segunda cancelación cambió el stock a %v", got)
	}

	// Anotar el reembolso.
	resp, _ = admin.post(ruta+"/reembolsado", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != ruta+"?r=reembolsado" {
		t.Fatalf("reembolsado: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, body = admin.get(ruta + "?r=reembolsado")
	if !strings.Contains(body, "Reembolsado") || strings.Contains(body, "Falta devolver") {
		t.Error("tras anotar el reembolso el detalle debe mostrarlo como hecho")
	}
	if resp, _ := admin.post(ruta+"/reembolsado", url.Values{"csrf": {csrf}}); resp.StatusCode != http.StatusConflict {
		t.Errorf("segundo reembolso: %d, quiero 409", resp.StatusCode)
	}
	// El cliente ve que su pedido se canceló.
	if _, body := e.cliente().get("/consultar-pedido"); body == "" {
		t.Error("la página de consulta no carga")
	}
}

func TestCancelarUnPedidoEntregadoPreguntaPorBodega(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	id := e.pedidoPagado(t, admin, csrf, "ana@ejemplo.com", "BOL-12345")
	ruta := fmt.Sprintf("/admin/pedidos/%d", id)

	if resp, _ := admin.post(ruta, url.Values{"csrf": {csrf}, "estado": {"entregado"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("marcar entregado: %d", resp.StatusCode)
	}
	_, body := admin.get(ruta)
	if !strings.Contains(body, `name="volvio_a_bodega"`) || !strings.Contains(body, "¿El producto volvió a bodega?") {
		t.Fatal("un pedido entregado debe preguntar si el producto volvió a bodega")
	}

	// Sin respuesta, no se cancela.
	resp, body := admin.post(ruta+"/cancelar", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Indica si el producto volvió a bodega") {
		t.Errorf("sin respuesta: %d", resp.StatusCode)
	}
	if got := estadoDe(t, e.entorno, id); got != "entregado" {
		t.Fatalf("estado = %q, quiero entregado", got)
	}

	// No volvió: el inventario no se toca, el reembolso queda pendiente.
	resp, _ = admin.post(ruta+"/cancelar", url.Values{"csrf": {csrf}, "volvio_a_bodega": {"no"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != ruta+"?r=cancelado_sin_devolver" {
		t.Fatalf("cancelar sin devolver: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := stockDe(t, e.entorno, e.producto); got != 9 {
		t.Errorf("stock = %v, quiero 9 (el producto no volvió)", got)
	}
	_, body = admin.get(ruta + "?r=cancelado_sin_devolver")
	for _, quiero := range []string{"no se tocó", "Su inventario no se devolvió", "Falta devolver Q 25.00"} {
		if !strings.Contains(body, quiero) {
			t.Errorf("el detalle no muestra %q", quiero)
		}
	}

	// Otro pedido entregado cuyo producto sí volvió.
	id2 := e.pedidoPagado(t, admin, csrf, "bea@ejemplo.com", "BOL-67890")
	ruta2 := fmt.Sprintf("/admin/pedidos/%d", id2)
	admin.post(ruta2, url.Values{"csrf": {csrf}, "estado": {"entregado"}})
	if got := stockDe(t, e.entorno, e.producto); got != 8 {
		t.Fatalf("stock antes = %v, quiero 8", got)
	}
	if resp, _ := admin.post(ruta2+"/cancelar", url.Values{"csrf": {csrf}, "volvio_a_bodega": {"si"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("cancelar devolviendo: %d", resp.StatusCode)
	}
	if got := stockDe(t, e.entorno, e.producto); got != 9 {
		t.Errorf("stock = %v, quiero 9 (volvió 1)", got)
	}
}

func TestNoSePuedeCancelarUnPedidoEnRevision(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	_, id := e.pedidoConComprobante(t, e.cliente(), "ana@ejemplo.com", "BOL-12345", false) // en revisión, sin aprobar

	resp, body := admin.post(fmt.Sprintf("/admin/pedidos/%d/cancelar", id), url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "comprobante por revisar") {
		t.Errorf("cancelar con comprobante en revisión: %d", resp.StatusCode)
	}
	if got := estadoDe(t, e.entorno, id); got != "transferencia_por_revisar" {
		t.Errorf("estado = %q", got)
	}
	if resp, _ := admin.post("/admin/pedidos/99999/cancelar", url.Values{"csrf": {csrf}}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("pedido inexistente: %d, quiero 404", resp.StatusCode)
	}
}

func TestCancelarUnPedidoQueEsperaTransferencia(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	cli := e.cliente()
	token, id := e.creaPedido(t, cli, "ana@ejemplo.com")
	e.esperaCorreo(t)
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	ruta := fmt.Sprintf("/admin/pedidos/%d", id)

	if _, body := cli.get("/"); !strings.Contains(body, `data-stock="9"`) {
		t.Fatal("el pedido debe estar reservando una unidad")
	}
	// El detalle ofrece cancelar, sin preguntas de bodega ni de reembolso.
	_, body := admin.get(ruta)
	if !strings.Contains(body, "Cancelar pedido") || !strings.Contains(body, "todavía no ha subido su comprobante") ||
		strings.Contains(body, `name="volvio_a_bodega"`) {
		t.Fatal("un pedido que espera la transferencia debe poder cancelarse")
	}

	// Seguridad: sin CSRF no se cancela.
	cancelar := ruta + "/cancelar"
	if resp, _ := admin.post(cancelar, url.Values{"motivo": {"x"}}); resp.StatusCode == http.StatusSeeOther {
		t.Error("una cancelación sin CSRF fue aceptada")
	}
	// Un motivo con salto de línea se rechaza.
	if resp, _ := admin.post(cancelar, url.Values{"csrf": {csrf}, "motivo": {"mal\nBcc: x@y.z"}}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("motivo con salto de línea: %d, quiero 400", resp.StatusCode)
	}
	if got := estadoDe(t, e.entorno, id); got != "espera_transferencia" {
		t.Fatalf("un intento inválido cambió el estado a %q", got)
	}

	resp, _ := admin.post(cancelar, url.Values{"csrf": {csrf}, "motivo": {"El cliente pidió cancelar <b>hoy</b>"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != ruta+"?r=cancelado_liberado" {
		t.Fatalf("cancelar: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := estadoDe(t, e.entorno, id); got != "cancelado" {
		t.Fatalf("estado = %q, quiero cancelado", got)
	}

	// La reserva se liberó; el inventario nunca se tocó.
	if _, body := cli.get("/"); !strings.Contains(body, `data-stock="10"`) {
		t.Error("tras cancelar, la tienda debe volver a mostrar las 10 unidades")
	}
	if got := stockDe(t, e.entorno, e.producto); got != 10 {
		t.Errorf("stock físico = %v, quiero 10", got)
	}
	var movs int
	e.Conn.QueryRow(`SELECT count(*) FROM movimientos_inventario WHERE pedido_id = $1`, id).Scan(&movs)
	if movs != 0 {
		t.Errorf("hay %d movimientos del pedido, quiero 0", movs)
	}

	// Aviso al cliente.
	m := e.esperaCorreo(t)
	if m.Para[0] != "ana@ejemplo.com" || !strings.Contains(m.Asunto, "fue cancelado") || !strings.Contains(m.Texto, "El cliente pidió cancelar") {
		t.Errorf("correo de cancelación incorrecto: %v %q\n%s", m.Para, m.Asunto, m.Texto)
	}

	// El detalle lo explica, sin pedir reembolso.
	_, body = admin.get(ruta + "?r=cancelado_liberado")
	for _, quiero := range []string{"Pedido cancelado", "antes de pagarse", "no hay nada que reembolsar", "Se liberó la reserva"} {
		if !strings.Contains(body, quiero) {
			t.Errorf("el detalle no muestra %q", quiero)
		}
	}
	if strings.Contains(body, "Falta devolver") || strings.Contains(body, "Actualizar estado") {
		t.Error("un pedido cancelado sin pago no debe pedir reembolso ni permitir cambiar su estado")
	}

	// El cliente ve que se canceló y ya no puede subir comprobantes.
	if _, body := cli.get("/pedido/" + token); !strings.Contains(body, "fue cancelado") || strings.Contains(body, "Enviar comprobante") {
		t.Error("la página del cliente debe decir que el pedido fue cancelado")
	}
	if resp, _ := cli.subeComprobante(token, camposComprobante("BOL-12345"), imagenPNG(t)); resp.StatusCode != http.StatusConflict {
		t.Errorf("subir tras cancelar: %d, quiero 409", resp.StatusCode)
	}
	// Definitivo.
	if resp, _ := admin.post(cancelar, url.Values{"csrf": {csrf}}); resp.StatusCode != http.StatusConflict {
		t.Errorf("segunda cancelación: %d, quiero 409", resp.StatusCode)
	}
}
