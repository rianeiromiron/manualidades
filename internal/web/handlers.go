package web

import (
	"net/http"
	"strconv"

	"manualidades/internal/config"
	"manualidades/internal/db"
	"manualidades/internal/inventario"
	"manualidades/internal/sitio"
	"manualidades/internal/tienda"
	"manualidades/internal/usuarios"
)

// ConfigDB maneja el formulario de conexión a la base de datos (Mantenimiento 1).
func (a *App) ConfigDB(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		a.renderConfigDB(w, r, cfg, "No se pudo leer config.json: "+err.Error(), "error")
		return
	}

	if r.Method == http.MethodGet {
		a.renderConfigDB(w, r, cfg, "", "")
		return
	}

	if err := r.ParseForm(); err != nil {
		a.renderConfigDB(w, r, cfg, "Formulario inválido: "+err.Error(), "error")
		return
	}

	port, err := strconv.Atoi(r.FormValue("port"))
	if err != nil {
		a.renderConfigDB(w, r, cfg, "El puerto debe ser numérico.", "error")
		return
	}

	newCfg := config.DBConfig{
		Host:     r.FormValue("host"),
		Port:     port,
		User:     r.FormValue("user"),
		Password: r.FormValue("password"),
		DBName:   r.FormValue("dbname"),
		SSLMode:  r.FormValue("sslmode"),
	}

	if err := config.Save(newCfg); err != nil {
		a.renderConfigDB(w, r, newCfg, "No se pudo guardar config.json: "+err.Error(), "error")
		return
	}

	switch r.FormValue("action") {
	case "create":
		created, err := db.EnsureDatabase(newCfg)
		switch {
		case err != nil:
			a.renderConfigDB(w, r, newCfg, "Configuración guardada, pero falló la creación: "+err.Error(), "error")
		case created:
			a.renderConfigDB(w, r, newCfg, "Base de datos \""+newCfg.DBName+"\" creada correctamente.", "success")
		default:
			a.renderConfigDB(w, r, newCfg, "La base de datos \""+newCfg.DBName+"\" ya existía. No se hicieron cambios.", "success")
		}
	default:
		if err := db.Test(newCfg); err != nil {
			a.renderConfigDB(w, r, newCfg, "Configuración guardada, pero la conexión falló: "+err.Error(), "error")
			return
		}
		a.reconnect(newCfg)
		a.renderConfigDB(w, r, newCfg, "Conexión exitosa. Configuración guardada.", "success")
	}
}

// reconnect abre un nuevo pool con la config recién guardada y lo deja
// disponible para el resto de la app (Inventario) sin reiniciar el servidor.
func (a *App) reconnect(cfg config.DBConfig) {
	conn, err := db.Open(cfg)
	if err != nil {
		return
	}
	if err := inventario.Migrate(conn); err != nil {
		conn.Close()
		return
	}
	if err := sitio.Migrate(conn); err != nil {
		conn.Close()
		return
	}
	if err := tienda.Migrate(conn); err != nil {
		conn.Close()
		return
	}
	if err := usuarios.Migrate(conn); err != nil {
		conn.Close()
		return
	}
	a.SetDB(conn)
}

func (a *App) renderConfigDB(w http.ResponseWriter, r *http.Request, cfg config.DBConfig, message, kind string) {
	render(w, r, "config_db.html", map[string]any{
		"Title":       "Base de datos",
		"Active":      "bd",
		"Config":      cfg,
		"Message":     message,
		"MessageKind": kind,
	})
}
