package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"manualidades/internal/inventario"
)

// sembrarCatalogo crea dos categorías con n1 y n2 productos activos con stock.
func sembrarCatalogo(t *testing.T, e *entorno, n1, n2 int) (cat1, cat2 int) {
	t.Helper()
	for _, nombre := range []string{"Alfa", "Beta"} {
		if err := inventario.CreateCategoria(e.Conn, nombre, ""); err != nil {
			t.Fatal(err)
		}
	}
	for i, p := range []*int{&cat1, &cat2} {
		nombre := []string{"Alfa", "Beta"}[i]
		if err := e.Conn.QueryRow(`SELECT id FROM categorias WHERE nombre = $1`, nombre).Scan(p); err != nil {
			t.Fatal(err)
		}
	}
	crear := func(cat, n int, prefijo string) {
		for i := 1; i <= n; i++ {
			id, err := inventario.CreateProducto(e.Conn, inventario.Producto{
				CategoriaID: cat, Nombre: fmt.Sprintf("%s %03d", prefijo, i), Descripcion: "d",
				PrecioCompra: 5, PrecioVenta: 10, Activo: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := inventario.CreateMovimiento(e.Conn, id, "ingreso", 5, "inicial", false, 0, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	crear(cat1, n1, "Alfa")
	crear(cat2, n2, "Beta")
	return cat1, cat2
}

func tarjetas(body string) int { return strings.Count(body, `class="t-card"`) }

func TestCatalogoPaginadoDe20(t *testing.T) {
	e := nuevoEntorno(t, true)
	cat1, cat2 := sembrarCatalogo(t, e, 45, 3)
	c := e.cliente()

	casos := []struct {
		nombre   string
		ruta     string
		tarjetas int
		paginado bool
	}{
		{"primera página sin filtro", "/", 20, true},
		{"segunda página", "/?pagina=2", 20, true},
		{"última página, incompleta", "/?pagina=3", 8, true},
		{"página fuera de rango se ajusta a la última", "/?pagina=99", 8, true},
		{"página inválida cae en la primera", "/?pagina=abc", 20, true},
		{"página cero cae en la primera", "/?pagina=0", 20, true},
		{"categoría con más de 20", fmt.Sprintf("/?cat=%d", cat1), 20, true},
		{"categoría con más de 20, última página", fmt.Sprintf("/?cat=%d&pagina=3", cat1), 5, true},
		{"categoría con menos de 20 no pagina", fmt.Sprintf("/?cat=%d", cat2), 3, false},
		{"varias categorías", fmt.Sprintf("/?cat=%d&cat=%d&pagina=3", cat1, cat2), 8, true},
		{"categoría inexistente se ignora", "/?cat=99999", 20, true},
		{"categoría no numérica se ignora", "/?cat=x", 20, true},
	}
	for _, tc := range casos {
		resp, body := c.get(tc.ruta)
		if resp.StatusCode != 200 {
			t.Errorf("%s: %s devolvió %d", tc.nombre, tc.ruta, resp.StatusCode)
			continue
		}
		if got := tarjetas(body); got != tc.tarjetas {
			t.Errorf("%s: %s mostró %d tarjetas, quería %d", tc.nombre, tc.ruta, got, tc.tarjetas)
		}
		if got := strings.Contains(body, `class="t-paginacion"`); got != tc.paginado {
			t.Errorf("%s: %s barra de páginas = %v, quería %v", tc.nombre, tc.ruta, got, tc.paginado)
		}
	}
}

func TestCatalogoPaginasNoRepitenNiPierdenProductos(t *testing.T) {
	e := nuevoEntorno(t, true)
	sembrarCatalogo(t, e, 45, 3)
	c := e.cliente()

	vistos := map[string]int{}
	for p := 1; p <= 3; p++ {
		_, body := c.get(fmt.Sprintf("/?pagina=%d", p))
		for _, parte := range strings.Split(body, `class="t-card-title"`)[1:] {
			inicio := strings.Index(parte, ">") + 1
			nombre := parte[strings.Index(parte[inicio:], ">")+inicio+1:]
			nombre = nombre[:strings.Index(nombre, "<")]
			vistos[nombre]++
		}
	}
	if len(vistos) != 48 {
		t.Errorf("entre las 3 páginas se vieron %d productos distintos, quería 48", len(vistos))
	}
	for n, veces := range vistos {
		if veces != 1 {
			t.Errorf("%q aparece %d veces", n, veces)
		}
	}
}

func TestCatalogoLosEnlacesConservanElFiltro(t *testing.T) {
	e := nuevoEntorno(t, true)
	cat1, _ := sembrarCatalogo(t, e, 45, 3)
	c := e.cliente()

	_, body := c.get(fmt.Sprintf("/?cat=%d", cat1))
	if want := fmt.Sprintf(`href="/?cat=%d&amp;pagina=2"`, cat1); !strings.Contains(body, want) {
		t.Errorf("el enlace «siguiente» no conserva la categoría; falta %s", want)
	}
	if strings.Contains(body, `rel="prev"`) {
		t.Error("la primera página no debe tener «anterior»")
	}

	_, body = c.get(fmt.Sprintf("/?cat=%d&pagina=3", cat1))
	if strings.Contains(body, `rel="next"`) {
		t.Error("la última página no debe tener «siguiente»")
	}
	if !strings.Contains(body, `rel="prev"`) {
		t.Error("la última página debe tener «anterior»")
	}
}

func TestCatalogoCategoriaSinProductosActivosAvisa(t *testing.T) {
	e := nuevoEntorno(t, true)
	_, cat2 := sembrarCatalogo(t, e, 5, 0)
	_, body := e.cliente().get(fmt.Sprintf("/?cat=%d", cat2))
	if tarjetas(body) != 0 || !strings.Contains(body, "Ningún producto coincide") {
		t.Error("una categoría sin productos debe mostrar el aviso y ninguna tarjeta")
	}
	if !strings.Contains(body, `id="tFiltroBtn"`) {
		t.Error("aun sin resultados debe seguir el filtro para poder cambiarlo")
	}
}
