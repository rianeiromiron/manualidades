package web

import (
	"fmt"
	"reflect"
	"testing"
)

func TestPaginar(t *testing.T) {
	items := make([]int, 45)
	for i := range items {
		items[i] = i
	}
	casos := []struct {
		pagina, quiere, largo, total int
	}{
		{1, 1, 20, 3}, {2, 2, 20, 3}, {3, 3, 5, 3},
		{0, 1, 20, 3}, {-4, 1, 20, 3}, {99, 3, 5, 3},
	}
	for _, c := range casos {
		porcion, actual, total := paginar(items, c.pagina, 20)
		if actual != c.quiere || len(porcion) != c.largo || total != c.total {
			t.Errorf("pagina %d: got (actual %d, largo %d, total %d), quería (%d, %d, %d)",
				c.pagina, actual, len(porcion), total, c.quiere, c.largo, c.total)
		}
	}

	if p, actual, total := paginar([]int(nil), 5, 20); len(p) != 0 || actual != 1 || total != 1 {
		t.Errorf("lista vacía: got (%v, %d, %d)", p, actual, total)
	}
	// Justo 20 y justo 21: el borde entre una y dos páginas.
	if _, _, total := paginar(make([]int, 20), 1, 20); total != 1 {
		t.Errorf("20 elementos deben ser 1 página, no %d", total)
	}
	if _, _, total := paginar(make([]int, 21), 1, 20); total != 2 {
		t.Errorf("21 elementos deben ser 2 páginas, no %d", total)
	}
}

func TestEnlacesPagina(t *testing.T) {
	urlDe := func(n int) string { return fmt.Sprintf("/?pagina=%d", n) }
	forma := func(actual, total int) []string {
		var out []string
		for _, e := range enlacesPagina(actual, total, urlDe) {
			switch {
			case e.Puntos:
				out = append(out, "…")
			case e.Actual:
				out = append(out, fmt.Sprintf("[%d]", e.N))
			default:
				out = append(out, fmt.Sprint(e.N))
			}
		}
		return out
	}
	casos := []struct {
		actual, total int
		quiere        []string
	}{
		{1, 3, []string{"[1]", "2", "3"}},
		{1, 7, []string{"[1]", "2", "3", "4", "5", "6", "7"}},
		{1, 20, []string{"[1]", "2", "…", "20"}},
		{10, 20, []string{"1", "…", "9", "[10]", "11", "…", "20"}},
		{20, 20, []string{"1", "…", "19", "[20]"}},
		{3, 20, []string{"1", "2", "[3]", "4", "…", "20"}},
	}
	for _, c := range casos {
		if got := forma(c.actual, c.total); !reflect.DeepEqual(got, c.quiere) {
			t.Errorf("actual %d de %d: got %v, quería %v", c.actual, c.total, got, c.quiere)
		}
	}
}

func TestUrlCatalogo(t *testing.T) {
	casos := []struct {
		cats   []int
		pagina int
		quiere string
	}{
		{nil, 1, "/"},
		{nil, 2, "/?pagina=2"},
		{[]int{3}, 1, "/?cat=3"},
		{[]int{3, 5}, 4, "/?cat=3&cat=5&pagina=4"},
	}
	for _, c := range casos {
		if got := urlCatalogo(c.cats, c.pagina); got != c.quiere {
			t.Errorf("urlCatalogo(%v, %d) = %q, quería %q", c.cats, c.pagina, got, c.quiere)
		}
	}
}

func TestParsePagina(t *testing.T) {
	for entrada, quiere := range map[string]int{"": 1, "abc": 1, "0": 1, "-3": 1, "2": 2, "17": 17, "1.5": 1} {
		if got := parsePagina(entrada); got != quiere {
			t.Errorf("parsePagina(%q) = %d, quería %d", entrada, got, quiere)
		}
	}
}
