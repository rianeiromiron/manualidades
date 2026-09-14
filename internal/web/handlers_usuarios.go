package web

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"manualidades/internal/usuarios"
)

// UsuariosList muestra el mantenimiento de usuarios: exclusivo del
// usuario admin (ver RequireOnlyAdmin en cmd/server/main.go).
func (a *App) UsuariosList(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "usuarios")
	if conn == nil {
		return
	}

	lista, err := usuarios.ListUsuarios(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	modulos, err := usuarios.ListModulos(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Para pintar los chips de módulos asignados en la tabla sin volver a
	// consultar por cada fila, se arma un mapa usuarioID -> nombres.
	nombresPorModulo := make(map[int]string, len(modulos))
	for _, m := range modulos {
		nombresPorModulo[m.ID] = m.Nombre
	}
	// No se usa embedding de usuarios.Usuario aquí a propósito: ese tipo
	// tiene un campo también llamado "Usuario" (el nombre de usuario), y
	// {{.Usuario}} en la plantilla se resolvería al struct embebido
	// entero en vez de a ese campo — de ahí campos explícitos.
	type filaUsuario struct {
		ID             int
		Usuario        string
		Rol            string
		Activo         bool
		ModulosNombres []string
	}
	filas := make([]filaUsuario, 0, len(lista))
	for _, u := range lista {
		var nombres []string
		if u.Rol == usuarios.RolAdministrativo {
			ids, err := usuarios.ModulosAsignados(conn, u.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			for _, id := range ids {
				nombres = append(nombres, nombresPorModulo[id])
			}
		}
		filas = append(filas, filaUsuario{ID: u.ID, Usuario: u.Usuario, Rol: u.Rol, Activo: u.Activo, ModulosNombres: nombres})
	}

	render(w, r, "usuarios_list.html", map[string]any{
		"Title":    "Usuarios",
		"Active":   "usuarios",
		"Usuarios": filas,
	})
}

func (a *App) UsuarioNuevo(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "usuarios")
	if conn == nil {
		return
	}

	if r.Method == http.MethodGet {
		a.renderUsuarioForm(w, r, conn, usuarios.Usuario{Rol: usuarios.RolAdministrativo}, nil, "")
		return
	}

	if err := r.ParseForm(); err != nil {
		a.renderUsuarioForm(w, r, conn, usuarios.Usuario{}, nil, "Formulario inválido.")
		return
	}

	usuario := r.FormValue("usuario")
	password := r.FormValue("password")
	confirmar := r.FormValue("confirmar")
	rol := r.FormValue("rol")
	moduloIDs := parseModuloIDs(r)

	switch {
	case usuario == "" || password == "":
		a.renderUsuarioForm(w, r, conn, usuarios.Usuario{Usuario: usuario, Rol: rol}, moduloIDs, "Usuario y contraseña son obligatorios.")
		return
	case len(password) < 8:
		a.renderUsuarioForm(w, r, conn, usuarios.Usuario{Usuario: usuario, Rol: rol}, moduloIDs, "La contraseña debe tener al menos 8 caracteres.")
		return
	case password != confirmar:
		a.renderUsuarioForm(w, r, conn, usuarios.Usuario{Usuario: usuario, Rol: rol}, moduloIDs, "Las contraseñas no coinciden.")
		return
	}

	if _, err := usuarios.CreateUsuario(conn, usuario, password, rol, moduloIDs); err != nil {
		a.renderUsuarioForm(w, r, conn, usuarios.Usuario{Usuario: usuario, Rol: rol}, moduloIDs, "No se pudo crear el usuario: "+err.Error())
		return
	}

	http.Redirect(w, r, "/admin/usuarios", http.StatusSeeOther)
}

func (a *App) UsuarioEditar(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "usuarios")
	if conn == nil {
		return
	}

	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rol := r.FormValue("rol")
		moduloIDs := parseModuloIDs(r)
		nueva := r.FormValue("password")
		confirmar := r.FormValue("confirmar")

		var nuevaPassword *string
		if nueva != "" {
			if len(nueva) < 8 {
				u, _ := usuarios.GetUsuario(conn, id)
				a.renderUsuarioForm(w, r, conn, u, moduloIDs, "La contraseña debe tener al menos 8 caracteres.")
				return
			}
			if nueva != confirmar {
				u, _ := usuarios.GetUsuario(conn, id)
				a.renderUsuarioForm(w, r, conn, u, moduloIDs, "Las contraseñas no coinciden.")
				return
			}
			nuevaPassword = &nueva
		}

		if err := usuarios.UpdateUsuario(conn, id, rol, moduloIDs, nuevaPassword); err != nil {
			u, _ := usuarios.GetUsuario(conn, id)
			a.renderUsuarioForm(w, r, conn, u, moduloIDs, "No se pudo guardar: "+err.Error())
			return
		}

		http.Redirect(w, r, "/admin/usuarios", http.StatusSeeOther)
		return
	}

	u, err := usuarios.GetUsuario(conn, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	moduloIDs, err := usuarios.ModulosAsignados(conn, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.renderUsuarioForm(w, r, conn, u, moduloIDs, "")
}

func (a *App) UsuarioActivar(w http.ResponseWriter, r *http.Request) {
	a.usuarioSetActivo(w, r, true)
}

func (a *App) UsuarioDesactivar(w http.ResponseWriter, r *http.Request) {
	a.usuarioSetActivo(w, r, false)
}

func (a *App) usuarioSetActivo(w http.ResponseWriter, r *http.Request, activo bool) {
	conn := a.requireDB(w, r, "usuarios")
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := usuarios.SetActivo(conn, id, activo); err != nil {
		http.Error(w, "no se pudo actualizar: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/usuarios", http.StatusSeeOther)
}

func (a *App) UsuarioEliminar(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "usuarios")
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := usuarios.DeleteUsuario(conn, id); err != nil {
		http.Error(w, "no se pudo eliminar: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/usuarios", http.StatusSeeOther)
}

// parseModuloIDs lee el checklist "modulos" del formulario (nombres de
// campo repetidos, uno por checkbox marcado).
func parseModuloIDs(r *http.Request) []int {
	valores := r.Form["modulos"]
	ids := make([]int, 0, len(valores))
	for _, v := range valores {
		if id, err := strconv.Atoi(v); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func (a *App) renderUsuarioForm(w http.ResponseWriter, r *http.Request, conn *sql.DB, u usuarios.Usuario, moduloIDsAsignados []int, message string) {
	modulos, err := usuarios.ListModulos(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	asignados := make(map[int]bool, len(moduloIDsAsignados))
	for _, id := range moduloIDsAsignados {
		asignados[id] = true
	}
	type moduloVista struct {
		usuarios.Modulo
		Asignado bool
	}
	vista := make([]moduloVista, 0, len(modulos))
	for _, m := range modulos {
		vista = append(vista, moduloVista{Modulo: m, Asignado: asignados[m.ID]})
	}

	formAction := "/admin/usuarios/nuevo"
	if u.ID != 0 {
		formAction = "/admin/usuarios/" + strconv.Itoa(u.ID) + "/editar"
	}

	render(w, r, "usuario_form.html", map[string]any{
		"Title":      "Usuario",
		"Active":     "usuarios",
		"Usuario":    u,
		"Modulos":    vista,
		"Message":    message,
		"EsNuevo":    u.ID == 0,
		"FormAction": formAction,
	})
}
