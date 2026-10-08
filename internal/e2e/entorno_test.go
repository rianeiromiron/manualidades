// Package e2e reúne las pruebas de punta a punta de los controles de
// seguridad: levantan el router REAL de la aplicación (web.NewRouter, el
// mismo que usa cmd/server) sobre una base de datos temporal y un admin.json
// temporal, y lo ejercitan por HTTP y con un Chrome real.
//
// Ejecutar:  go test ./internal/e2e/ -v
//
// Si no hay Postgres accesible se saltan todas; si no hay Chrome/Edge, solo se
// saltan las de navegador. Nunca tocan la base ni el admin.json reales.
package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"manualidades/internal/config"
	"manualidades/internal/db"
	"manualidades/internal/inventario"
	"manualidades/internal/sesiones"
	"manualidades/internal/sitio"
	"manualidades/internal/tienda"
	"manualidades/internal/usuarios"
	"manualidades/internal/web"
)

const (
	adminUsuario = "admin"
	adminClave   = "clave-admin-123"
)

// entorno es una instancia aislada de la aplicación: servidor, base de datos
// y directorio de trabajo propios.
type entorno struct {
	t    *testing.T
	URL  string
	Conn *sql.DB // nil si el entorno se creó sin base de datos
	App  *web.App
}

// copiarDir copia recursivamente origen en destino (solo se usa con web/, que
// es pequeño: plantillas y estáticos).
func copiarDir(t *testing.T, origen, destino string) {
	t.Helper()
	err := filepath.WalkDir(origen, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(origen, p)
		out := filepath.Join(destino, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copiando %s: %v", origen, err)
	}
}

// nuevoEntorno prepara todo. Con conBD=false no hay base de datos (el estado
// de una instalación que aún no la configuró).
func nuevoEntorno(t *testing.T, conBD bool) *entorno {
	t.Helper()

	raiz, _ := filepath.Abs("../..")
	cwdOriginal, _ := os.Getwd()

	// config.json de la raíz solo se lee para saber dónde está Postgres.
	var cfg config.DBConfig
	if conBD {
		os.Chdir(raiz)
		var err error
		cfg, err = config.Load()
		os.Chdir(cwdOriginal)
		if err != nil {
			t.Skip("sin config.json:", err)
		}
		probe, err := sql.Open("postgres", cfg.DSNMaintenance()+" connect_timeout=5")
		if err != nil {
			t.Skip("no hay Postgres accesible:", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		err = probe.PingContext(ctx)
		cancel()
		probe.Close()
		if err != nil {
			t.Skip("no hay Postgres accesible:", err)
		}
	}

	// Directorio de trabajo temporal: la aplicación lee admin.json y
	// web/templates relativos al directorio actual.
	dir := t.TempDir()
	copiarDir(t, filepath.Join(raiz, "web"), filepath.Join(dir, "web"))
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwdOriginal) })

	if err := config.CreateAdmin(adminUsuario, adminClave); err != nil {
		t.Fatalf("creando admin.json temporal: %v", err)
	}

	e := &entorno{t: t, App: &web.App{}}

	if conBD {
		cfg.DBName = fmt.Sprintf("manualidades_e2e_%d", time.Now().UnixNano())
		if _, err := db.EnsureDatabase(cfg); err != nil {
			t.Skip("no se pudo crear la base temporal:", err)
		}
		conn, err := db.Open(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, migrar := range []func(*sql.DB) error{
			inventario.Migrate, sitio.Migrate, tienda.Migrate, usuarios.Migrate, sesiones.Migrate,
		} {
			if err := migrar(conn); err != nil {
				t.Fatal(err)
			}
		}
		e.Conn = conn
		e.App.SetDB(conn)
		t.Cleanup(func() {
			conn.Close()
			admin, err := sql.Open("postgres", cfg.DSNMaintenance())
			if err != nil {
				return
			}
			defer admin.Close()
			admin.Exec(`DROP DATABASE IF EXISTS "` + cfg.DBName + `" WITH (FORCE)`)
		})
	}

	srv := httptest.NewServer(web.NewRouter(e.App))
	t.Cleanup(srv.Close)
	e.URL = srv.URL
	return e
}

// cliente es un navegador sin JavaScript: guarda cookies y NO sigue
// redirecciones, para poder comprobar a dónde manda cada respuesta.
type cliente struct {
	t    *testing.T
	base string
	http *http.Client
}

func (e *entorno) cliente() *cliente {
	jar, _ := cookiejar.New(nil)
	return &cliente{
		t:    e.t,
		base: e.URL,
		http: &http.Client{
			Jar:     jar,
			Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *cliente) hacer(req *http.Request) (*http.Response, string) {
	c.t.Helper()
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (c *cliente) get(path string) (*http.Response, string) {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	return c.hacer(req)
}

func (c *cliente) post(path string, datos url.Values) (*http.Response, string) {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.base+path, strings.NewReader(datos.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.hacer(req)
}

var reCSRF = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// csrf obtiene el token de formulario de una página de admin.
func (c *cliente) csrf() string {
	c.t.Helper()
	resp, body := c.get("/admin/sesiones")
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("no se pudo leer /admin/sesiones para el token CSRF: %d", resp.StatusCode)
	}
	m := reCSRF.FindStringSubmatch(body)
	if m == nil {
		c.t.Fatal("la página no trae token CSRF")
	}
	return m[1]
}

// login inicia sesión y devuelve si fue exitoso (redirige a /admin).
func (c *cliente) login(usuario, clave string) bool {
	c.t.Helper()
	resp, _ := c.post("/admin/login", url.Values{"usuario": {usuario}, "password": {clave}})
	return resp.StatusCode == http.StatusSeeOther && resp.Header.Get("Location") == "/admin"
}

// entra inicia sesión como el admin raíz y falla la prueba si no puede.
func (c *cliente) entraComoAdmin() {
	c.t.Helper()
	if !c.login(adminUsuario, adminClave) {
		c.t.Fatal("no se pudo iniciar sesión como admin")
	}
}

// cookieSesion devuelve el valor crudo de la cookie de sesión del cliente.
func (c *cliente) cookieSesion() string {
	u, _ := url.Parse(c.base + "/admin")
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "admin_session" {
			return ck.Value
		}
	}
	return ""
}

// estaAutenticado dice si /admin responde 200 (sesión válida) o redirige al
// login (sesión inválida).
func (c *cliente) estaAutenticado() bool {
	c.t.Helper()
	resp, _ := c.get("/admin")
	switch resp.StatusCode {
	case http.StatusOK:
		return true
	case http.StatusSeeOther:
		if strings.HasPrefix(resp.Header.Get("Location"), "/admin/login") {
			return false
		}
	}
	c.t.Fatalf("/admin respondió inesperadamente %d (Location %q)", resp.StatusCode, resp.Header.Get("Location"))
	return false
}

// conCookie crea un cliente nuevo que presenta exactamente esa cookie de
// sesión, como lo haría quien la robó.
func (e *entorno) conCookie(valor string) *cliente {
	c := e.cliente()
	u, _ := url.Parse(e.URL + "/admin")
	c.http.Jar.SetCookies(u, []*http.Cookie{{Name: "admin_session", Value: valor, Path: "/admin"}})
	return c
}
