package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"manualidades/internal/usuarios"
)

// ---------------------------------------------------------------------------
// Cabeceras / CSP
// ---------------------------------------------------------------------------

func TestCSPEnTodasLasZonas(t *testing.T) {
	e := nuevoEntorno(t, true)
	c := e.cliente()

	for _, ruta := range []string{"/", "/carrito", "/checkout", "/admin/login", "/static/js/carrito.js"} {
		resp, _ := c.get(ruta)
		csp := resp.Header.Get("Content-Security-Policy")
		if csp == "" {
			t.Errorf("%s: sin Content-Security-Policy", ruta)
			continue
		}
		var script string
		for _, d := range strings.Split(csp, ";") {
			if d = strings.TrimSpace(d); strings.HasPrefix(d, "script-src") {
				script = d
			}
		}
		if script != "script-src 'self'" {
			t.Errorf("%s: script-src = %q, se esperaba exactamente 'self'", ruta, script)
		}
		if resp.Header.Get("X-Frame-Options") != "DENY" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: faltan X-Frame-Options/X-Content-Type-Options", ruta)
		}
	}
}

// Ninguna página HTML que sirve la aplicación debe traer JavaScript inline:
// con la CSP actual simplemente no correría. Se comprueba sobre las
// respuestas reales (no solo sobre los archivos de plantilla).
func TestPaginasRenderizadasSinJavaScriptInline(t *testing.T) {
	e := nuevoEntorno(t, true)
	c := e.cliente()
	c.entraComoAdmin()

	inline := regexp.MustCompile(`(?is)<script\b[^>]*>`)
	conSrc := regexp.MustCompile(`(?is)\bsrc\s*=`)
	evento := regexp.MustCompile(`(?i)\son[a-z]+\s*=`)

	rutas := []string{
		"/", "/carrito", "/checkout",
		"/admin", "/admin/sesiones", "/admin/cambiar-password",
		"/admin/usuarios", "/admin/usuarios/nuevo", "/admin/sitio",
		"/admin/mantenimiento/inventario/productos", "/admin/mantenimiento/inventario/categorias",
		"/admin/mantenimiento/inventario/productos/nuevo", "/admin/mantenimiento/inventario/movimientos",
		"/admin/pedidos", "/admin/reportes", "/admin/reportes/ventas", "/admin/reportes/movimientos",
	}
	for _, ruta := range rutas {
		resp, body := c.get(ruta)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: respondió %d", ruta, resp.StatusCode)
			continue
		}
		for _, tag := range inline.FindAllString(body, -1) {
			if !conSrc.MatchString(tag) {
				t.Errorf("%s: <script> inline en la respuesta: %s", ruta, tag)
			}
		}
		if m := evento.FindString(body); m != "" {
			t.Errorf("%s: atributo de evento inline en la respuesta: %q", ruta, strings.TrimSpace(m))
		}
	}
}

// ---------------------------------------------------------------------------
// Contraseñas: largo mínimo y máximo (bcrypt solo usa 72 bytes)
// ---------------------------------------------------------------------------

func TestLargoDePasswordEnCrearUsuario(t *testing.T) {
	e := nuevoEntorno(t, true)
	c := e.cliente()
	c.entraComoAdmin()
	csrf := c.csrf()

	crear := func(usuario, clave string) (*http.Response, string) {
		return c.post("/admin/usuarios/nuevo", url.Values{
			"csrf": {csrf}, "usuario": {usuario}, "password": {clave}, "confirmar": {clave}, "rol": {"superusuario"},
		})
	}
	existe := func(usuario string) bool {
		var n int
		if err := e.Conn.QueryRow(`SELECT count(*) FROM usuarios WHERE usuario = $1`, usuario).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}

	casos := []struct {
		nombre  string
		largo   int
		creado  bool
		mensaje string
	}{
		{"7 caracteres", 7, false, "al menos 8"},
		{"8 caracteres", 8, true, ""},
		{"72 caracteres", 72, true, ""},
		{"73 caracteres", 73, false, "no puede superar los 72"},
		{"500 caracteres", 500, false, "no puede superar los 72"},
	}
	for i, caso := range casos {
		usuario := "u" + strconv.Itoa(i)
		resp, body := crear(usuario, strings.Repeat("a", caso.largo))
		if caso.creado {
			if resp.StatusCode != http.StatusSeeOther || !existe(usuario) {
				t.Errorf("%s: debió crearse (status %d)", caso.nombre, resp.StatusCode)
			}
			continue
		}
		if existe(usuario) {
			t.Errorf("%s: se creó un usuario con contraseña inválida", caso.nombre)
		}
		if !strings.Contains(body, caso.mensaje) {
			t.Errorf("%s: el mensaje de error no contiene %q", caso.nombre, caso.mensaje)
		}
	}
}

