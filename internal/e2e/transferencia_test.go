package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"manualidades/internal/correo"
	"manualidades/internal/cuentas"
	"manualidades/internal/web"
)

// ---- ayudantes ----

// entornoTransferencia arma una tienda con un producto (stock 10), una cuenta
// bancaria activa y el correo configurado; los correos que la app intenta
// enviar se capturan en el canal en vez de salir por SMTP.
type entornoTransferencia struct {
	*entorno
	producto int
	correos  chan correo.Mensaje
}

func nuevoEntornoTransferencia(t *testing.T, conCuenta bool) *entornoTransferencia {
	t.Helper()
	web.ReiniciarLimitadores()
	e := nuevoEntorno(t, true)
	producto, _ := sembrarProducto(t, e.Conn, "Taza azul")
	if conCuenta {
		_, err := cuentas.Create(e.Conn, cuentas.Datos{
			Banco: "Banco de Prueba", TipoCuenta: "monetaria", Numero: "0000-0000-1", Titular: "Manualidades Cristal",
			Moneda: "GTQ", Instrucciones: "Indica el número de pedido.",
		}, cuentas.Quien{Usuario: "prueba"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := correo.Save(correo.Config{
		Servidor: "smtp.ejemplo.com", Puerto: 587, Seguridad: correo.STARTTLS, DesdeCorreo: "tienda@ejemplo.com",
		Destinatarios: []string{"dueña@ejemplo.com"}, UrlTienda: "https://tienda.ejemplo.com",
	}); err != nil {
		t.Fatal(err)
	}
	ch := make(chan correo.Mensaje, 20)
	e.App.EnviarCorreo = func(_ correo.Config, m correo.Mensaje) error { ch <- m; return nil }
	return &entornoTransferencia{entorno: e, producto: producto, correos: ch}
}

func (e *entornoTransferencia) esperaCorreo(t *testing.T) correo.Mensaje {
	t.Helper()
	select {
	case m := <-e.correos:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no se envió el correo esperado")
		return correo.Mensaje{}
	}
}

func (e *entornoTransferencia) sinCorreo(t *testing.T, motivo string) {
	t.Helper()
	select {
	case m := <-e.correos:
		t.Fatalf("se envió un correo que no debía (%s): %q a %v", motivo, m.Asunto, m.Para)
	case <-time.After(400 * time.Millisecond):
	}
}

func (c *cliente) postJSON(path string, v any) (*http.Response, map[string]any) {
	c.t.Helper()
	b, _ := json.Marshal(v)
	req, _ := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, cuerpo := c.hacer(req)
	var out map[string]any
	json.Unmarshal([]byte(cuerpo), &out)
	return resp, out
}

func pedidoJSON(producto int, email string, cantidad float64) map[string]any {
	return map[string]any{
		"cliente_nombre": "Ana Pérez", "cliente_telefono": "5555-1234", "cliente_email": email,
		"metodo_entrega": "recoger",
		"items":          []map[string]any{{"producto_id": producto, "cantidad": cantidad}},
	}
}

var reRedirect = regexp.MustCompile(`^/pedido/([0-9a-f-]{36})$`)

// creaPedido hace un pedido por transferencia y devuelve su token.
func (e *entornoTransferencia) creaPedido(t *testing.T, c *cliente, email string) (token string, id int) {
	t.Helper()
	resp, out := c.postJSON("/checkout/transferencia", pedidoJSON(e.producto, email, 1))
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("crear pedido: %d %v", resp.StatusCode, out)
	}
	m := reRedirect.FindStringSubmatch(fmt.Sprint(out["redirect"]))
	if m == nil {
		t.Fatalf("redirect inesperado: %v", out["redirect"])
	}
	return m[1], int(out["pedido_id"].(float64))
}

func imagenPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 60, 40))
	for x := 0; x < 60; x++ {
		for y := 0; y < 40; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 6), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// subeComprobante envía el formulario de la página del pedido.
