package e2e

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"manualidades/internal/usuarios"
)

func formCuenta(csrf string) url.Values {
	return url.Values{
		"csrf": {csrf}, "banco": {"Banco de Prueba"}, "tipo_cuenta": {"monetaria"}, "numero": {"0000-0000-1"},
		"titular": {"Manualidades Cristal"}, "moneda": {"GTQ"}, "instrucciones": {"Indica tu pedido"}, "orden": {"1"},
	}
}

func contarCuentas(t *testing.T, e *entorno) int {
	t.Helper()
	var n int
	if err := e.Conn.QueryRow(`SELECT count(*) FROM cuentas_bancarias`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCuentasAdminCreaEditaYQuedaEnElHistorial(t *testing.T) {
	e := nuevoEntorno(t, true)
	c := e.cliente()
	c.entraComoAdmin()

	resp, body := c.get("/admin/cuentas")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Todavía no hay cuentas") {
		t.Fatalf("lista vacía: %d", resp.StatusCode)
	}
	if !strings.Contains(body, `href="/admin/cuentas"`) {
		t.Error("el menú no enlaza a Cuentas bancarias")
	}
	csrf := c.csrf()

	resp, _ = c.post("/admin/cuentas", formCuenta(csrf))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("crear: %d, quiero 303", resp.StatusCode)
	}
	_, body = c.get("/admin/cuentas")
	for _, quiero := range []string{"Banco de Prueba", "0000-0000-1", "Manualidades Cristal", "Activa", "creada", ">admin<"} {
		if !strings.Contains(body, quiero) {
			t.Errorf("la lista no muestra %q", quiero)
		}
	}

	var id string
	e.Conn.QueryRow(`SELECT id::text FROM cuentas_bancarias`).Scan(&id)

	edit := formCuenta(csrf)
	edit.Set("numero", "7777-1111-2")
	if resp, _ := c.post("/admin/cuentas/"+id+"/editar", edit); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("editar: %d, quiero 303", resp.StatusCode)
	}
	_, body = c.get("/admin/cuentas")
	if !strings.Contains(body, "0000-0000-1» → «7777-1111-2") {
		t.Error("el historial no muestra el cambio de número")
	}

	c.post("/admin/cuentas/"+id+"/desactivar", url.Values{"csrf": {csrf}})
	var activa bool
	e.Conn.QueryRow(`SELECT activa FROM cuentas_bancarias`).Scan(&activa)
	if activa {
		t.Error("la cuenta debería estar inactiva")
	}
	c.post("/admin/cuentas/"+id+"/activar", url.Values{"csrf": {csrf}})
	c.post("/admin/cuentas/"+id+"/eliminar", url.Values{"csrf": {csrf}})
	if n := contarCuentas(t, e); n != 0 {
		t.Errorf("quedan %d cuentas tras eliminar", n)
	}
	_, body = c.get("/admin/cuentas")
	if !strings.Contains(body, "eliminada") || !strings.Contains(body, "7777-1111-2") {
		t.Error("el historial debe conservar la baja con los datos de la cuenta")
	}
}

func TestCuentasDatosInvalidosMuestranElMotivoYNoGuardan(t *testing.T) {
	e := nuevoEntorno(t, true)
	c := e.cliente()
	c.entraComoAdmin()
	csrf := c.csrf()

	mala := formCuenta(csrf)
	mala.Set("numero", "12;DROP TABLE")
	resp, body := c.post("/admin/cuentas", mala)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (se vuelve a mostrar el formulario)", resp.StatusCode)
	}
	if !strings.Contains(body, "solo puede llevar letras, números, espacios y guiones") {
		t.Error("no explica por qué se rechazó")
	}
	if !strings.Contains(body, `value="Banco de Prueba"`) {
		t.Error("el formulario debe conservar lo que el usuario había escrito")
	}
	if n := contarCuentas(t, e); n != 0 {
		t.Fatalf("se guardaron %d cuentas inválidas", n)
	}
	for _, prohibido := range []string{"pq:", "sql:", "violates"} {
		if strings.Contains(body, prohibido) {
			t.Errorf("la página muestra un error interno (%q)", prohibido)
		}
	}
}