func TestLargoDePasswordEnCambiarPassword(t *testing.T) {
	e := nuevoEntorno(t, true)
	c := e.cliente()
	c.entraComoAdmin()
	csrf := c.csrf()

	intentar := func(nueva string) string {
		_, body := c.post("/admin/cambiar-password", url.Values{
			"csrf": {csrf}, "actual": {adminClave}, "nueva": {nueva}, "confirmar": {nueva},
		})
		return body
	}
	if body := intentar(strings.Repeat("b", 73)); !strings.Contains(body, "no puede superar los 72") {
		t.Error("73 caracteres no fue rechazado en cambiar-password")
	}
	if body := intentar("corta"); !strings.Contains(body, "al menos 8") {
		t.Error("una contraseña corta no fue rechazada en cambiar-password")
	}
	// Las rechazadas no cambiaron nada: la sesión sigue viva y la clave vieja sirve.
	if !c.estaAutenticado() {
		t.Error("un cambio rechazado cerró la sesión")
	}
	if !e.cliente().login(adminUsuario, adminClave) {
		t.Error("la contraseña original dejó de funcionar tras un cambio rechazado")
	}
}

// ---------------------------------------------------------------------------
// Sesiones individuales
// ---------------------------------------------------------------------------

// sidDeCookie extrae el id de sesión (4.º campo del payload firmado).
func sidDeCookie(t *testing.T, valor string) string {
	t.Helper()
	payloadEnc, _, _ := strings.Cut(valor, ".")
	b, err := base64.RawURLEncoding.DecodeString(payloadEnc)
	if err != nil {
		t.Fatalf("cookie ilegible: %v", err)
	}
	campos := strings.SplitN(string(b), "|", 4)
	if len(campos) != 4 {
		t.Fatalf("la cookie no trae 4 campos: %q", b)
	}
	return campos[3]
}

