package web

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"manualidades/internal/inventario"
)

func (a *App) CategoriasList(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "inventario")
	if conn == nil {
		return
	}

	cats, err := inventario.ListCategorias(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	render(w, r, "categorias_list.html", map[string]any{
		"Title":      "Categorías",
		"Active":     "inventario",
		"Categorias": cats,
	})
}

func (a *App) CategoriaCrear(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "inventario")
	if conn == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := inventario.CreateCategoria(conn, r.FormValue("nombre"), r.FormValue("descripcion")); err != nil {
		http.Error(w, "no se pudo crear la categoría: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/mantenimiento/inventario/categorias", http.StatusSeeOther)
}

func (a *App) CategoriaEditar(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "inventario")
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
		if err := inventario.UpdateCategoria(conn, id, r.FormValue("nombre"), r.FormValue("descripcion")); err != nil {
			http.Error(w, "no se pudo actualizar: "+err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/admin/mantenimiento/inventario/categorias", http.StatusSeeOther)
		return
	}

	cat, err := inventario.GetCategoria(conn, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	render(w, r, "categoria_form.html", map[string]any{
		"Title":     "Editar categoría",
		"Active":    "inventario",
		"Categoria": cat,
	})
}

func (a *App) CategoriaEliminar(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "inventario")
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := inventario.DeleteCategoria(conn, id); err != nil {
		http.Error(w, "no se pudo eliminar (¿tiene productos asociados?): "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/mantenimiento/inventario/categorias", http.StatusSeeOther)
}
