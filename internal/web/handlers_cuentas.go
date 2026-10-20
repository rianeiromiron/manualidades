package web

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"manualidades/internal/cuentas"
)

const (
	rutaCuentas      = "/admin/cuentas"
	limiteHistorial  = 50
	mensajeCuentaErr = "No se pudo guardar el cambio. Inténtalo de nuevo."
)

// quienCambia identifica en el historial a la sesión que hace el cambio.
func quienCambia(r *http.Request) cuentas.Quien {
	q := cuentas.Quien{IP: clientIP(r)}
	if s, ok := sesionFromContext(r); ok {
		q.Usuario = s.Usuario
	}
	return q
}

// datosCuenta lee el formulario de alta o edición.
func datosCuenta(r *http.Request) cuentas.Datos {
	orden, _ := strconv.Atoi(r.FormValue("orden"))
	return cuentas.Datos{
		Banco:         r.FormValue("banco"),
		TipoCuenta:    r.FormValue("tipo_cuenta"),
		Numero:        r.FormValue("numero"),
		Titular:       r.FormValue("titular"),
		Moneda:        r.FormValue("moneda"),
		Instrucciones: r.FormValue("instrucciones"),
		Orden:         orden,
	}
}

// mensajeDeError traduce un error de cuentas a algo que se pueda mostrar: los
// de validación van tal cual; los internos se registran y se muestran en
// genérico (nunca el texto de la base de datos).
func mensajeDeError(err error, contexto string) string {
	switch {
	case errors.Is(err, cuentas.ErrValidacion):
		return cuentas.Mensaje(err)
	case errors.Is(err, cuentas.ErrNoExiste):
		return "Esa cuenta ya no existe."
	}
	log.Printf("cuentas: %s: %v", contexto, err)
	return mensajeCuentaErr
}

func (a *App) renderCuentas(w http.ResponseWriter, r *http.Request, conn *sql.DB, form cuentas.Datos, message, kind string) {
	lista, err := cuentas.List(conn)
	if err != nil {
		log.Printf("cuentas: listando: %v", err)
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return
	}
	historial, err := cuentas.Historial(conn, limiteHistorial)
	if err != nil {
		log.Printf("cuentas: leyendo el historial: %v", err)
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return
	}
	if form.TipoCuenta == "" {
		form.TipoCuenta = cuentas.Tipos[0].Valor
	}
	if form.Moneda == "" {
		form.Moneda = cuentas.Monedas[0].Valor
	}
	render(w, r, "cuentas_list.html", map[string]any{
		"Title":       "Cuentas bancarias",
		"Active":      "cuentas",
		"Cuentas":     lista,
		"Historial":   historial,
		"Form":        form,
		"Tipos":       cuentas.Tipos,
		"Monedas":     cuentas.Monedas,
		"Message":     message,
		"MessageKind": kind,
	})
}

func (a *App) CuentasList(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "cuentas")
	if conn == nil {
		return
	}
	a.renderCuentas(w, r, conn, cuentas.Datos{}, "", "")
}

func (a *App) CuentaCrear(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "cuentas")
	if conn == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulario inválido.", http.StatusBadRequest)
		return
	}
	d := datosCuenta(r)
	if _, err := cuentas.Create(conn, d, quienCambia(r)); err != nil {
		a.renderCuentas(w, r, conn, d, mensajeDeError(err, "creando"), "error")
		return
	}
	http.Redirect(w, r, rutaCuentas, http.StatusSeeOther)
}

func (a *App) CuentaEditar(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "cuentas")
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderForm := func(c cuentas.Cuenta, message string) {
		render(w, r, "cuenta_form.html", map[string]any{
			"Title":       "Editar cuenta bancaria",
			"Active":      "cuentas",
			"Cuenta":      c,
			"Tipos":       cuentas.Tipos,
			"Monedas":     cuentas.Monedas,
			"Message":     message,
			"MessageKind": "error",
		})
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulario inválido.", http.StatusBadRequest)
			return
		}
		d := datosCuenta(r)
		if err := cuentas.Update(conn, id, d, quienCambia(r)); err != nil {
			if errors.Is(err, cuentas.ErrNoExiste) {
				http.NotFound(w, r)
				return
			}
			// Se vuelve a mostrar el formulario con lo que el usuario escribió.
			renderForm(cuentas.Cuenta{
				ID: id, Banco: d.Banco, TipoCuenta: d.TipoCuenta, Numero: d.Numero, Titular: d.Titular,
				Moneda: d.Moneda, Instrucciones: d.Instrucciones, Orden: d.Orden,
			}, mensajeDeError(err, "editando"))
			return
		}
		http.Redirect(w, r, rutaCuentas, http.StatusSeeOther)
		return
	}

	c, err := cuentas.Get(conn, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	renderForm(c, "")
}

// cambiarEstadoCuenta atiende los tres botones de la fila: activar,
// desactivar y eliminar.
func (a *App) cambiarEstadoCuenta(w http.ResponseWriter, r *http.Request, hacer func(*sql.DB, int, cuentas.Quien) error) {
	conn := a.requireDB(w, r, "cuentas")
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := hacer(conn, id, quienCambia(r)); err != nil {
		a.renderCuentas(w, r, conn, cuentas.Datos{}, mensajeDeError(err, "cambiando estado"), "error")
		return
	}
	http.Redirect(w, r, rutaCuentas, http.StatusSeeOther)
}

func (a *App) CuentaActivar(w http.ResponseWriter, r *http.Request) {
	a.cambiarEstadoCuenta(w, r, func(c *sql.DB, id int, q cuentas.Quien) error { return cuentas.SetActiva(c, id, true, q) })
}

func (a *App) CuentaDesactivar(w http.ResponseWriter, r *http.Request) {
	a.cambiarEstadoCuenta(w, r, func(c *sql.DB, id int, q cuentas.Quien) error { return cuentas.SetActiva(c, id, false, q) })
}

func (a *App) CuentaEliminar(w http.ResponseWriter, r *http.Request) {
	a.cambiarEstadoCuenta(w, r, cuentas.Delete)
}
