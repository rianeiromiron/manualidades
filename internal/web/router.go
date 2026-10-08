package web

import (
	"net/http"

	"github.com/gorilla/mux"
)

// NewRouter arma todas las rutas de la aplicación (admin, tienda y estáticos)
// con sus middlewares. Vive aquí, y no en cmd/server, para que las pruebas de
// seguridad ejerciten exactamente el mismo cableado que usa el servidor real.
func NewRouter(app *App) http.Handler {
	r := mux.NewRouter()
	// Cabeceras de seguridad en toda respuesta (admin, tienda y estáticos):
	// ver internal/web/cabeceras.go.
	r.Use(SecurityHeaders)

	admin := r.PathPrefix("/admin").Subrouter()
	admin.Use(app.RequireAdminAuth)
	// RequireCSRF va después de RequireAdminAuth porque necesita la sesión
	// (su SecretKey) que ese middleware deja en el contexto de la request.
	admin.Use(app.RequireCSRF)

	admin.HandleFunc("/setup", app.AdminSetup).Methods(http.MethodGet, http.MethodPost)
	admin.HandleFunc("/login", app.AdminLogin).Methods(http.MethodGet, http.MethodPost)
	// Solo POST: un <a> o <img> no debe poder cerrar la sesión de nadie con
	// una simple petición GET.
	admin.HandleFunc("/logout", app.AdminLogout).Methods(http.MethodPost)
	admin.HandleFunc("/cambiar-password", app.AdminCambiarPassword).Methods(http.MethodGet, http.MethodPost)
	admin.HandleFunc("/sesiones", app.AdminSesiones).Methods(http.MethodGet)
	admin.HandleFunc("/sesiones/revocar-otras", app.AdminSesionesRevocarOtras).Methods(http.MethodPost)
	admin.HandleFunc("/sesiones/{id}/revocar", app.AdminSesionRevocar).Methods(http.MethodPost)

	// Home y cambio de contraseña son válidos para cualquier sesión, sin
	// importar el módulo asignado; cada uno filtra lo que muestra según
	// la sesión (ver render()/injectNav en internal/web/app.go).
	admin.HandleFunc("", app.Home).Methods(http.MethodGet)
	admin.HandleFunc("/", app.Home).Methods(http.MethodGet)

	bd := admin.PathPrefix("/mantenimiento/bd").Subrouter()
	bd.Use(app.RequireModule("bd"))
	bd.HandleFunc("", app.ConfigDB).Methods(http.MethodGet, http.MethodPost)

	inv := admin.PathPrefix("/mantenimiento/inventario").Subrouter()
	inv.Use(app.RequireModule("inventario"))
	inv.HandleFunc("", app.InventarioHome).Methods(http.MethodGet)

	inv.HandleFunc("/categorias", app.CategoriasList).Methods(http.MethodGet)
	inv.HandleFunc("/categorias", app.CategoriaCrear).Methods(http.MethodPost)
	inv.HandleFunc("/categorias/{id}/editar", app.CategoriaEditar).Methods(http.MethodGet, http.MethodPost)
	inv.HandleFunc("/categorias/{id}/eliminar", app.CategoriaEliminar).Methods(http.MethodPost)

	inv.HandleFunc("/productos", app.ProductosList).Methods(http.MethodGet)
	inv.HandleFunc("/productos/nuevo", app.ProductoNuevo).Methods(http.MethodGet, http.MethodPost)
	inv.HandleFunc("/productos/{id}/editar", app.ProductoEditar).Methods(http.MethodGet, http.MethodPost)
	inv.HandleFunc("/productos/{id}/eliminar", app.ProductoEliminar).Methods(http.MethodPost)
	inv.HandleFunc("/productos/{id}/fotos/{fotoId}/eliminar", app.FotoEliminar).Methods(http.MethodPost)
	inv.HandleFunc("/productos/{id}/movimientos", app.ProductoMovimientosFragment).Methods(http.MethodGet)

	inv.HandleFunc("/movimientos", app.MovimientosList).Methods(http.MethodGet, http.MethodPost)
	// El reporte de movimientos ahora vive en Reportes; el enlace viejo redirige.
	inv.HandleFunc("/reporte", app.ReporteMovimientosRedirect).Methods(http.MethodGet)

	sitioR := admin.PathPrefix("/sitio").Subrouter()
	sitioR.Use(app.RequireModule("sitio"))
	sitioR.HandleFunc("", app.SitioConfig).Methods(http.MethodGet, http.MethodPost)

	pedidosR := admin.PathPrefix("/pedidos").Subrouter()
	pedidosR.Use(app.RequireModule("pedidos"))
	pedidosR.HandleFunc("", app.PedidosList).Methods(http.MethodGet)
	pedidosR.HandleFunc("/pagos", app.PagosList).Methods(http.MethodGet)
	pedidosR.HandleFunc("/{id}", app.PedidoDetalle).Methods(http.MethodGet, http.MethodPost)

	reportesR := admin.PathPrefix("/reportes").Subrouter()
	reportesR.Use(app.RequireModule("reportes"))
	reportesR.HandleFunc("", app.ReportesHome).Methods(http.MethodGet)
	reportesR.HandleFunc("/ventas", app.ReporteVentas).Methods(http.MethodGet)
	reportesR.HandleFunc("/productos", app.ReporteProductos).Methods(http.MethodGet)
	reportesR.HandleFunc("/utilidad", app.ReporteUtilidad).Methods(http.MethodGet)
	reportesR.HandleFunc("/stock", app.ReporteStock).Methods(http.MethodGet)
	reportesR.HandleFunc("/pedidos", app.ReportePedidos).Methods(http.MethodGet)
	reportesR.HandleFunc("/movimientos", app.Reporte).Methods(http.MethodGet)

	// El mantenimiento de usuarios es exclusivo del usuario admin de
	// admin.json: ni siquiera un superusuario puede entrar.
	usuariosR := admin.PathPrefix("/usuarios").Subrouter()
	usuariosR.Use(app.RequireOnlyAdmin)
	usuariosR.HandleFunc("", app.UsuariosList).Methods(http.MethodGet)
	usuariosR.HandleFunc("/nuevo", app.UsuarioNuevo).Methods(http.MethodGet, http.MethodPost)
	usuariosR.HandleFunc("/{id}/editar", app.UsuarioEditar).Methods(http.MethodGet, http.MethodPost)
	usuariosR.HandleFunc("/{id}/activar", app.UsuarioActivar).Methods(http.MethodPost)
	usuariosR.HandleFunc("/{id}/desactivar", app.UsuarioDesactivar).Methods(http.MethodPost)
	usuariosR.HandleFunc("/{id}/eliminar", app.UsuarioEliminar).Methods(http.MethodPost)

	// --- Tienda pública ---
	r.HandleFunc("/", app.TiendaHome).Methods(http.MethodGet)
	r.HandleFunc("/producto/{id}", app.TiendaProducto).Methods(http.MethodGet)
	r.HandleFunc("/carrito", app.TiendaCarrito).Methods(http.MethodGet)
	r.HandleFunc("/checkout", app.TiendaCheckout).Methods(http.MethodGet)
	r.HandleFunc("/checkout/confirmar", app.TiendaCheckoutConfirmar).Methods(http.MethodPost)
	r.HandleFunc("/pedido/{id}/confirmacion", app.TiendaConfirmacion).Methods(http.MethodGet)

	r.PathPrefix("/static/").Handler(noCache(http.StripPrefix("/static/", http.FileServer(http.Dir("web/static")))))
	r.PathPrefix("/media/productos/").Handler(http.StripPrefix("/media/productos/", http.FileServer(http.Dir("media/productos"))))
	r.PathPrefix("/media/sitio/").Handler(http.StripPrefix("/media/sitio/", http.FileServer(http.Dir("media/sitio"))))

	return r
}

// noCache fuerza a que el navegador siempre revalide CSS/JS con el
// servidor (con If-Modified-Since, sigue pudiendo recibir un 304 barato si
// no cambió) en vez de decidir por su cuenta cuánto tiempo cachearlos. Sin
// esto, un cambio de estilos puede tardar en verse según el navegador de
// cada quien.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}
