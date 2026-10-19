package web

import (
	"net/url"
	"strconv"
)

// productosPorPagina es cuántos artículos muestra el catálogo público por página.
const productosPorPagina = 20

// EnlacePagina es un elemento de la barra de páginas: un número (con o sin
// enlace) o unos puntos suspensivos.
type EnlacePagina struct {
	N      int
	URL    string
	Actual bool
	Puntos bool
}

// paginar devuelve la porción de items que corresponde a la página pedida, la
// página efectiva (se ajusta al rango válido) y el total de páginas (mínimo 1).
func paginar[T any](items []T, pagina, tam int) ([]T, int, int) {
	total := (len(items) + tam - 1) / tam
	if total < 1 {
		total = 1
	}
	if pagina < 1 {
		pagina = 1
	}
	if pagina > total {
		pagina = total
	}
	desde := (pagina - 1) * tam
	hasta := desde + tam
	if hasta > len(items) {
		hasta = len(items)
	}
	return items[desde:hasta], pagina, total
}

// enlacesPagina arma la barra de páginas. Con pocas páginas las muestra todas;
// con muchas, la primera, la última y las vecinas de la actual, con puntos
// suspensivos en los saltos.
func enlacesPagina(actual, total int, urlDe func(int) string) []EnlacePagina {
	var out []EnlacePagina
	ultimaMostrada := 0
	for n := 1; n <= total; n++ {
		if total > 7 && n != 1 && n != total && (n < actual-1 || n > actual+1) {
			continue
		}
		if n-ultimaMostrada > 1 {
			out = append(out, EnlacePagina{Puntos: true})
		}
		out = append(out, EnlacePagina{N: n, URL: urlDe(n), Actual: n == actual})
		ultimaMostrada = n
	}
	return out
}

// parsePagina interpreta el parámetro ?pagina=; cualquier valor inválido es 1.
func parsePagina(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// urlCatalogo arma la dirección del catálogo conservando las categorías
// elegidas. La página 1 no lleva parámetro.
func urlCatalogo(categorias []int, pagina int) string {
	q := url.Values{}
	for _, id := range categorias {
		q.Add("cat", strconv.Itoa(id))
	}
	if pagina > 1 {
		q.Set("pagina", strconv.Itoa(pagina))
	}
	if len(q) == 0 {
		return "/"
	}
	return "/?" + q.Encode()
}
