package e2e

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"manualidades/internal/usuarios"
)

const claveSecretaSMTP = "clave-secreta-smtp-0123"

func formCorreo(csrf string, puerto int) url.Values {
	return url.Values{
		"csrf": {csrf}, "servidor": {"127.0.0.1"}, "puerto": {strconv.Itoa(puerto)}, "seguridad": {"starttls"},
		"usuario": {"tienda@ejemplo.com"}, "password": {claveSecretaSMTP},
		"desde_nombre": {"Manualidades Cristal"}, "desde_correo": {"tienda@ejemplo.com"},
		"destinatarios": {"dueña@ejemplo.com\nbodega@ejemplo.com"},
	}
}

func puertoCerrado(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

func TestCorreoGuardaSinMostrarLaContrasena(t *testing.T) {
	e := nuevoEntorno(t, true)
	c := e.cliente()
	c.entraComoAdmin()

	resp, body := c.get("/admin/correo")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Sin configurar") {
		t.Fatalf("pantalla inicial: %d", resp.StatusCode)
	}
	if !strings.Contains(body, `href="/admin/correo"`) {
		t.Error("el menú no enlaza a Correo")
	}
	csrf := c.csrf()
	puerto := puertoCerrado(t)

	// Antes de guardar no hay nada que probar.
	if _, body := c.post("/admin/correo/probar", url.Values{"csrf": {csrf}}); !strings.Contains(body, "Primero guarda la configuración") {
		t.Error("probar sin configuración debe pedir guardarla primero")
	}

	resp, body = c.post("/admin/correo", formCorreo(csrf, puerto))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Configuración guardada") || !strings.Contains(body, "Configurado") {
		t.Fatalf("guardar: %d", resp.StatusCode)
	}
	if strings.Contains(body, claveSecretaSMTP) {
		t.Fatal("la página devolvió la contraseña SMTP al navegador")
	}
	if !strings.Contains(body, "ya hay una guardada") {
		t.Error("debe avisar que ya hay una contraseña guardada")
	}
	_, body = c.get("/admin/correo")
	if strings.Contains(body, claveSecretaSMTP) {
		t.Fatal("la pantalla muestra la contraseña SMTP")
	}
	if !strings.Contains(body, "bodega@ejemplo.com") {
		t.Error("los correos de aviso deben mostrarse")
	}
	// El archivo quedó en el servidor con la contraseña, y solo ahí.
	datos, err := os.ReadFile("smtp.json")
	if err != nil || !strings.Contains(string(datos), claveSecretaSMTP) {
		t.Fatalf("smtp.json no se escribió bien: %v", err)
	}

	// Guardar con la contraseña en blanco la conserva.
	f := formCorreo(csrf, puerto)
	f.Set("password", "")
	f.Set("desde_nombre", "Otro Nombre")
	c.post("/admin/correo", f)
	datos, _ = os.ReadFile("smtp.json")
	if !strings.Contains(string(datos), claveSecretaSMTP) || !strings.Contains(string(datos), "Otro Nombre") {
		t.Error("al dejar la contraseña en blanco debía conservarse la anterior")
	}

	// Cambiar de servidor sin escribir la contraseña se rechaza.
	f.Set("servidor", "smtp.otro.com")
	_, body = c.post("/admin/correo", f)
	if !strings.Contains(body, "escribe también la contraseña") {
		t.Error("cambiar de servidor sin contraseña debe rechazarse")
	}
	datos, _ = os.ReadFile("smtp.json")
	if strings.Contains(string(datos), "smtp.otro.com") {
		t.Error("el cambio rechazado quedó guardado")
	}

	// Datos inválidos: se explica y no se guarda.
	mal := formCorreo(csrf, puerto)
	mal.Set("desde_correo", "no-es-un-correo")
	_, body = c.post("/admin/correo", mal)
	if !strings.Contains(body, "no es una dirección de correo válida") {
		t.Error("no explica el correo inválido")
	}
	mal = formCorreo(csrf, puerto)
	mal.Set("destinatarios", "bien@x.com, mal")
	if _, body = c.post("/admin/correo", mal); !strings.Contains(body, "«mal» no es una dirección") {
		t.Error("no señala cuál aviso es inválido")
	}

	// La prueba contra un servidor que no responde falla con una explicación,
	// sin exponer la contraseña ni errores internos; y una segunda seguida se frena.
	_, body = c.post("/admin/correo/probar", url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "La prueba falló") || !strings.Contains(body, "No se pudo conectar con 127.0.0.1") {
		t.Errorf("la prueba fallida no se explica bien")
	}
	if strings.Contains(body, claveSecretaSMTP) || strings.Contains(body, "connectex") || strings.Contains(body, "dial tcp") {
		t.Error("la página muestra detalles internos o la contraseña")
	}
	if _, body = c.post("/admin/correo/probar", url.Values{"csrf": {csrf}}); !strings.Contains(body, "Espera unos segundos") {
		t.Error("una segunda prueba inmediata debe frenarse")
	}
}

func TestCorreoExigeSesionPermisoYCSRF(t *testing.T) {
	e := nuevoEntorno(t, true)

	if resp, _ := e.cliente().get("/admin/correo"); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("sin sesión: %d, quiero 303 al login", resp.StatusCode)
	}

	if _, err := usuarios.CreateUsuario(e.Conn, "silvia", "clave-de-silvia-1", usuarios.RolAdministrativo, nil); err != nil {
		t.Fatal(err)
	}
	s := e.cliente()
	s.login("silvia", "clave-de-silvia-1")
	if resp, _ := s.get("/admin/correo"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("sin módulo, GET: %d, quiero 403", resp.StatusCode)
	}
	csrf := s.csrf()
	if resp, _ := s.post("/admin/correo", formCorreo(csrf, 587)); resp.StatusCode != http.StatusForbidden {
		t.Errorf("sin módulo, POST: %d, quiero 403", resp.StatusCode)
	}
	if resp, _ := s.post("/admin/correo/probar", url.Values{"csrf": {csrf}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("sin módulo, probar: %d, quiero 403", resp.StatusCode)
	}
	if _, err := os.Stat("smtp.json"); err == nil {
		t.Fatal("un usuario sin permiso escribió smtp.json")
	}

	var moduloID int
	e.Conn.QueryRow(`SELECT id FROM modulos WHERE clave = 'correo'`).Scan(&moduloID)
	if moduloID == 0 {
		t.Fatal("el módulo 'correo' no se sembró")
	}
	u, _ := usuarios.GetUsuarioPorNombre(e.Conn, "silvia")
	usuarios.UpdateUsuario(e.Conn, u.ID, usuarios.RolAdministrativo, []int{moduloID}, nil)
	s2 := e.cliente()
	s2.login("silvia", "clave-de-silvia-1")
	if resp, _ := s2.get("/admin/correo"); resp.StatusCode != http.StatusOK {
		t.Fatalf("con módulo, GET: %d", resp.StatusCode)
	}

	sinToken := formCorreo("", 587)
	sinToken.Del("csrf")
	if resp, _ := s2.post("/admin/correo", sinToken); resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
		t.Errorf("POST sin CSRF respondió %d", resp.StatusCode)
	}
	if _, err := os.Stat("smtp.json"); err == nil {
		t.Fatal("un POST sin CSRF escribió smtp.json")
	}
}
