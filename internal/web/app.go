package web

import (
	"database/sql"
	"html/template"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"manualidades/internal/storage"
)

// localTime formatea una hora en la hora local del servidor. Las columnas
// TIMESTAMPTZ de Postgres viajan como UTC hasta Go (lib/pq no las convierte),
// y time.Time.Format no cambia de zona por sí solo: sin este paso, todas las
// horas mostradas en el admin salían en UTC en vez de la hora local del
// negocio. Acepta time.Time o *time.Time (nil da cadena vacía) para que las
// plantillas puedan formatear ExpiraEn sin un {{if}} aparte.
func localTime(t any, layout string) string {
	switch v := t.(type) {
	case time.Time:
		return v.Local().Format(layout)
	case *time.Time:
		if v == nil {
			return ""
		}
		return v.Local().Format(layout)
	default:
		return ""
	}
}

var funcMap = template.FuncMap{"localtime": localTime}

// parseTemplates parsea uno o más archivos con funcMap ya registrado
// (html/template exige que las funciones existan antes del parseo). El
// nombre raíz es el del primer archivo, igual que hace template.ParseFiles
// por su cuenta, para no alterar cómo Execute()/ExecuteTemplate() encuentran
// la plantilla en cada llamador.
func parseTemplates(files ...string) *template.Template {
	return template.Must(template.New(filepath.Base(files[0])).Funcs(funcMap).ParseFiles(files...))
}

// App agrupa las dependencias compartidas por los handlers: la conexión a
// la base de datos (puede cambiar en caliente si se reconfigura desde
// Mantenimiento 1) y los backends de almacenamiento de fotos de producto y
// del logo del sitio.
type App struct {
	mu          sync.RWMutex
	conn        *sql.DB
	Storage     storage.Storage
	LogoStorage storage.Storage
}

func (a *App) DB() *sql.DB {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.conn
}

// SetDB reemplaza el pool de conexiones activo, cerrando el anterior si
// existía. nil es válido: significa "todavía no hay base configurada".
func (a *App) SetDB(conn *sql.DB) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn != nil {
		a.conn.Close()
	}
	a.conn = conn
}

// render arma la página de admin con el layout completo (nav incluido).
// Además de renderizar, calcula qué módulos puede ver la sesión actual
// (CurrentRol/ModulosVisibles) e inyecta esa información en data, para que
// layout.html y home.html filtren el nav y las tarjetas sin que cada
// handler tenga que hacerlo a mano.
func render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	injectNav(r, data)
	tmpl := parseTemplates("web/templates/layout.html", "web/templates/"+page)
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "error interno: "+err.Error(), http.StatusInternalServerError)
	}
}

func injectNav(r *http.Request, data map[string]any) {
	sesion, ok := sesionFromContext(r)
	if !ok {
		return
	}
	rolActual := "admin"
	if sesion.Kind == "user" {
		rolActual = sesion.Rol
	}
	data["CurrentRol"] = rolActual
	data["CurrentUsuario"] = sesion.Usuario

	visibles := make(map[string]bool, len(todosLosModulos))
	for _, clave := range todosLosModulos {
		visibles[clave] = sesion.TieneAcceso(clave)
	}
	data["ModulosVisibles"] = visibles
}

// renderAuth envuelve con el layout minimalista (sin menú) usado por las
// pantallas de login y configuración inicial de acceso.
func renderAuth(w http.ResponseWriter, page string, data map[string]any) {
	tmpl := parseTemplates("web/templates/layout_auth.html", "web/templates/"+page)
	if err := tmpl.ExecuteTemplate(w, "layout_auth", data); err != nil {
		http.Error(w, "error interno: "+err.Error(), http.StatusInternalServerError)
	}
}

// renderFragment renderiza una plantilla suelta, sin el layout completo:
// se usa para pedazos de HTML que se inyectan por fetch() en la misma
// página (ej. el historial de movimientos de un producto).
func renderFragment(w http.ResponseWriter, page string, data map[string]any) {
	tmpl := parseTemplates("web/templates/" + page)
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "error interno: "+err.Error(), http.StatusInternalServerError)
	}
}

// requireDB devuelve la conexión activa, o renderiza una página de aviso y
// devuelve nil si el inventario todavía no tiene base de datos configurada.
func (a *App) requireDB(w http.ResponseWriter, r *http.Request, active string) *sql.DB {
	conn := a.DB()
	if conn == nil {
		render(w, r, "sin_bd.html", map[string]any{
			"Title":  "Inventario",
			"Active": active,
		})
	}
	return conn
}

// Home muestra el panel principal con acceso a los mantenimientos.
func (a *App) Home(w http.ResponseWriter, r *http.Request) {
	render(w, r, "home.html", map[string]any{
		"Title":  "Panel",
		"Active": "home",
	})
}

// InventarioHome muestra los tres accesos del Mantenimiento 2.
func (a *App) InventarioHome(w http.ResponseWriter, r *http.Request) {
	render(w, r, "inventario_home.html", map[string]any{
		"Title":  "Inventario",
		"Active": "inventario",
	})
}