func TestCuentasEscapaElHTML(t *testing.T) {
	e := nuevoEntorno(t, true)
	c := e.cliente()
	c.entraComoAdmin()
	csrf := c.csrf()

	f := formCuenta(csrf)
	f.Set("banco", `<script>alert(1)</script>`)
	f.Set("instrucciones", `"><img src=x onerror=alert(1)>`)
	c.post("/admin/cuentas", f)
	_, body := c.get("/admin/cuentas")
	if strings.Contains(body, "<script>alert(1)") || strings.Contains(body, "<img src=x") {
		t.Fatal("el nombre del banco o las instrucciones se pintaron sin escapar")
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("el banco debería verse como texto")
	}
}

func TestCuentasExigeSesionPermisoYCSRF(t *testing.T) {
	e := nuevoEntorno(t, true)

	// Sin sesión: al login.
	anonimo := e.cliente()
	if resp, _ := anonimo.get("/admin/cuentas"); resp.StatusCode != http.StatusSeeOther ||
		!strings.HasPrefix(resp.Header.Get("Location"), "/admin/login") {
		t.Fatalf("sin sesión: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Usuario administrativo sin el módulo: prohibido (ver y escribir).
	if _, err := usuarios.CreateUsuario(e.Conn, "silvia", "clave-de-silvia-1", usuarios.RolAdministrativo, nil); err != nil {
		t.Fatal(err)
	}
	s := e.cliente()
	if !s.login("silvia", "clave-de-silvia-1") {
		t.Fatal("silvia no pudo entrar")
	}
	if resp, _ := s.get("/admin/cuentas"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("sin módulo, GET: %d, quiero 403", resp.StatusCode)
	}
	csrf := s.csrf()
	if resp, _ := s.post("/admin/cuentas", formCuenta(csrf)); resp.StatusCode != http.StatusForbidden {
		t.Errorf("sin módulo, POST: %d, quiero 403", resp.StatusCode)
	}
	if n := contarCuentas(t, e); n != 0 {
		t.Fatalf("un usuario sin permiso creó %d cuentas", n)
	}
	if _, body := s.get("/admin"); strings.Contains(body, "Cuentas bancarias") {
		t.Error("el panel de quien no tiene el módulo no debe mostrarlo")
	}

	// Con el módulo asignado: puede.
	var moduloID int
	e.Conn.QueryRow(`SELECT id FROM modulos WHERE clave = 'cuentas'`).Scan(&moduloID)
	if moduloID == 0 {
		t.Fatal("el módulo 'cuentas' no se sembró")
	}
	u, _ := usuarios.GetUsuarioPorNombre(e.Conn, "silvia")
	if err := usuarios.UpdateUsuario(e.Conn, u.ID, usuarios.RolAdministrativo, []int{moduloID}, nil); err != nil {
		t.Fatal(err)
	}
	s2 := e.cliente()
	s2.login("silvia", "clave-de-silvia-1")
	if resp, _ := s2.get("/admin/cuentas"); resp.StatusCode != http.StatusOK {
		t.Fatalf("con módulo, GET: %d, quiero 200", resp.StatusCode)
	}

	// Sin token CSRF no se escribe, ni siquiera con permiso.
	sinToken := formCuenta("")
	sinToken.Del("csrf")
	if resp, _ := s2.post("/admin/cuentas", sinToken); resp.StatusCode == http.StatusSeeOther {
		t.Error("un POST sin CSRF fue aceptado")
	}
	if n := contarCuentas(t, e); n != 0 {
		t.Fatalf("un POST sin CSRF creó %d cuentas", n)
	}
	// Con token sí; y el historial dice quién fue.
	if resp, _ := s2.post("/admin/cuentas", formCuenta(s2.csrf())); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("crear con permiso: %d", resp.StatusCode)
	}
	var quien string
	e.Conn.QueryRow(`SELECT usuario FROM cuentas_bancarias_historial`).Scan(&quien)
	if quien != "silvia" {
		t.Errorf("historial.usuario = %q, quiero silvia", quien)
	}

	// Los cambios de estado no se hacen con GET.
	var id string
	e.Conn.QueryRow(`SELECT id::text FROM cuentas_bancarias`).Scan(&id)
	for _, accion := range []string{"eliminar", "activar", "desactivar"} {
		if resp, _ := s2.get("/admin/cuentas/" + id + "/" + accion); resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
			t.Errorf("GET /%s respondió %d; solo debe aceptar POST", accion, resp.StatusCode)
		}
	}
	if n := contarCuentas(t, e); n != 1 {
		t.Errorf("hay %d cuentas, quiero 1 (un GET no debe borrar)", n)
	}
}
