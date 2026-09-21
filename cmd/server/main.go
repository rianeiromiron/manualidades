package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"manualidades/internal/config"
	"manualidades/internal/db"
	"manualidades/internal/inventario"
	"manualidades/internal/sitio"
	"manualidades/internal/storage"
	"manualidades/internal/tienda"
	"manualidades/internal/usuarios"
	"manualidades/internal/web"
)

func main() {
	app := &web.App{}

	productosStorage, err := storage.NewLocal("media/productos", "/media/productos")
	if err != nil {
		log.Fatalf("no se pudo preparar el almacenamiento de fotos: %v", err)
	}
	app.Storage = productosStorage

	logoStorage, err := storage.NewLocal("media/sitio", "/media/sitio")
	if err != nil {
		log.Fatalf("no se pudo preparar el almacenamiento del logo: %v", err)
	}
	app.LogoStorage = logoStorage

	if cfg, err := config.Load(); err == nil {
		if conn, err := db.Open(cfg); err == nil {
			if err := inventario.Migrate(conn); err != nil {
				log.Printf("advertencia: no se pudo migrar el esquema de inventario: %v", err)
			}
			if err := sitio.Migrate(conn); err != nil {
				log.Printf("advertencia: no se pudo migrar el esquema del sitio: %v", err)
			}
			if err := tienda.Migrate(conn); err != nil {
				log.Printf("advertencia: no se pudo migrar el esquema de la tienda: %v", err)
			}
			if err := usuarios.Migrate(conn); err != nil {
				log.Printf("advertencia: no se pudo migrar el esquema de usuarios: %v", err)
			}
			app.SetDB(conn)
			log.Printf("conectado a la base de datos %q en %s:%d", cfg.DBName, cfg.Host, cfg.Port)
		} else {
			log.Printf("advertencia: no se pudo conectar a la base de datos (configúrala en /admin/mantenimiento/bd): %v", err)
		}
	}

	go limpiarReservas(app)

	r := mux.NewRouter()

	admin := r.PathPrefix("/admin").Subrouter()
	admin.Use(app.RequireAdminAuth)

	admin.HandleFunc("/setup", app.AdminSetup).Methods(http.MethodGet, http.MethodPost)
	admin.HandleFunc("/login", app.AdminLogin).Methods(http.MethodGet, http.MethodPost)
	admin.HandleFunc("/logout", app.AdminLogout).Methods(http.MethodGet, http.MethodPost)
	admin.HandleFunc("/cambiar-password", app.AdminCambiarPassword).Methods(http.MethodGet, http.MethodPost)

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
	inv.HandleFunc("/reporte", app.Reporte).Methods(http.MethodGet)

	sitioR := admin.PathPrefix("/sitio").Subrouter()
	sitioR.Use(app.RequireModule("sitio"))
	sitioR.HandleFunc("", app.SitioConfig).Methods(http.MethodGet, http.MethodPost)

	pedidosR := admin.PathPrefix("/pedidos").Subrouter()
	pedidosR.Use(app.RequireModule("pedidos"))
	pedidosR.HandleFunc("", app.PedidosList).Methods(http.MethodGet)
	pedidosR.HandleFunc("/pagos", app.PagosList).Methods(http.MethodGet)
	pedidosR.HandleFunc("/{id}", app.PedidoDetalle).Methods(http.MethodGet, http.MethodPost)

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

	addr := ":8090"
	log.Printf("manualidades escuchando en http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, r))
}

// limpiarReservas pasa a "expirado" los pedidos cuya reserva de stock venció
// y deja por conciliar los cobros que quedaron a medias. No hace falta para
// que el stock disponible sea correcto (las consultas ya ignoran reservas
// vencidas): solo mantiene ordenado el estado de pedidos y pagos. Consulta
// app.DB() en cada vuelta porque la base puede configurarse o cambiarse con
// el servidor ya corriendo.
func limpiarReservas(app *web.App) {
	for range time.Tick(time.Minute) {
		conn := app.DB()
		if conn == nil {
			continue
		}
		if n, err := tienda.LimpiarExpirados(conn); err != nil {
			log.Printf("advertencia: no se pudieron limpiar las reservas vencidas: %v", err)
		} else if n > 0 {
			log.Printf("reservas vencidas liberadas: %d pedido(s)", n)
		}
	}
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
