package web

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"manualidades/internal/correo"
)

// esperaEntrePruebas evita que el botón de prueba se use para inundar de
// correos al servidor SMTP o a los destinatarios.
const esperaEntrePruebas = 10 * time.Second

var (
	pruebaMu      sync.Mutex
	ultimaPrueba  time.Time
	mensajeCorreo = "No se pudo guardar. Inténtalo de nuevo."
)

// formCorreo es lo que el formulario muestra. La contraseña nunca viaja de
// vuelta al navegador: TienePassword solo dice si ya hay una guardada.
type formCorreo struct {
	Servidor      string
	Puerto        int
	Seguridad     string
	Usuario       string
	DesdeNombre   string
	DesdeCorreo   string
	Destinatarios string
	TienePassword bool
}

func formDe(c correo.Config) formCorreo {
	return formCorreo{
		Servidor: c.Servidor, Puerto: c.Puerto, Seguridad: c.Seguridad, Usuario: c.Usuario,
		DesdeNombre: c.DesdeNombre, DesdeCorreo: c.DesdeCorreo,
		Destinatarios: strings.Join(c.Destinatarios, "\n"), TienePassword: c.Password != "",
	}
}

func (a *App) renderCorreo(w http.ResponseWriter, r *http.Request, f formCorreo, configurado bool, message, kind string) {
	if f.Puerto == 0 {
		f.Puerto = 587
	}
	if f.Seguridad == "" {
		f.Seguridad = correo.STARTTLS
	}
	render(w, r, "correo_config.html", map[string]any{
		"Title":        "Correo saliente",
		"Active":       "correo",
		"Form":         f,
		"Seguridades":  correo.Seguridades,
		"Configurado":  configurado,
		"MaxAvisos":    correo.MaxDestinatarios,
		"Message":      message,
		"MessageKind":  kind,
		"EsperaPrueba": int(esperaEntrePruebas.Seconds()),
	})
}

func (a *App) CorreoConfig(w http.ResponseWriter, r *http.Request) {
	c, err := correo.Load()
	if err != nil {
		log.Printf("correo: leyendo la configuración: %v", err)
		a.renderCorreo(w, r, formCorreo{}, false, "No se pudo leer la configuración guardada. Revisa el archivo smtp.json del servidor.", "error")
		return
	}
	a.renderCorreo(w, r, formDe(c), c.Configurado(), "", "")
}

func (a *App) CorreoGuardar(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulario inválido.", http.StatusBadRequest)
		return
	}
	puerto, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("puerto")))
	enviado := formCorreo{
		Servidor: r.FormValue("servidor"), Puerto: puerto, Seguridad: r.FormValue("seguridad"),
		Usuario: r.FormValue("usuario"), DesdeNombre: r.FormValue("desde_nombre"),
		DesdeCorreo: r.FormValue("desde_correo"), Destinatarios: r.FormValue("destinatarios"),
	}
	previa, _ := correo.Load()
	enviado.TienePassword = previa.Password != ""

	destinatarios, err := correo.ParseDestinatarios(enviado.Destinatarios)
	if err == nil {
		_, err = correo.Save(correo.Config{
			Servidor: enviado.Servidor, Puerto: enviado.Puerto, Seguridad: enviado.Seguridad,
			Usuario: enviado.Usuario, Password: r.FormValue("password"),
			DesdeNombre: enviado.DesdeNombre, DesdeCorreo: enviado.DesdeCorreo,
			Destinatarios: destinatarios,
		})
	}
	if err != nil {
		msg := mensajeCorreo
		if errors.Is(err, correo.ErrValidacion) {
			msg = correo.TextoValidacion(err)
		} else {
			log.Printf("correo: guardando la configuración: %v", err)
		}
		a.renderCorreo(w, r, enviado, previa.Configurado(), msg, "error")
		return
	}

	// Quedan en el registro quién y desde dónde cambió el servidor de correo
	// (nunca la contraseña).
	log.Printf("correo: configuración guardada por %q desde %s (servidor %s:%d)",
		quienCambia(r).Usuario, clientIP(r), strings.TrimSpace(enviado.Servidor), enviado.Puerto)

	c, _ := correo.Load()
	a.renderCorreo(w, r, formDe(c), c.Configurado(), "Configuración guardada. Pulsa «Enviar correo de prueba» para comprobarla.", "success")
}

// CorreoProbar manda un correo de prueba. Solo a los correos de aviso
// configurados (o, si no hay, al propio remitente): nunca a una dirección que
// llegue en la petición, para que no sirva de relé.
func (a *App) CorreoProbar(w http.ResponseWriter, r *http.Request) {
	c, err := correo.Load()
	if err != nil {
		log.Printf("correo: leyendo la configuración: %v", err)
		a.renderCorreo(w, r, formCorreo{}, false, "No se pudo leer la configuración guardada.", "error")
		return
	}
	f := formDe(c)
	if !c.Configurado() {
		a.renderCorreo(w, r, f, false, "Primero guarda la configuración del servidor.", "error")
		return
	}

	pruebaMu.Lock()
	if espera := time.Until(ultimaPrueba.Add(esperaEntrePruebas)); espera > 0 {
		pruebaMu.Unlock()
		a.renderCorreo(w, r, f, true, "Espera unos segundos antes de enviar otra prueba.", "error")
		return
	}
	ultimaPrueba = time.Now()
	pruebaMu.Unlock()

	para := c.Destinatarios
	if len(para) == 0 {
		para = []string{c.DesdeCorreo}
	}
	err = correo.Enviar(c, correo.Mensaje{
		Para:   para,
		Asunto: "Prueba de correo — Manualidades",
		Texto: "Este es un correo de prueba enviado desde el panel de administración.\n\n" +
			"Si lo estás leyendo, el servidor de correo saliente está bien configurado.",
	})
	if err != nil {
		var e *correo.ErrEnvio
		if errors.As(err, &e) {
			log.Printf("correo: la prueba falló en la etapa %s (código %d): %v", e.Etapa, e.Codigo, e.Causa)
			a.renderCorreo(w, r, f, true, "La prueba falló. "+e.Descripcion(c), "error")
			return
		}
		log.Printf("correo: la prueba falló: %v", err)
		a.renderCorreo(w, r, f, true, "No se pudo enviar el correo de prueba.", "error")
		return
	}
	a.renderCorreo(w, r, f, true, "Correo de prueba enviado a "+strings.Join(para, ", ")+". Revisa la bandeja de entrada (y la carpeta de spam).", "success")
}
