package web

import (
	"strings"
	"testing"
)

func TestErrorLargoPassword(t *testing.T) {
	casos := []struct {
		nombre string
		pass   string
		valida bool
	}{
		{"corta", strings.Repeat("a", 7), false},
		{"minima", strings.Repeat("a", 8), true},
		{"maxima", strings.Repeat("a", 72), true},
		{"larga", strings.Repeat("a", 73), false},
		{"multibyte pasa de 72 bytes", strings.Repeat("ñ", 37), false},
	}
	for _, c := range casos {
		if got := errorLargoPassword(c.pass) == ""; got != c.valida {
			t.Errorf("%s: válida=%v, se esperaba %v", c.nombre, got, c.valida)
		}
	}
}