// firmar replica el esquema de firma de la cookie (HMAC-SHA256 con la
// SecretKey de admin.json) para fabricar cookies como lo haría un atacante
// que conociera la llave, o para comprobar que el formato antiguo ya no vale.
func firmar(t *testing.T, payload string) string {
	t.Helper()
	b, err := os.ReadFile("admin.json")
	if err != nil {
		t.Fatal(err)
	}
	var adm struct {
		SecretKey string `json:"secret_key"`
	}
	if err := json.Unmarshal(b, &adm); err != nil {
		t.Fatal(err)
	}
	llave, err := base64.StdEncoding.DecodeString(adm.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := hmac.New(sha256.New, llave)
	mac.Write([]byte(enc))
	return enc + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

var reRevocar = regexp.MustCompile(`/admin/sesiones/([0-9a-f]{32})/revocar`)

func sidsListados(body string) []string {
	var out []string
	for _, m := range reRevocar.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestRevocarUnaSesionNoAfectaALasDemas(t *testing.T) {
	e := nuevoEntorno(t, true)
	a, b := e.cliente(), e.cliente()
	a.entraComoAdmin()
	b.entraComoAdmin()
	sidB := sidDeCookie(t, b.cookieSesion())

	if !a.estaAutenticado() || !b.estaAutenticado() {
		t.Fatal("las dos sesiones deberían estar activas")
	}

	_, lista := a.get("/admin/sesiones")
	if got := sidsListados(lista); len(got) != 2 {
		t.Fatalf("la página debería listar 2 sesiones, lista %d", len(got))
	}

	// A cierra la sesión de B.
	resp, _ := a.post("/admin/sesiones/"+sidB+"/revocar", url.Values{"csrf": {a.csrf()}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revocar respondió %d", resp.StatusCode)
	}
	if b.estaAutenticado() {
		t.Error("la sesión revocada sigue funcionando")
	}
	if !a.estaAutenticado() {
		t.Error("revocar la sesión de B cerró también la de A")
	}

	// La fila quedó marcada en la base de datos.
	var revocada bool
	if err := e.Conn.QueryRow(`SELECT revocada FROM sesiones WHERE id = $1`, sidB).Scan(&revocada); err != nil || !revocada {
		t.Errorf("la fila de la sesión no quedó revocada (err %v)", err)
	}
}

func TestCerrarLaPropiaSesionDesdeLaLista(t *testing.T) {
	e := nuevoEntorno(t, true)
	a := e.cliente()
	a.entraComoAdmin()
	sid := sidDeCookie(t, a.cookieSesion())

	resp, _ := a.post("/admin/sesiones/"+sid+"/revocar", url.Values{"csrf": {a.csrf()}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/login" {
		t.Errorf("cerrar la propia sesión debería mandar a /admin/login, mandó a %q", resp.Header.Get("Location"))
	}
	if a.estaAutenticado() {
		t.Error("la sesión propia sigue activa tras cerrarla")
	}
}

func TestRevocarOtrasConservaLaActual(t *testing.T) {
	e := nuevoEntorno(t, true)
	a, b, c := e.cliente(), e.cliente(), e.cliente()
	a.entraComoAdmin()
	b.entraComoAdmin()
	c.entraComoAdmin()

	resp, _ := a.post("/admin/sesiones/revocar-otras", url.Values{"csrf": {a.csrf()}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revocar-otras respondió %d", resp.StatusCode)
	}
	if !a.estaAutenticado() {
		t.Error("revocar-otras cerró la sesión actual")
	}
	if b.estaAutenticado() || c.estaAutenticado() {
		t.Error("revocar-otras dejó vivas otras sesiones")
	}
}

func TestCerrarSesionRevocaLaCookieRobada(t *testing.T) {
	e := nuevoEntorno(t, true)
	victima := e.cliente()
	victima.entraComoAdmin()
	robada := victima.cookieSesion()

	// El atacante tiene una copia de la cookie y funciona…
	ladron := e.conCookie(robada)
	if !ladron.estaAutenticado() {
		t.Fatal("la copia de la cookie debería funcionar mientras la sesión esté viva")
	}
	// …hasta que la víctima cierra sesión.
	if resp, _ := victima.post("/admin/logout", url.Values{"csrf": {victima.csrf()}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout respondió %d", resp.StatusCode)
	}
	if e.conCookie(robada).estaAutenticado() {
		t.Error("la cookie robada sigue funcionando después de cerrar sesión (antes de este cambio duraba hasta 12 h)")
	}
}

func TestRevocarRequiereTokenCSRF(t *testing.T) {
	e := nuevoEntorno(t, true)
	a, b := e.cliente(), e.cliente()
	a.entraComoAdmin()
	b.entraComoAdmin()
	sidB := sidDeCookie(t, b.cookieSesion())

	for _, datos := range []url.Values{{}, {"csrf": {"token-falso"}}} {
		resp, _ := a.post("/admin/sesiones/"+sidB+"/revocar", datos)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("revocar con CSRF %v respondió %d, se esperaba 403", datos, resp.StatusCode)
		}
	}
	if !b.estaAutenticado() {
		t.Error("una petición sin CSRF válido logró revocar la sesión")
	}
	// Y no se puede revocar por GET (un <img src> no debe poder cerrar sesiones).
	if resp, _ := a.get("/admin/sesiones/" + sidB + "/revocar"); resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
		t.Errorf("revocar por GET respondió %d", resp.StatusCode)
	}
	if !b.estaAutenticado() {
		t.Error("un GET logró revocar la sesión")
	}
}

func TestNadieRevocaSesionesDeOtraIdentidad(t *testing.T) {
	e := nuevoEntorno(t, true)
	admin := e.cliente()
	admin.entraComoAdmin()
	sidAdmin := sidDeCookie(t, admin.cookieSesion())

	if _, err := usuarios.CreateUsuario(e.Conn, "ana", "clave-de-ana-1", usuarios.RolSuperusuario, nil); err != nil {
		t.Fatal(err)
	}
	ana := e.cliente()
	if !ana.login("ana", "clave-de-ana-1") {
		t.Fatal("ana no pudo iniciar sesión")
	}

	// La lista de ana no muestra la sesión del admin…
	_, lista := ana.get("/admin/sesiones")
	for _, sid := range sidsListados(lista) {
		if sid == sidAdmin {
			t.Error("ana ve la sesión del admin en su lista")
		}
	}
	// …y aunque conozca el id, no puede cerrarla.
	ana.post("/admin/sesiones/"+sidAdmin+"/revocar", url.Values{"csrf": {ana.csrf()}})
	ana.post("/admin/sesiones/revocar-otras", url.Values{"csrf": {ana.csrf()}})
	if !admin.estaAutenticado() {
		t.Error("un usuario logró cerrar la sesión del admin")
	}
	if !ana.estaAutenticado() {
		t.Error("ana perdió su propia sesión sin motivo")
	}
}

func TestCookiesManipuladasOAntiguasNoValen(t *testing.T) {
	e := nuevoEntorno(t, true)
	a, b := e.cliente(), e.cliente()
	a.entraComoAdmin()
	b.entraComoAdmin()
	expira := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)

	// Control: una cookie bien firmada con una sesión registrada SÍ vale; así
	// sabemos que el helper de firma es correcto y que los rechazos de abajo
	// se deben a lo que se quiere probar y no a un error de la prueba.
	if !e.conCookie(firmar(t, "admin|"+adminUsuario+"|"+expira+"|"+sidDeCookie(t, a.cookieSesion()))).estaAutenticado() {
		t.Fatal("el helper de firma no reproduce una cookie válida; la prueba no sirve")
	}

	casos := map[string]string{
		"sid cambiado sin refirmar (usar el id de otra sesión)": func() string {
			payloadEnc, sig, _ := strings.Cut(a.cookieSesion(), ".")
			raw, _ := base64.RawURLEncoding.DecodeString(payloadEnc)
			campos := strings.SplitN(string(raw), "|", 4)
			campos[3] = sidDeCookie(t, b.cookieSesion())
			return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(campos, "|"))) + "." + sig
		}(),
		"firma válida pero sid no registrado":               firmar(t, "admin|"+adminUsuario+"|"+expira+"|00000000000000000000000000000000"),
		"formato antiguo (3 campos) con firma válida":       firmar(t, "admin|"+adminUsuario+"|"+expira),
		"firma válida pero vencida":                         firmar(t, "admin|"+adminUsuario+"|1|"+sidDeCookie(t, a.cookieSesion())),
		"sid válido pero identidad distinta (otro usuario)": firmar(t, "admin|otro|"+expira+"|"+sidDeCookie(t, a.cookieSesion())),
		"sid vacío": firmar(t, "admin|"+adminUsuario+"|"+expira+"|"),
	}
	for nombre, cookie := range casos {
		if e.conCookie(cookie).estaAutenticado() {
			t.Errorf("la cookie «%s» fue aceptada", nombre)
		}
	}
}

func TestCambiarPasswordCierraTodasLasSesionesDeLaIdentidad(t *testing.T) {
	e := nuevoEntorno(t, true)
	a, b := e.cliente(), e.cliente()
	a.entraComoAdmin()
	b.entraComoAdmin()
	sidA, sidB := sidDeCookie(t, a.cookieSesion()), sidDeCookie(t, b.cookieSesion())

	nueva := "otra-clave-nueva-9"
	resp, _ := a.post("/admin/cambiar-password", url.Values{
		"csrf": {a.csrf()}, "actual": {adminClave}, "nueva": {nueva}, "confirmar": {nueva},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("cambiar-password respondió %d", resp.StatusCode)
	}
	if a.estaAutenticado() || b.estaAutenticado() {
		t.Error("tras cambiar la contraseña siguen vivas sesiones anteriores")
	}
	for _, sid := range []string{sidA, sidB} {
		var revocada bool
		if err := e.Conn.QueryRow(`SELECT revocada FROM sesiones WHERE id = $1`, sid).Scan(&revocada); err != nil || !revocada {
			t.Errorf("la sesión %s no quedó marcada como revocada (err %v)", sid[:6], err)
		}
	}
	if e.cliente().login(adminUsuario, adminClave) {
		t.Error("la contraseña vieja sigue funcionando")
	}
	if !e.cliente().login(adminUsuario, nueva) {
		t.Error("la contraseña nueva no funciona")
	}
}

func TestSesionesNoSeMuestranSinIniciarSesion(t *testing.T) {
	e := nuevoEntorno(t, true)
	c := e.cliente()
	for _, ruta := range []string{"/admin/sesiones"} {
		resp, _ := c.get(ruta)
		if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/admin/login") {
			t.Errorf("%s sin sesión respondió %d → %q", ruta, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	resp, _ := c.post("/admin/sesiones/revocar-otras", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("POST sin sesión respondió %d, se esperaba redirección al login", resp.StatusCode)
	}
}

// El admin raíz debe poder entrar aunque todavía no haya base de datos (es la
// forma de configurarla). En ese caso no hay dónde registrar la sesión.
func TestAdminEntraSinBaseDeDatos(t *testing.T) {
	e := nuevoEntorno(t, false)
	a := e.cliente()
	if !a.login(adminUsuario, adminClave) {
		t.Fatal("el admin no pudo iniciar sesión sin base de datos")
	}
	if !a.estaAutenticado() {
		t.Error("la sesión del admin no funciona sin base de datos")
	}
	if resp, _ := a.get("/admin/mantenimiento/bd"); resp.StatusCode != http.StatusOK {
		t.Errorf("la pantalla de configuración de la BD respondió %d", resp.StatusCode)
	}
	// La página de sesiones avisa en vez de fallar.
	if resp, body := a.get("/admin/sesiones"); resp.StatusCode != http.StatusOK || !strings.Contains(body, "todavía no está configurada") {
		t.Errorf("/admin/sesiones sin BD debería avisar (status %d)", resp.StatusCode)
	}
}
