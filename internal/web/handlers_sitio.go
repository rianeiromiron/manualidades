package web

import (
	"database/sql"
	"net/http"

	"manualidades/internal/sitio"
)

// SitioConfig maneja el Mantenimiento 3: identidad del negocio, redes
// sociales, logo y los 4 colores base de la tienda pública.
func (a *App) SitioConfig(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "sitio")
	if conn == nil {
		return
	}

	if r.Method == http.MethodGet {
		a.renderSitioConfig(w, r, conn, "", "")
		return
	}

	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		a.renderSitioConfig(w, r, conn, "Formulario inválido: "+err.Error(), "error")
		return
	}

	cfg := sitio.Config{
		NombreNegocio:  r.FormValue("nombre_negocio"),
		Direccion:      r.FormValue("direccion"),
		Telefono:       r.FormValue("telefono"),
		Email:          r.FormValue("email"),
		FacebookURL:    r.FormValue("facebook_url"),
		InstagramURL:   r.FormValue("instagram_url"),
		WhatsappNumero: r.FormValue("whatsapp_numero"),
		ColorFondo:     r.FormValue("color_fondo"),
		ColorTexto:     r.FormValue("color_texto"),
		ColorMarco:     r.FormValue("color_marco"),
		ColorAcento:    r.FormValue("color_acento"),
	}

	if err := sitio.Save(conn, cfg); err != nil {
		a.renderSitioConfig(w, r, conn, "No se pudo guardar: "+err.Error(), "error")
		return
	}

	if err := a.guardarLogo(conn, r); err != nil {
		a.renderSitioConfig(w, r, conn, "Los datos se guardaron, pero el logo no se pudo subir: "+err.Error(), "error")
		return
	}

	a.renderSitioConfig(w, r, conn, "Configuración guardada.", "success")
}

func (a *App) guardarLogo(conn *sql.DB, r *http.Request) error {
	if r.MultipartForm == nil {
		return nil
	}
	files := r.MultipartForm.File["logo"]
	if len(files) == 0 {
		return nil
	}
	fh := files[0]
	file, err := fh.Open()
	if err != nil {
		return err
	}
	defer file.Close()

	previo, _ := sitio.Get(conn)

	ruta, err := a.LogoStorage.Save(fh.Filename, file)
	if err != nil {
		return err
	}
	if err := sitio.SaveLogo(conn, ruta); err != nil {
		return err
	}
	if previo.LogoRuta != "" {
		a.LogoStorage.Delete(previo.LogoRuta)
	}
	return nil
}

func (a *App) renderSitioConfig(w http.ResponseWriter, r *http.Request, conn *sql.DB, message, kind string) {
	cfg, err := sitio.Get(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logoURL := ""
	if cfg.LogoRuta != "" {
		logoURL = a.LogoStorage.URL(cfg.LogoRuta)
	}

	render(w, r, "sitio_config.html", map[string]any{
		"Title":       "Sitio web",
		"Active":      "sitio",
		"Config":      cfg,
		"LogoURL":     logoURL,
		"Message":     message,
		"MessageKind": kind,
	})
}
