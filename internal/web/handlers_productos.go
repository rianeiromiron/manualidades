package web

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"manualidades/internal/inventario"
)

const maxUploadBytes = 20 << 20 // 20 MB por request

func (a *App) ProductosList(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "inventario")
	if conn == nil {
		return
	}
	productos, err := inventario.ListProductos(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	categorias, err := inventario.ListCategorias(conn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, "productos_list.html", map[string]any{
		"Title":      "Productos",
		"Active":     "inventario",
		"Productos":  productos,
		"Categorias": categorias,
	})
}

func (a *App) ProductoNuevo(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "inventario")
	if conn == nil {
		return
	}

	if r.Method == http.MethodGet {
		a.renderProductoForm(w, r, conn, inventario.Producto{Activo: true}, nil, "")
		return
	}

	p, err := a.parseProductoForm(r)
	if err != nil {
		a.renderProductoForm(w, r, conn, p, nil, err.Error())
		return
	}

	id, err := inventario.CreateProducto(conn, p)
	if err != nil {
		a.renderProductoForm(w, r, conn, p, nil, "No se pudo crear el producto: "+err.Error())
		return
	}

	if err := a.guardarFotos(conn, r, id); err != nil {
		http.Redirect(w, r, "/admin/mantenimiento/inventario/productos/"+strconv.Itoa(id)+"/editar?fotoError=1", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/admin/mantenimiento/inventario/productos", http.StatusSeeOther)
}

func (a *App) ProductoEditar(w http.ResponseWriter, r *http.Request) {
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
		p, err := a.parseProductoForm(r)
		p.ID = id
		if err != nil {
			fotos, _ := inventario.ListFotos(conn, id)
			a.renderProductoForm(w, r, conn, p, fotos, err.Error())
			return
		}

		if err := inventario.UpdateProducto(conn, p); err != nil {
			fotos, _ := inventario.ListFotos(conn, id)
			a.renderProductoForm(w, r, conn, p, fotos, "No se pudo guardar: "+err.Error())
			return
		}

		if err := a.guardarFotos(conn, r, id); err != nil {
			http.Redirect(w, r, "/admin/mantenimiento/inventario/productos/"+strconv.Itoa(id)+"/editar?fotoError=1", http.StatusSeeOther)
			return
		}

		http.Redirect(w, r, "/admin/mantenimiento/inventario/productos", http.StatusSeeOther)
		return
	}

	p, err := inventario.GetProducto(conn, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fotos, _ := inventario.ListFotos(conn, id)

	msg := ""
	if r.URL.Query().Get("fotoError") != "" {
		msg = "El producto se guardó, pero una o más fotos no se pudieron subir."
	}
	a.renderProductoForm(w, r, conn, p, fotos, msg)
}

func (a *App) ProductoEliminar(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "inventario")
	if conn == nil {
		return
	}
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.NotFound(w, r)
		return
	}

	fotos, _ := inventario.ListFotos(conn, id)

	if err := inventario.DeleteProducto(conn, id); err != nil {
		http.Error(w, "no se pudo eliminar (¿tiene movimientos registrados?): "+err.Error(), http.StatusBadRequest)
		return
	}

	for _, f := range fotos {
		a.Storage.Delete(f.Ruta)
	}

	http.Redirect(w, r, "/admin/mantenimiento/inventario/productos", http.StatusSeeOther)
}

func (a *App) FotoEliminar(w http.ResponseWriter, r *http.Request) {
	conn := a.requireDB(w, r, "inventario")
	if conn == nil {
		return
	}
	vars := mux.Vars(r)
	productoID := vars["id"]
	fotoID, err := strconv.Atoi(vars["fotoId"])
	if err != nil {
		http.NotFound(w, r)
		return
	}

	ruta, err := inventario.DeleteFoto(conn, fotoID)
	if err == nil {
		a.Storage.Delete(ruta)
	}

	http.Redirect(w, r, "/admin/mantenimiento/inventario/productos/"+productoID+"/editar", http.StatusSeeOther)
}

func (a *App) parseProductoForm(r *http.Request) (inventario.Producto, error) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		return inventario.Producto{}, err
	}

	categoriaID, err := strconv.Atoi(r.FormValue("categoria_id"))
	if err != nil {
		return inventario.Producto{}, err
	}
	precioCompra, err := strconv.ParseFloat(r.FormValue("precio_compra"), 64)
	if err != nil {
		return inventario.Producto{}, err
	}
	precioVenta, err := strconv.ParseFloat(r.FormValue("precio_venta"), 64)
	if err != nil {
		return inventario.Producto{}, err
	}

	return inventario.Producto{
		CategoriaID:  categoriaID,
		Nombre:       r.FormValue("nombre"),
		Descripcion:  r.FormValue("descripcion"),
		PrecioCompra: precioCompra,
		PrecioVenta:  precioVenta,
		Activo:       r.FormValue("activo") == "on",
	}, nil
}

// guardarFotos toma los archivos del campo "fotos" (input multiple) y los
// sube al storage configurado, registrando cada uno en producto_fotos.
func (a *App) guardarFotos(conn *sql.DB, r *http.Request, productoID int) error {
	if r.MultipartForm == nil {
		return nil
	}
	files := r.MultipartForm.File["fotos"]
	if len(files) == 0 {
		return nil
	}

	existentes, err := inventario.CountFotos(conn, productoID)
	if err != nil {
		return err
	}

	for i, fh := range files {
		file, err := fh.Open()
		if err != nil {
			return err
		}
		ruta, err := a.Storage.Save(fh.Filename, file)
		file.Close()
		if err != nil {
			return err
		}
		if err := inventario.AddFoto(conn, productoID, ruta, existentes+i); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) renderProductoForm(w http.ResponseWriter, r *http.Request, conn *sql.DB, p inventario.Producto, fotos []inventario.Foto, message string) {
	categorias, _ := inventario.ListCategorias(conn)

	fotosURL := make([]map[string]any, 0, len(fotos))
	for _, f := range fotos {
		fotosURL = append(fotosURL, map[string]any{
			"ID":  f.ID,
			"URL": a.Storage.URL(f.Ruta),
		})
	}

	formAction := "/admin/mantenimiento/inventario/productos/nuevo"
	if p.ID != 0 {
		formAction = "/admin/mantenimiento/inventario/productos/" + strconv.Itoa(p.ID) + "/editar"
	}

	render(w, r, "producto_form.html", map[string]any{
		"Title":      "Producto",
		"Active":     "inventario",
		"Producto":   p,
		"Fotos":      fotosURL,
		"Categorias": categorias,
		"Message":    message,
		"EsNuevo":    p.ID == 0,
		"FormAction": formAction,
	})
}
