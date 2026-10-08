package main

import (
	"log"
	"net/http"
	"time"

	"manualidades/internal/config"
	"manualidades/internal/db"
	"manualidades/internal/inventario"
	"manualidades/internal/sitio"
	"manualidades/internal/storage"
	"manualidades/internal/tienda"
	"manualidades/internal/sesiones"
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
			if err := sesiones.Migrate(conn); err != nil {
				log.Printf("advertencia: no se pudo migrar el esquema de sesiones: %v", err)
			}
			app.SetDB(conn)
			log.Printf("conectado a la base de datos %q en %s:%d", cfg.DBName, cfg.Host, cfg.Port)
		} else {
			log.Printf("advertencia: no se pudo conectar a la base de datos (configúrala en /admin/mantenimiento/bd): %v", err)
		}
	}

	go limpiarReservas(app)

	r := web.NewRouter(app)

	addr := ":8090"
	// http.ListenAndServe(addr, r) a secas no pone ningún límite de tiempo:
	// una conexión que manda el header o el body a medio byte por segundo
	// (o que nunca lo termina) se queda abierta indefinidamente, agotando
	// las conexiones disponibles del servidor (tipo "slowloris"). Los
	// timeouts de abajo cierran esas conexiones sin afectar el uso normal:
	// ReadTimeout/WriteTimeout dan margen de sobra a la subida de fotos
	// (hasta maxUploadBytes) y al checkout (que espera hasta 10s a la
	// pasarela, ver internal/pasarela/cliente.go).
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("manualidades escuchando en http://localhost%s", addr)
	log.Fatal(srv.ListenAndServe())
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