func (c *cliente) subeComprobante(token string, campos map[string]string, archivo []byte) (*http.Response, string) {
	c.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range campos {
		w.WriteField(k, v)
	}
	if archivo != nil {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="comprobante"; filename="boleta.png"`)
		h.Set("Content-Type", "image/png")
		part, _ := w.CreatePart(h)
		part.Write(archivo)
	}
	w.Close()
	req, _ := http.NewRequest(http.MethodPost, c.base+"/pedido/"+token+"/comprobante", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.hacer(req)
}

func camposComprobante(boleta string) map[string]string {
	return map[string]string{
		"banco_origen": "Banco Industrial", "numero_boleta": boleta, "fecha": time.Now().Format("2006-01-02"),
	}
}

func archivosEnComprobantes(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("comprobantes")
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

func estadoDe(t *testing.T, e *entorno, id int) string {
	t.Helper()
	var s string
	e.Conn.QueryRow(`SELECT estado FROM pedidos WHERE id = $1`, id).Scan(&s)
	return s
}

// ---- pruebas ----

func TestTransferenciaNoSeOfreceSinCuentasActivas(t *testing.T) {
	e := nuevoEntornoTransferencia(t, false)
	_, body := e.cliente().get("/checkout")
	if strings.Contains(body, "Transferencia bancaria") {
		t.Error("sin cuentas activas no debe ofrecerse la transferencia")
	}
	if resp, out := e.cliente().postJSON("/checkout/transferencia", pedidoJSON(e.producto, "a@ejemplo.com", 1)); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(fmt.Sprint(out["error"]), "no está disponible") {
		t.Errorf("crear sin cuentas: %d %v", resp.StatusCode, out)
	}
	var n int
	e.Conn.QueryRow(`SELECT count(*) FROM pedidos`).Scan(&n)
	if n != 0 {
		t.Errorf("se creó un pedido sin cuentas a las que transferir")
	}
}

func TestTransferenciaSeOfreceConCuentaActivaYSeOcultaAlDesactivarla(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	if _, body := e.cliente().get("/checkout"); !strings.Contains(body, "Transferencia bancaria") || !strings.Contains(body, `name="metodo_pago"`) {
		t.Error("con una cuenta activa debe ofrecerse la transferencia")
	}
	e.Conn.Exec(`UPDATE cuentas_bancarias SET activa = false`)
	if _, body := e.cliente().get("/checkout"); strings.Contains(body, "Transferencia bancaria") {
		t.Error("con la cuenta desactivada no debe ofrecerse la transferencia")
	}
}

func TestTransferenciaRecorridoCompleto(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	c := e.cliente()

	token, id := e.creaPedido(t, c, "ana@ejemplo.com")
	if got := estadoDe(t, e.entorno, id); got != "espera_transferencia" {
		t.Fatalf("estado = %q", got)
	}

	// 1. Correo al cliente: cuenta, monto y enlace con la dirección configurada.
	m := e.esperaCorreo(t)
	if len(m.Para) != 1 || m.Para[0] != "ana@ejemplo.com" {
		t.Errorf("destinatario = %v", m.Para)
	}
	for _, quiero := range []string{"0000-0000-1", "Manualidades Cristal", "Q 25.00", fmt.Sprintf("#%d", id), "https://tienda.ejemplo.com/pedido/" + token} {
		if !strings.Contains(m.Texto, quiero) {
			t.Errorf("el correo al cliente no contiene %q:\n%s", quiero, m.Texto)
		}
	}

	// 2. Página del pedido: cuenta, formulario, sin caché.
	resp, body := c.get("/pedido/" + token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("página del pedido: %d", resp.StatusCode)
	}
	for _, quiero := range []string{"Banco de Prueba", "0000-0000-1", "Enviar comprobante", "Taza azul", "Q 25.00"} {
		if !strings.Contains(body, quiero) {
			t.Errorf("la página no muestra %q", quiero)
		}
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, quiero no-store", cc)
	}
	if rp := resp.Header.Get("Referrer-Policy"); rp != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, quiero no-referrer (el token va en la URL)", rp)
	}

	// 3. Sube el comprobante.
	resp, _ = c.subeComprobante(token, camposComprobante("BOL-12345"), imagenPNG(t))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/pedido/"+token+"?enviado=1" {
		t.Fatalf("subir: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := estadoDe(t, e.entorno, id); got != "transferencia_por_revisar" {
		t.Fatalf("estado tras subir = %q", got)
	}
	if n := archivosEnComprobantes(t); n != 1 {
		t.Errorf("hay %d archivos en comprobantes/, quiero 1", n)
	}

	// 4. Aviso al negocio.
	aviso := e.esperaCorreo(t)
	if len(aviso.Para) != 1 || aviso.Para[0] != "dueña@ejemplo.com" {
		t.Errorf("el aviso fue a %v", aviso.Para)
	}
	for _, quiero := range []string{"BOL-12345", "Banco Industrial", "Ana Pérez", fmt.Sprintf("https://tienda.ejemplo.com/admin/pedidos/%d", id)} {
		if !strings.Contains(aviso.Texto, quiero) {
			t.Errorf("el aviso al negocio no contiene %q:\n%s", quiero, aviso.Texto)
		}
	}

	// 5. La página ahora dice que está en revisión y ya no ofrece subir.
	_, body = c.get("/pedido/" + token + "?enviado=1")
	if !strings.Contains(body, "Estamos revisando tu transferencia") || strings.Contains(body, "Enviar comprobante") ||
		!strings.Contains(body, "BOL-12345") || !strings.Contains(body, "Recibimos tu comprobante") {
		t.Error("la página en revisión no muestra lo esperado")
	}
	if resp, _ := c.subeComprobante(token, camposComprobante("BOL-99999"), imagenPNG(t)); resp.StatusCode != http.StatusConflict {
		t.Errorf("segundo comprobante: %d, quiero 409", resp.StatusCode)
	}
	if n := archivosEnComprobantes(t); n != 1 {
		t.Errorf("tras el rechazo hay %d archivos, quiero 1 (sin huérfanos)", n)
	}
	e.sinCorreo(t, "un comprobante rechazado no avisa")
}

func TestTransferenciaComprobanteInvalidoNoDejaRastro(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	c := e.cliente()
	token, id := e.creaPedido(t, c, "ana@ejemplo.com")
	e.esperaCorreo(t)

	casos := []struct {
		nombre  string
		campos  map[string]string
		archivo []byte
		quiero  string
	}{
		{"sin imagen", camposComprobante("BOL-12345"), nil, "Adjunta la imagen"},
		{"no es una imagen", camposComprobante("BOL-12345"), []byte("<html><script>alert(1)</script></html>"), "no es una imagen válida"},
		{"boleta inválida", camposComprobante("1;2"), imagenPNG(t), "número de boleta"},
		{"fecha futura", map[string]string{"banco_origen": "B1", "numero_boleta": "BOL-12345", "fecha": time.Now().AddDate(0, 0, 3).Format("2006-01-02")}, imagenPNG(t), "no puede ser futura"},
		{"sin fecha", map[string]string{"banco_origen": "B1", "numero_boleta": "BOL-12345"}, imagenPNG(t), "fecha"},
		{"sin banco", map[string]string{"numero_boleta": "BOL-12345", "fecha": time.Now().Format("2006-01-02")}, imagenPNG(t), "banco"},
	}
	for _, tc := range casos {
		resp, body := c.subeComprobante(token, tc.campos, tc.archivo)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d, quiero 400", tc.nombre, resp.StatusCode)
		}
		if !strings.Contains(body, tc.quiero) {
			t.Errorf("%s: la página no explica el problema (%q)", tc.nombre, tc.quiero)
		}
		if got := estadoDe(t, e.entorno, id); got != "espera_transferencia" {
			t.Errorf("%s: el estado cambió a %q", tc.nombre, got)
		}
		if n := archivosEnComprobantes(t); n != 0 {
			t.Errorf("%s: quedaron %d archivos", tc.nombre, n)
		}
	}
	e.sinCorreo(t, "ningún comprobante inválido avisa")

	// Boleta repetida de otro pedido: se rechaza y no deja el archivo guardado.
	token2, _ := e.creaPedido(t, e.cliente(), "otra@ejemplo.com")
	e.esperaCorreo(t)
	if resp, _ := c.subeComprobante(token, camposComprobante("BOL-12345"), imagenPNG(t)); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("primer comprobante válido: %d", resp.StatusCode)
	}
	e.esperaCorreo(t)
	resp, body := e.cliente().subeComprobante(token2, camposComprobante("bol-12345"), imagenPNG(t))
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "ya está registrada") {
		t.Errorf("boleta repetida: %d", resp.StatusCode)
	}
	if n := archivosEnComprobantes(t); n != 1 {
		t.Errorf("hay %d archivos, quiero 1 (el de la boleta repetida se borra)", n)
	}
}

func TestTransferenciaPrivacidadDelEnlaceYDeLosComprobantes(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	c := e.cliente()
	token, _ := e.creaPedido(t, c, "ana@ejemplo.com")
	e.esperaCorreo(t)
	c.subeComprobante(token, camposComprobante("BOL-12345"), imagenPNG(t))
	e.esperaCorreo(t)

	var archivo string
	e.Conn.QueryRow(`SELECT archivo FROM comprobantes`).Scan(&archivo)
	if archivo == "" {
		t.Fatal("no se guardó el comprobante")
	}
	// El archivo no se sirve por ninguna ruta pública.
	for _, ruta := range []string{"/comprobantes/" + archivo, "/media/comprobantes/" + archivo, "/media/productos/" + archivo, "/static/" + archivo, "/pedido/" + token + "/" + archivo} {
		if resp, _ := c.get(ruta); resp.StatusCode == http.StatusOK {
			t.Errorf("%s se sirvió públicamente", ruta)
		}
	}
	// Tokens que no existen, mal formados o de un pedido de tarjeta no abren nada.
	for _, ruta := range []string{"/pedido/" + strings.Repeat("a", 36), "/pedido/00000000-0000-0000-0000-000000000000", "/pedido/abc", "/pedido/" + strings.ToUpper(token)} {
		if resp, _ := c.get(ruta); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s respondió %d, quiero 404", ruta, resp.StatusCode)
		}
	}
	if resp, _ := c.subeComprobante(strings.Repeat("b", 36), camposComprobante("BOL-00000"), imagenPNG(t)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("subir a un token desconocido: %d, quiero 404", resp.StatusCode)
	}
	// La página no muestra datos de otros: el pedido pide el token completo.
	if resp, _ := c.get("/pedido/1"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("/pedido/1 respondió %d: los ids no deben abrir pedidos de transferencia", resp.StatusCode)
	}
}

func TestTransferenciaConsultarReenviaElEnlace(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	c := e.cliente()
	token, id := e.creaPedido(t, c, "Ana@Ejemplo.com")
	e.esperaCorreo(t)

	pregunta := func(numero, mail string) string {
		_, body := e.cliente().post("/consultar-pedido", url.Values{"numero": {numero}, "correo": {mail}})
		return body
	}
	if resp, body := c.get("/consultar-pedido"); resp.StatusCode != http.StatusOK || !strings.Contains(body, "Enviarme el enlace") {
		t.Fatal("la página de consulta no carga")
	}

	conCoincidencia := pregunta(fmt.Sprint(id), " ana@ejemplo.com ")
	m := e.esperaCorreo(t)
	if m.Para[0] != "ana@ejemplo.com" || !strings.Contains(m.Texto, "https://tienda.ejemplo.com/pedido/"+token) {
		t.Errorf("el reenvío no lleva el enlace correcto: %v\n%s", m.Para, m.Texto)
	}
	if strings.Contains(conCoincidencia, token) {
		t.Fatal("la pantalla de consulta mostró el token")
	}

	// Sin coincidencia la respuesta es idéntica y no se envía nada.
	for nombre, args := range map[string][2]string{
		"otro correo":        {fmt.Sprint(id), "otra@ejemplo.com"},
		"pedido inexistente": {"99999", "ana@ejemplo.com"},
		"número inválido":    {"abc", "ana@ejemplo.com"},
	} {
		sin := pregunta(args[0], args[1])
		if !strings.Contains(sin, respuestaGenerica) {
			t.Errorf("%s: la respuesta cambia, filtra qué pedidos existen", nombre)
		}
	}
	e.sinCorreo(t, "datos sin coincidencia")

	// Una segunda petición inmediata para el mismo pedido no vuelve a enviar.
	pregunta(fmt.Sprint(id), "ana@ejemplo.com")
	e.sinCorreo(t, "reenvío demasiado pronto")
}

const respuestaGenerica = "Si el número de pedido y el correo coinciden"

func TestTransferenciaConsultarSeFrenaPorIP(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	c := e.cliente()
	var ultimo int
	for i := 0; i < 8; i++ {
		resp, _ := c.post("/consultar-pedido", url.Values{"numero": {fmt.Sprint(i + 1)}, "correo": {"x@ejemplo.com"}})
		ultimo = resp.StatusCode
	}
	if ultimo != http.StatusTooManyRequests {
		t.Fatalf("tras muchos intentos respondió %d, quiero 429", ultimo)
	}
}

// El enlace de los correos sale de la configuración: la cabecera Host de la
// petición la controla quien hace el pedido y no debe decidir adónde apunta.
func TestTransferenciaElEnlaceNoDependeDeLaCabeceraHost(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)

	b, _ := json.Marshal(pedidoJSON(e.producto, "victima@ejemplo.com", 1))
	req, _ := http.NewRequest(http.MethodPost, e.URL+"/checkout/transferencia", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "sitio-falso.example"
	resp, _ := e.cliente().hacer(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("crear: %d", resp.StatusCode)
	}
	m := e.esperaCorreo(t)
	if strings.Contains(m.Texto, "sitio-falso.example") || !strings.Contains(m.Texto, "https://tienda.ejemplo.com/pedido/") {
		t.Fatalf("el correo usó la cabecera Host:\n%s", m.Texto)
	}

	// Sin dirección configurada y con un Host que no es local, no se manda
	// ningún enlace por correo (el cliente igual lo ve en pantalla).
	if _, err := correo.Save(correo.Config{Servidor: "smtp.ejemplo.com", Puerto: 587, Seguridad: correo.STARTTLS, DesdeCorreo: "tienda@ejemplo.com"}); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(pedidoJSON(e.producto, "otra@ejemplo.com", 1))
	req, _ = http.NewRequest(http.MethodPost, e.URL+"/checkout/transferencia", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "sitio-falso.example"
	resp, body := e.cliente().hacer(req)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"redirect":"/pedido/`) {
		t.Fatalf("sin URL configurada el pedido debe crearse igual: %d %s", resp.StatusCode, body)
	}
	e.sinCorreo(t, "no hay dirección pública confiable")
}

