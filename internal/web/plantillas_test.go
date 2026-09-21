package web

import (
	"html/template"
	"strings"
	"testing"
	"time"

	"manualidades/internal/inventario"
	"manualidades/internal/tienda"
)

// Las plantillas se parsean al vuelo en cada petición (render/renderTienda),
// así que un error de sintaxis o de campo solo se vería al abrir la página.
// Este test ejecuta el bloque "content" de las que dependen de los estados de
// pedido/pago y del stock disponible, con datos de ejemplo.
func ejecutarContent(t *testing.T, archivo string, datos map[string]any) string {
	t.Helper()
	tmpl, err := template.ParseFiles("../../web/templates/" + archivo)
	if err != nil {
		t.Fatalf("%s: %v", archivo, err)
	}
	var sb strings.Builder
	if err := tmpl.ExecuteTemplate(&sb, "content", datos); err != nil {
		t.Fatalf("%s: %v", archivo, err)
	}
	return sb.String()
}

func TestPedidosListConPendientes(t *testing.T) {
	expira := time.Now().Add(15 * time.Minute)
	out := ejecutarContent(t, "pedidos_list.html", map[string]any{
		"Pedidos":    []tienda.Pedido{{ID: 1, ClienteNombre: "Ana", Estado: "pagado", MetodoEntrega: "recoger", CreadoEn: time.Now()}},
		"Pendientes": []tienda.Pedido{{ID: 2, ClienteNombre: "Beto", Estado: "pendiente_pago", MetodoEntrega: "recoger", CreadoEn: time.Now(), ExpiraEn: &expira}},
	})
	for _, quiero := range []string{"Ana", "Pendientes de pago", "Beto", "Pendiente de pago", expira.Format("15:04")} {
		if !strings.Contains(out, quiero) {
			t.Errorf("falta %q en pedidos_list", quiero)
		}
	}
}

func TestPedidoDetalleSegunEstado(t *testing.T) {
	expira := time.Now().Add(10 * time.Minute)
	pagado := ejecutarContent(t, "pedido_detalle.html", map[string]any{
		"Pedido":         tienda.Pedido{ID: 1, Estado: "pagado", MetodoEntrega: "recoger", CreadoEn: time.Now()},
		"EstadosValidos": tienda.EstadosValidos,
	})
	if !strings.Contains(pagado, "Actualizar estado") {
		t.Error("un pedido pagado debe poder cambiar de estado")
	}

	pendiente := ejecutarContent(t, "pedido_detalle.html", map[string]any{
		"Pedido":         tienda.Pedido{ID: 2, Estado: "pendiente_pago", MetodoEntrega: "recoger", CreadoEn: time.Now(), ExpiraEn: &expira},
		"EstadosValidos": tienda.EstadosValidos,
	})
	if strings.Contains(pendiente, "Actualizar estado") {
		t.Error("un pedido pendiente de pago NO debe ofrecer cambiar de estado (bodega no despacha lo no pagado)")
	}
	if !strings.Contains(pendiente, "no está pagado") {
		t.Error("falta la explicación de por qué no se puede cambiar el estado")
	}

	pago := tienda.Pago{Estado: "por_conciliar", Metodo: "tarjeta", Referencia: "R1", MotivoRechazo: "sin stock", CreadoEn: time.Now()}
	conciliar := ejecutarContent(t, "pedido_detalle.html", map[string]any{
		"Pedido":         tienda.Pedido{ID: 3, Estado: "expirado", MetodoEntrega: "recoger", CreadoEn: time.Now()},
		"EstadosValidos": tienda.EstadosValidos,
		"Pago":           &pago,
	})
	if !strings.Contains(conciliar, "Por conciliar") || !strings.Contains(conciliar, "sin stock") {
		t.Error("el detalle debe mostrar el pago por conciliar y su motivo")
	}
}

func TestPagosListEstados(t *testing.T) {
	pedido := 5
	out := ejecutarContent(t, "pagos_list.html", map[string]any{
		"Pagos": []tienda.Pago{
			{Estado: "aprobado", PedidoID: &pedido, CreadoEn: time.Now()},
			{Estado: "rechazado", PedidoID: &pedido, CreadoEn: time.Now()},
			{Estado: "por_conciliar", PedidoID: &pedido, CreadoEn: time.Now()},
			{Estado: "iniciado", PedidoID: &pedido, CreadoEn: time.Now()},
			{Estado: "fallo", PedidoID: &pedido, CreadoEn: time.Now()},
		},
	})
	for _, quiero := range []string{"Aprobado", "Rechazado", "Por conciliar", "En curso", "Falla del proveedor"} {
		if !strings.Contains(out, quiero) {
			t.Errorf("falta la etiqueta %q en pagos_list", quiero)
		}
	}
}

// La tienda pública debe ofrecer el stock DISPONIBLE (físico − reservado), no
// el físico: un producto con todo el stock reservado sale "Agotado".
func TestTiendaUsaStockDisponible(t *testing.T) {
	agotado := inventario.Producto{ID: 1, Nombre: "Reservado", Activo: true, Stock: 5, Disponible: 0, PrecioVenta: 10}
	out := ejecutarContent(t, "tienda_producto.html", map[string]any{"Producto": agotado})
	if !strings.Contains(out, "Agotado") {
		t.Error("tienda_producto: con Disponible=0 debe mostrar Agotado aunque haya stock físico")
	}

	hay := inventario.Producto{ID: 2, Nombre: "Libre", Activo: true, Stock: 5, Disponible: 3, PrecioVenta: 10}
	out = ejecutarContent(t, "tienda_producto.html", map[string]any{"Producto": hay})
	if !strings.Contains(out, `max="3"`) {
		t.Error("tienda_producto: la cantidad máxima debe ser el disponible (3), no el físico (5)")
	}

	out = ejecutarContent(t, "tienda_home.html", map[string]any{
		"Productos":  []inventario.Producto{agotado, hay},
		"Categorias": []inventario.Categoria{},
	})
	if !strings.Contains(out, `data-stock="3"`) || !strings.Contains(out, "Agotado") {
		t.Error("tienda_home: debe usar el disponible para el tope del carrito y el estado Agotado")
	}
}
