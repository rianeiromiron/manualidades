package tienda

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"manualidades/internal/config"
	"manualidades/internal/db"
	"manualidades/internal/inventario"
)

var (
	sondaOnce sync.Once
	sondaErr  error
)

// nuevaBase crea una base de datos temporal en el Postgres configurado en
// config.json (nunca toca la base real), le aplica las migraciones y la
// elimina al terminar el test. Si no hay Postgres accesible, salta el test.
func nuevaBase(t *testing.T) *sql.DB {
	t.Helper()

	// config.Load lee config.json relativo al directorio actual, que en un
	// test es el del paquete; se sube a la raíz del proyecto solo para leerlo.
	cwd, _ := os.Getwd()
	if err := os.Chdir("../.."); err != nil {
		t.Skip("no se pudo ubicar config.json:", err)
	}
	cfg, err := config.Load()
	os.Chdir(cwd)
	if err != nil {
		t.Skip("sin config.json:", err)
	}
	// Sonda con timeout, una sola vez por ejecución: si el puerto acepta
	// conexiones pero no hay un Postgres respondiendo (p. ej. Docker Desktop
	// con el contenedor caído), se saltan los tests en vez de colgarse.
	sondaOnce.Do(func() {
		probe, err := sql.Open("postgres", cfg.DSNMaintenance()+" connect_timeout=5")
		if err != nil {
			sondaErr = err
			return
		}
		defer probe.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		sondaErr = probe.PingContext(ctx)
	})
	if sondaErr != nil {
		t.Skip("no hay Postgres accesible:", sondaErr)
	}

	cfg.DBName = fmt.Sprintf("manualidades_test_%d", time.Now().UnixNano())
	if _, err := db.EnsureDatabase(cfg); err != nil {
		t.Skip("no se pudo crear la base temporal:", err)
	}
	conn, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		admin, err := sql.Open("postgres", cfg.DSNMaintenance())
		if err != nil {
			return
		}
		defer admin.Close()
		admin.Exec(`DROP DATABASE IF EXISTS "` + cfg.DBName + `" WITH (FORCE)`)
	})

	if err := inventario.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	// Migrate debe ser idempotente.
	if err := Migrate(conn); err != nil {
		t.Fatalf("segunda migración: %v", err)
	}
	return conn
}