func TestTransferenciaValidacionYLimitesDelPedido(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	c := e.cliente()

	malos := map[string]func(map[string]any){
		"sin correo":              func(p map[string]any) { p["cliente_email"] = "" },
		"correo inválido":         func(p map[string]any) { p["cliente_email"] = "no-es-correo" },
		"correo con nombre":       func(p map[string]any) { p["cliente_email"] = "Ana <a@b.com>" },
		"correo con salto":        func(p map[string]any) { p["cliente_email"] = "a@b.com\r\nBcc: x@y.z" },
		"sin nombre":              func(p map[string]any) { p["cliente_nombre"] = " " },
		"nombre larguísimo":       func(p map[string]any) { p["cliente_nombre"] = strings.Repeat("a", 151) },
		"entrega inválida":        func(p map[string]any) { p["metodo_entrega"] = "dron" },
		"domicilio sin dirección": func(p map[string]any) { p["metodo_entrega"] = "domicilio" },
		"carrito vacío":           func(p map[string]any) { p["items"] = []map[string]any{} },
	}
	for nombre, rompe := range malos {
		web.ReiniciarLimitadores() // cada intento cuenta para el límite por IP
		p := pedidoJSON(e.producto, "a@ejemplo.com", 1)
		rompe(p)
		if resp, out := c.postJSON("/checkout/transferencia", p); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %v, quiero 400", nombre, resp.StatusCode, out)
		}
	}
	var n int
	e.Conn.QueryRow(`SELECT count(*) FROM pedidos`).Scan(&n)
	if n != 0 {
		t.Fatalf("los pedidos inválidos dejaron %d filas", n)
	}

	// Más de lo que hay en stock: no se reserva nada.
	web.ReiniciarLimitadores()
	if resp, out := c.postJSON("/checkout/transferencia", pedidoJSON(e.producto, "a@ejemplo.com", 11)); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(fmt.Sprint(out["error"]), "stock") {
		t.Errorf("sin stock: %d %v", resp.StatusCode, out)
	}

	// Tope de pedidos abiertos por correo (el cuarto se rechaza).
	web.ReiniciarLimitadores()
	for i := 0; i < 3; i++ {
		e.creaPedido(t, c, "Ana@ejemplo.com")
	}
	if resp, out := c.postJSON("/checkout/transferencia", pedidoJSON(e.producto, "ana@EJEMPLO.com", 1)); resp.StatusCode != http.StatusConflict ||
		!strings.Contains(fmt.Sprint(out["error"]), "varios pedidos") {
		t.Errorf("cuarto pedido abierto: %d %v", resp.StatusCode, out)
	}
	// El stock quedó reservado por los tres.
	if _, body := c.get("/"); !strings.Contains(body, `data-stock="7"`) {
		t.Error("la tienda debe mostrar el stock disponible descontando lo reservado (7)")
	}
}

func TestTransferenciaCrearPedidoSeFrenaPorIP(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	c := e.cliente()
	var ultimo int
	for i := 0; i < 8; i++ {
		resp, _ := c.postJSON("/checkout/transferencia", pedidoJSON(e.producto, fmt.Sprintf("u%d@ejemplo.com", i), 1))
		ultimo = resp.StatusCode
	}
	if ultimo != http.StatusTooManyRequests {
		t.Fatalf("tras muchos pedidos respondió %d, quiero 429", ultimo)
	}
}
