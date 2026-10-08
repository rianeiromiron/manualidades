package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"manualidades/internal/inventario"
	"manualidades/internal/usuarios"
)

const rutaMovs = "/admin/mantenimiento/inventario/movimientos"

func stockEnBD(t *testing.T, e *entorno, productoID int) float64 {
	t.Helper()
	s, err := inventario.StockActual(e.Conn, productoID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Formularios «Anular» presentes en la página, por id de movimiento.
func formsAnular(body string) []string {
	re := regexp.MustCompile(`/movimientos/(\d+)/anular`)
	var ids []string
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

// El caso real que motivó la función: se registró 11 en vez de 1.
func TestAnularIngresoDesdeLaPantalla(t *testing.T) {
	s := nuevoEscenario(t) // trae un producto con stock y un pedido pagado
	e := s.e
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	stockInicial := stockEnBD(t, e, s.productoID)

	// 1. Registrar el ingreso equivocado desde el formulario.
	hoy := time.Now().Format("2006-01-02")
	_, body := admin.post(rutaMovs, url.Values{
		"csrf": {csrf}, "producto_id": {fmt.Sprint(s.productoID)}, "tipo": {"ingreso"},
		"cantidad": {"11"}, "fecha": {hoy}, "motivo": {"compra equivocada"},
	})
	if !strings.Contains(body, "Movimiento registrado") {
		t.Fatalf("no se registró el ingreso: %.200s", body)
	}
	var malo int
	if err := e.Conn.QueryRow(`SELECT max(id) FROM movimientos_inventario WHERE motivo = 'compra equivocada'`).Scan(&malo); err != nil {
		t.Fatal(err)
	}
	if got := stockEnBD(t, e, s.productoID); got != stockInicial+11 {
		t.Fatalf("stock tras el ingreso: %v", got)
	}

	// 2. La lista ofrece «Anular» para ese movimiento manual.
	_, lista := admin.get(rutaMovs)
	if !contiene(formsAnular(lista), fmt.Sprint(malo)) {
		t.Fatalf("la lista no ofrece Anular para el movimiento #%d", malo)
	}
	if !strings.Contains(lista, `data-confirm="¿Anular el movimiento #`) {
		t.Error("el botón Anular no pide confirmación")
	}

	// 3. Anular.
	resp, body := admin.post(fmt.Sprintf("%s/%d/anular", rutaMovs, malo), url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "anulado") {
		t.Fatalf("anular respondió %d: %.300s", resp.StatusCode, body)
	}
	if got := stockEnBD(t, e, s.productoID); got != stockInicial {
		t.Errorf("tras anular, stock %v, se esperaba %v", got, stockInicial)
	}

	// 4. El historial lo muestra: el original anulado, sin botón; y su anulación.
	_, lista = admin.get(rutaMovs)
	if contiene(formsAnular(lista), fmt.Sprint(malo)) {
		t.Error("el movimiento ya anulado todavía ofrece Anular")
	}
	if !strings.Contains(lista, "Anulado") || !strings.Contains(lista, fmt.Sprintf("Anulación de #%d", malo)) {
		t.Error("la lista no distingue el movimiento anulado ni su anulación")
	}
	if !strings.Contains(lista, `class="mov-anulado"`) {
		t.Error("la fila anulada no se marca como tal")
	}

	// 5. Anular de nuevo (doble clic / página vieja) da un mensaje claro y no
	// vuelve a mover el stock.
	_, body = admin.post(fmt.Sprintf("%s/%d/anular", rutaMovs, malo), url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "ya fue anulado") {
		t.Errorf("el segundo intento debería decir que ya fue anulado: %.300s", body)
	}
	if got := stockEnBD(t, e, s.productoID); got != stockInicial {
		t.Errorf("el segundo intento movió el stock: %v", got)
	}

	// 6. El historial del producto y el reporte también lo muestran.
	_, frag := admin.get(fmt.Sprintf("/admin/mantenimiento/inventario/productos/%d/movimientos", s.productoID))
	if !strings.Contains(frag, "Anulado") || !strings.Contains(frag, "Anulación de #") {
		t.Error("el historial del producto no muestra la anulación")
	}
	_, rep := admin.get(fmt.Sprintf("/admin/reportes/movimientos?f=1&ingresos=on&egresos=on&desde=%s&hasta=%s", hoy, hoy))
	if !strings.Contains(rep, "Anulación de #") {
		t.Error("el reporte de movimientos no muestra la anulación")
	}

	// 7. El stock queda correcto al registrar el dato bien.
	admin.post(rutaMovs, url.Values{
		"csrf": {csrf}, "producto_id": {fmt.Sprint(s.productoID)}, "tipo": {"ingreso"},
		"cantidad": {"1"}, "fecha": {hoy}, "motivo": {"compra correcta"},
	})
	if got := stockEnBD(t, e, s.productoID); got != stockInicial+1 {
		t.Errorf("stock final %v, se esperaba %v", got, stockInicial+1)
	}
}

func TestAnularRespetaPermisosYProtecciones(t *testing.T) {
	s := nuevoEscenario(t)
	e := s.e
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	hoy := time.Now().Format("2006-01-02")
	admin.post(rutaMovs, url.Values{
		"csrf": {csrf}, "producto_id": {fmt.Sprint(s.productoID)}, "tipo": {"ingreso"},
		"cantidad": {"5"}, "fecha": {hoy}, "motivo": {"para anular"},
	})
	var id int
	e.Conn.QueryRow(`SELECT max(id) FROM movimientos_inventario WHERE motivo = 'para anular'`).Scan(&id)
	ruta := fmt.Sprintf("%s/%d/anular", rutaMovs, id)
	stock0 := stockEnBD(t, e, s.productoID)

	// Sin token CSRF, con token falso, y por GET: nada se anula.
	for _, datos := range []url.Values{{}, {"csrf": {"falso"}}} {
		if resp, _ := admin.post(ruta, datos); resp.StatusCode != http.StatusForbidden {
			t.Errorf("anular con CSRF %v respondió %d, se esperaba 403", datos, resp.StatusCode)
		}
	}
	if resp, _ := admin.get(ruta); resp.StatusCode == http.StatusOK {
		t.Error("anular por GET respondió 200")
	}

	// Un usuario SIN el módulo de inventario no puede anular.
	var reportes int
	e.Conn.QueryRow(`SELECT id FROM modulos WHERE clave = 'reportes'`).Scan(&reportes)
	if _, err := usuarios.CreateUsuario(e.Conn, "bea", "clave-de-bea-1", usuarios.RolAdministrativo, []int{reportes}); err != nil {
		t.Fatal(err)
	}
	bea := e.cliente()
	if !bea.login("bea", "clave-de-bea-1") {
		t.Fatal("bea no pudo iniciar sesión")
	}
	if resp, _ := bea.post(ruta, url.Values{"csrf": {bea.csrf()}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("un usuario sin inventario respondió %d al anular, se esperaba 403", resp.StatusCode)
	}
	// Anónimo: al login.
	if resp, _ := e.cliente().post(ruta, url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("un anónimo respondió %d, se esperaba redirección al login", resp.StatusCode)
	}
	if got := stockEnBD(t, e, s.productoID); got != stock0 {
		t.Errorf("algún intento no autorizado movió el stock: %v → %v", stock0, got)
	}

	// Los movimientos que nacen de un pedido de la tienda no se anulan aquí.
	var delPedido int
	e.Conn.QueryRow(`SELECT id FROM movimientos_inventario WHERE pedido_id = $1 LIMIT 1`, s.pedidoID).Scan(&delPedido)
	if delPedido == 0 {
		t.Fatal("el escenario debería tener un movimiento de pedido")
	}
	_, lista := admin.get(rutaMovs)
	if contiene(formsAnular(lista), fmt.Sprint(delPedido)) {
		t.Error("un movimiento de pedido ofrece el botón Anular")
	}
	if !strings.Contains(lista, fmt.Sprintf("Pedido #%d", s.pedidoID)) {
		t.Error("la lista no indica de qué pedido viene el movimiento")
	}
	stock1 := stockEnBD(t, e, s.productoID)
	_, body := admin.post(fmt.Sprintf("%s/%d/anular", rutaMovs, delPedido), url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "viene de un pedido") {
		t.Errorf("anular un movimiento de pedido debería explicar por qué no: %.300s", body)
	}
	if got := stockEnBD(t, e, s.productoID); got != stock1 {
		t.Error("anular un movimiento de pedido movió el stock")
	}

	// Ids absurdos: sin error 5xx ni filtración (la inyección SQL ya se cubre
	// en sqli_test.go; aquí, el comportamiento).
	for _, malo := range []string{"abc", "0", "-1", "999999", "1%20OR%201=1"} {
		resp, body := admin.post(rutaMovs+"/"+malo+"/anular", url.Values{"csrf": {csrf}})
		if resp.StatusCode >= 500 || filtraErrorSQL(body) != "" {
			t.Errorf("anular el id %q respondió %d / filtra SQL", malo, resp.StatusCode)
		}
	}
}

// En un navegador real: el botón pide confirmación; cancelar no anula y
// aceptar sí. (Prueba además que el diálogo viene de data-confirm, sin JS inline.)
func TestNavegadorAnularPideConfirmacion(t *testing.T) {
	s := nuevoEscenario(t)
	e := s.e
	hoy := time.Now().Truncate(24 * time.Hour)
	if err := inventario.CreateMovimiento(e.Conn, s.productoID, "ingreso", 11, "compra equivocada", false, 0, hoy); err != nil {
		t.Fatal(err)
	}
	var id int
	e.Conn.QueryRow(`SELECT max(id) FROM movimientos_inventario WHERE motivo = 'compra equivocada'`).Scan(&id)
	stock0 := stockEnBD(t, e, s.productoID)

	n := nuevoNavegador(t)
	n.iniciaSesionAdmin(t, e.URL)
	n.correr(t, chromedp.Navigate(e.URL+rutaMovs), chromedp.WaitVisible(`.data-table`))
	n.marca(t, fmt.Sprintf(`document.querySelector('form[action$="/movimientos/%d/anular"] button')`, id), "btn-anular")

	// Cancelar: no pasa nada.
	n.aceptar.Store(false)
	n.correr(t, chromedp.Click(`#btn-anular`))
	for fin := time.Now().Add(8 * time.Second); len(n.dialogosVistos()) == 0 && time.Now().Before(fin); {
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	d := n.dialogosVistos()
	if len(d) != 1 || !strings.Contains(d[0], fmt.Sprintf("¿Anular el movimiento #%d (ingreso de 11.00", id)) {
		t.Fatalf("diálogo inesperado: %q", d)
	}
	if got := stockEnBD(t, e, s.productoID); got != stock0 {
		t.Error("cancelar el diálogo anuló el movimiento")
	}

	// Aceptar: se anula.
	n.aceptar.Store(true)
	n.correr(t, chromedp.Click(`#btn-anular`))
	for fin := time.Now().Add(8 * time.Second); stockEnBD(t, e, s.productoID) == stock0 && time.Now().Before(fin); {
		time.Sleep(150 * time.Millisecond)
	}
	if got := stockEnBD(t, e, s.productoID); got != stock0-11 {
		t.Errorf("aceptar el diálogo no anuló: stock %v, se esperaba %v", got, stock0-11)
	}
	n.errores.exigeSinErrores(t, "pantalla de movimientos tras anular")
}