// nuevoProducto crea un producto activo con stock físico inicial.
func nuevoProducto(t *testing.T, conn *sql.DB, nombre string, stock float64) int {
	t.Helper()
	var catID int
	if err := conn.QueryRow(`INSERT INTO categorias (nombre) VALUES ($1) ON CONFLICT (nombre) DO UPDATE SET nombre = EXCLUDED.nombre RETURNING id`, "General").Scan(&catID); err != nil {
		t.Fatal(err)
	}
	id, err := inventario.CreateProducto(conn, inventario.Producto{CategoriaID: catID, Nombre: nombre, PrecioVenta: 10, Activo: true})
	if err != nil {
		t.Fatal(err)
	}
	if stock > 0 {
		if err := inventario.CreateMovimiento(conn, id, "ingreso", stock, "inicial", false, 0, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func datos() DatosPedido {
	return DatosPedido{ClienteNombre: "Ana", ClienteTelefono: "5555", MetodoEntrega: "recoger"}
}

func linea(productoID int, cantidad float64) LineaPedido {
	return LineaPedido{ProductoID: productoID, Nombre: fmt.Sprintf("prod%d", productoID), Cantidad: cantidad, PrecioUnitario: 10, Subtotal: cantidad * 10}
}

func stockFisico(t *testing.T, conn *sql.DB, id int) float64 {
	t.Helper()
	s, err := inventario.StockActual(conn, id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func estadoPedido(t *testing.T, conn *sql.DB, id int) string {
	t.Helper()
	var e string
	if err := conn.QueryRow(`SELECT estado FROM pedidos WHERE id = $1`, id).Scan(&e); err != nil {
		t.Fatal(err)
	}
	return e
}

func estadoPago(t *testing.T, conn *sql.DB, id int) string {
	t.Helper()
	var e string
	if err := conn.QueryRow(`SELECT estado FROM pagos WHERE id = $1`, id).Scan(&e); err != nil {
		t.Fatal(err)
	}
	return e
}

func reservado(t *testing.T, conn *sql.DB, id int) float64 {
	t.Helper()
	m, err := ReservadoPorProducto(conn)
	if err != nil {
		t.Fatal(err)
	}
	return m[id]
}

func TestFlujoAprobado(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Collar", 5)

	in, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 3)}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got := estadoPedido(t, conn, in.PedidoID); got != EstadoPagando {
		t.Fatalf("estado tras reservar = %q, quiero pagando", got)
	}
	if got := estadoPago(t, conn, in.PagoID); got != EstadoPagoIniciado {
		t.Fatalf("pago tras reservar = %q, quiero iniciado", got)
	}
	// Reservar NO toca el kardex, pero sí baja el disponible.
	if got := stockFisico(t, conn, p); got != 5 {
		t.Fatalf("stock físico tras reservar = %v, quiero 5", got)
	}
	if got := reservado(t, conn, p); got != 3 {
		t.Fatalf("reservado = %v, quiero 3", got)
	}

	if err := ConfirmarPago(conn, in, ResultadoPago{Referencia: "REF1", Marca: "visa", Ultimos4: "1111"}); err != nil {
		t.Fatal(err)
	}
	if got := estadoPedido(t, conn, in.PedidoID); got != EstadoPagado {
		t.Fatalf("estado tras confirmar = %q, quiero pagado", got)
	}
	if got := estadoPago(t, conn, in.PagoID); got != EstadoPagoAprobado {
		t.Fatalf("pago tras confirmar = %q, quiero aprobado", got)
	}
	if got := stockFisico(t, conn, p); got != 2 {
		t.Fatalf("stock físico tras confirmar = %v, quiero 2", got)
	}
	if got := reservado(t, conn, p); got != 0 {
		t.Fatalf("reservado tras confirmar = %v, quiero 0 (ya está en el kardex)", got)
	}

	// Un pedido pagado ya no se puede volver a confirmar (sin doble descarga).
	if err := ConfirmarPago(conn, in, ResultadoPago{Referencia: "REF1"}); !errors.Is(err, ErrPedidoNoPagable) {
		t.Fatalf("segunda confirmación: err = %v, quiero ErrPedidoNoPagable", err)
	}
	if got := stockFisico(t, conn, p); got != 2 {
		t.Fatalf("doble descarga: stock = %v, quiero 2", got)
	}
}

func TestReservaBloqueaAOtros(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Aretes", 5)

	if _, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 3)}, 30); err != nil {
		t.Fatal(err)
	}
	// Quedan 2 disponibles: pedir 3 falla, sin haber cobrado nada.
	if _, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 3)}, 30); !errors.Is(err, inventario.ErrStockInsuficiente) {
		t.Fatalf("err = %v, quiero ErrStockInsuficiente", err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM pedidos`).Scan(&n)
	if n != 1 {
		t.Fatalf("un intento fallido dejó %d pedidos, quiero 1", n)
	}
	// Pedir 2 sí alcanza.
	if _, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 2)}, 20); err != nil {
		t.Fatal(err)
	}
}

func TestRechazoYReintentoReutilizaPedido(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Pulsera", 5)
	q := nuevoProducto(t, conn, "Anillo", 5)

	in, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 3)}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegistrarCobroFallido(conn, in, EstadoPagoRechazado, "REF-R", "fondos insuficientes"); err != nil {
		t.Fatal(err)
	}
	if got := estadoPedido(t, conn, in.PedidoID); got != EstadoPendientePago {
		t.Fatalf("estado tras rechazo = %q, quiero pendiente_pago", got)
	}
	if got := estadoPago(t, conn, in.PagoID); got != EstadoPagoRechazado {
		t.Fatalf("pago tras rechazo = %q, quiero rechazado", got)
	}
	// La reserva sigue viva para que el cliente reintente.
	if got := reservado(t, conn, p); got != 3 {
		t.Fatalf("reservado tras rechazo = %v, quiero 3", got)
	}

	// Reintento con el mismo token y OTRO carrito: mismo pedido, líneas nuevas.
	// Pide 4 de p: cabe porque su propia reserva de 3 no cuenta contra sí misma.
	in2, err := IniciarPago(conn, in.Token, datos(), []LineaPedido{linea(p, 4), linea(q, 1)}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if in2.PedidoID != in.PedidoID {
		t.Fatalf("el reintento creó el pedido %d, quiero reutilizar el %d", in2.PedidoID, in.PedidoID)
	}
	if in2.PagoID == in.PagoID {
		t.Fatal("cada intento debe tener su propio registro de pago")
	}
	if got := reservado(t, conn, p); got != 4 {
		t.Fatalf("reservado de p tras reintento = %v, quiero 4 (líneas reemplazadas, no sumadas)", got)
	}
	if got := reservado(t, conn, q); got != 1 {
		t.Fatalf("reservado de q = %v, quiero 1", got)
	}
	var pedidos int
	conn.QueryRow(`SELECT COUNT(*) FROM pedidos`).Scan(&pedidos)
	if pedidos != 1 {
		t.Fatalf("hay %d pedidos, quiero 1", pedidos)
	}

	if err := ConfirmarPago(conn, in2, ResultadoPago{Referencia: "REF-OK", Marca: "visa", Ultimos4: "4242"}); err != nil {
		t.Fatal(err)
	}
	if got := stockFisico(t, conn, p); got != 1 {
		t.Fatalf("stock de p = %v, quiero 1", got)
	}
}

func TestDobleClicNoCobraDosVeces(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Llavero", 5)

	in, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Mismo navegador (mismo token) reenvía mientras el cobro sigue en curso.
	if _, err := IniciarPago(conn, in.Token, datos(), []LineaPedido{linea(p, 1)}, 10); !errors.Is(err, ErrPedidoEnProceso) {
		t.Fatalf("err = %v, quiero ErrPedidoEnProceso", err)
	}
}

func TestStockAgotadoAlConfirmarQuedaPorConciliar(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Bolso", 2)

	in, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 2)}, 20)
	if err != nil {
		t.Fatal(err)
	}
	// El admin consume stock físico a mano mientras el cliente paga.
	if err := inventario.CreateMovimiento(conn, p, "consumo", 2, "se rompió", false, 0, time.Now()); err != nil {
		t.Fatal(err)
	}

	err = ConfirmarPago(conn, in, ResultadoPago{Referencia: "REF-X", Marca: "visa", Ultimos4: "1111"})
	if !errors.Is(err, inventario.ErrStockInsuficiente) {
		t.Fatalf("err = %v, quiero ErrStockInsuficiente", err)
	}
	// Rollback completo: ni pedido pagado ni kardex a medias.
	if got := estadoPedido(t, conn, in.PedidoID); got != EstadoPagando {
		t.Fatalf("estado tras confirmación fallida = %q, quiero pagando", got)
	}
	if got := stockFisico(t, conn, p); got != 0 {
		t.Fatalf("stock = %v, quiero 0 (solo el consumo manual)", got)
	}

	if err := MarcarPorConciliar(conn, in, ResultadoPago{Referencia: "REF-X", Marca: "visa", Ultimos4: "1111"}, "sin stock"); err != nil {
		t.Fatal(err)
	}
	if got := estadoPago(t, conn, in.PagoID); got != EstadoPagoPorConciliar {
		t.Fatalf("pago = %q, quiero por_conciliar", got)
	}
	if got := estadoPedido(t, conn, in.PedidoID); got != EstadoExpirado {
		t.Fatalf("pedido = %q, quiero expirado (reserva liberada)", got)
	}
	var ref string
	conn.QueryRow(`SELECT referencia FROM pagos WHERE id = $1`, in.PagoID).Scan(&ref)
	if ref != "REF-X" {
		t.Fatalf("referencia = %q: debe quedar la real del cobro para poder reembolsar", ref)
	}
}

func TestExpiracion(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Cuaderno", 3)

	// Pedido pendiente_pago (rechazado) y luego vencido.
	in, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 3)}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegistrarCobroFallido(conn, in, EstadoPagoRechazado, "R", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE pedidos SET expira_en = now() - interval '1 minute' WHERE id = $1`, in.PedidoID); err != nil {
		t.Fatal(err)
	}

	// Aunque la limpieza no haya corrido, la reserva vencida ya no cuenta.
	if got := reservado(t, conn, p); got != 0 {
		t.Fatalf("reservado con reserva vencida = %v, quiero 0", got)
	}
	in2, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 3)}, 30)
	if err != nil {
		t.Fatalf("otro cliente debería poder tomar el stock: %v", err)
	}

	// Ese segundo pedido muere a mitad del cobro: pagando + vencido.
	if _, err := conn.Exec(`UPDATE pedidos SET expira_en = now() - interval '1 minute' WHERE id = $1`, in2.PedidoID); err != nil {
		t.Fatal(err)
	}

	n, err := LimpiarExpirados(conn)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("LimpiarExpirados expiró %d pedidos, quiero 2", n)
	}
	if got := estadoPedido(t, conn, in.PedidoID); got != EstadoExpirado {
		t.Fatalf("pendiente vencido = %q, quiero expirado", got)
	}
	if got := estadoPedido(t, conn, in2.PedidoID); got != EstadoExpirado {
		t.Fatalf("pagando vencido = %q, quiero expirado", got)
	}
	// El intento que quedó "iniciado" sin desenlace pasa a conciliar; el ya
	// rechazado conserva su estado.
	if got := estadoPago(t, conn, in2.PagoID); got != EstadoPagoPorConciliar {
		t.Fatalf("pago interrumpido = %q, quiero por_conciliar", got)
	}
	if got := estadoPago(t, conn, in.PagoID); got != EstadoPagoRechazado {
		t.Fatalf("pago rechazado = %q, quiero rechazado (no debe cambiar)", got)
	}
	// Un pedido expirado no se puede confirmar ni reutilizar.
	if err := ConfirmarPago(conn, in2, ResultadoPago{Referencia: "TARDE"}); !errors.Is(err, ErrPedidoNoPagable) {
		t.Fatalf("confirmar pedido expirado: err = %v, quiero ErrPedidoNoPagable", err)
	}
	if _, err := IniciarPago(conn, in.Token, datos(), []LineaPedido{linea(p, 1)}, 10); err != nil {
		t.Fatal(err)
	}
	if got := estadoPedido(t, conn, in.PedidoID); got != EstadoExpirado {
		t.Fatalf("un pedido expirado no debe revivir; estado = %q", got)
	}
}

// Sin sobreventa: con 1 unidad y muchos clientes a la vez, exactamente uno la
// reserva y el resto recibe ErrStockInsuficiente (sin cobrar).
func TestConcurrenciaNoSobrevende(t *testing.T) {
	conn := nuevaBase(t)
	conn.SetMaxOpenConns(20)
	p := nuevoProducto(t, conn, "Unico", 1)

	const clientes = 12
	var ok, sinStock, otros atomic.Int32
	var wg sync.WaitGroup
	arranque := make(chan struct{})
	for i := 0; i < clientes; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-arranque
			_, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, inventario.ErrStockInsuficiente):
				sinStock.Add(1)
			default:
				t.Errorf("error inesperado: %v", err)
				otros.Add(1)
			}
		}()
	}
	close(arranque)
	wg.Wait()

	if ok.Load() != 1 || sinStock.Load() != clientes-1 {
		t.Fatalf("reservaron %d y %d se quedaron sin stock; quiero 1 y %d", ok.Load(), sinStock.Load(), clientes-1)
	}
}

// Dos pedidos con los mismos productos en orden inverso, a la vez, no deben
// quedar en deadlock (los locks se toman siempre en orden de id).
func TestSinDeadlockConProductosEnOrdenInverso(t *testing.T) {
	conn := nuevaBase(t)
	conn.SetMaxOpenConns(20)
	a := nuevoProducto(t, conn, "A", 100)
	b := nuevoProducto(t, conn, "B", 100)

	var wg sync.WaitGroup
	arranque := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-arranque
			lineas := []LineaPedido{linea(a, 1), linea(b, 1)}
			if i%2 == 1 {
				lineas = []LineaPedido{linea(b, 1), linea(a, 1)}
			}
			in, err := IniciarPago(conn, "", datos(), lineas, 20)
			if err != nil {
				t.Errorf("IniciarPago: %v", err)
				return
			}
			if err := ConfirmarPago(conn, in, ResultadoPago{Referencia: fmt.Sprintf("R%d", i)}); err != nil {
				t.Errorf("ConfirmarPago: %v", err)
			}
		}(i)
	}
	close(arranque)
	wg.Wait()

	if got := stockFisico(t, conn, a); got != 80 {
		t.Fatalf("stock de A = %v, quiero 80", got)
	}
	if got := stockFisico(t, conn, b); got != 80 {
		t.Fatalf("stock de B = %v, quiero 80", got)
	}
}

func TestListadosYEstadoManual(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Taza", 5)

	pendiente, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	pagado, err := IniciarPago(conn, "", datos(), []LineaPedido{linea(p, 1)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfirmarPago(conn, pagado, ResultadoPago{Referencia: "OK"}); err != nil {
		t.Fatal(err)
	}

	reales, err := ListPedidos(conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(reales) != 1 || reales[0].ID != pagado.PedidoID {
		t.Fatalf("ListPedidos debe traer solo el pagado; trajo %+v", reales)
	}
	pend, err := ListPedidosPendientes(conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 1 || pend[0].ID != pendiente.PedidoID || pend[0].ExpiraEn == nil {
		t.Fatalf("ListPedidosPendientes debe traer solo el que reserva; trajo %+v", pend)
	}

	// El admin no puede "entregar" algo que no se ha pagado...
	if err := ActualizarEstado(conn, pendiente.PedidoID, EstadoEntregado); !errors.Is(err, ErrEstadoNoModificable) {
		t.Fatalf("err = %v, quiero ErrEstadoNoModificable", err)
	}
	// ...pero sí avanzar uno pagado.
	if err := ActualizarEstado(conn, pagado.PedidoID, EstadoProcesando); err != nil {
		t.Fatal(err)
	}
}

func TestCotizarCarritoJuntaDuplicados(t *testing.T) {
	conn := nuevaBase(t)
	p := nuevoProducto(t, conn, "Sticker", 10)

	lineas, total, err := CotizarCarrito(conn, []ItemCarrito{{p, 2}, {p, 3}, {p, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if len(lineas) != 1 || lineas[0].Cantidad != 5 || total != 50 {
		t.Fatalf("lineas=%+v total=%v; quiero una sola línea de 5 por Q50", lineas, total)
	}
	if _, _, err := CotizarCarrito(conn, nil); !errors.Is(err, ErrCarritoVacio) {
		t.Fatalf("err = %v, quiero ErrCarritoVacio", err)
	}
}
