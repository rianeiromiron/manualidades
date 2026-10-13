package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func jpegGrande(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 5), B: uint8(x + y), A: 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestExtensionCompatible(t *testing.T) {
	casos := []struct {
		nombre, salida string
		quiero         bool
	}{
		{"a.jpg", ".jpg", true},
		{"a.JPG", ".jpg", true},
		{"a.jpeg", ".jpg", true},
		{"a.png", ".png", true},
		{"a.png", ".jpg", false},
		{"a.jpg", ".png", false},
		{"a.webp", ".jpg", false},
		{"sin-extension", ".jpg", false},
	}
	for _, c := range casos {
		if got := extensionCompatible(c.nombre, c.salida); got != c.quiero {
			t.Errorf("extensionCompatible(%q, %q) = %v, quería %v", c.nombre, c.salida, got, c.quiero)
		}
	}
}

func TestProcesarReduceFotosGrandes(t *testing.T) {
	original := jpegGrande(t, 3000, 2000)
	salida, r, _ := procesar("foto.jpg", original)
	if r != reducida {
		t.Fatalf("resultado %v, quería reducida", r)
	}
	if len(salida) >= len(original) {
		t.Fatalf("la foto reducida (%d bytes) no es más pequeña que la original (%d)", len(salida), len(original))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(salida))
	if err != nil || cfg.Width != 1200 || cfg.Height != 800 {
		t.Fatalf("dimensiones %dx%d (err %v), quería 1200x800", cfg.Width, cfg.Height, err)
	}
}

func TestProcesarCopiaLoQueNoDebeTocar(t *testing.T) {
	jpg := jpegGrande(t, 3000, 2000)

	// JPEG guardado con extensión .webp: el servidor lo serviría como WebP.
	if salida, r, _ := procesar("foto.webp", jpg); r != copiadaTipoDistinto || !bytes.Equal(salida, jpg) {
		t.Errorf("foto.webp: resultado %v, debía copiarse sin cambios", r)
	}
	// No es una imagen.
	texto := []byte("no soy una foto")
	if salida, r, _ := procesar("raro.jpg", texto); r != copiadaError || !bytes.Equal(salida, texto) {
		t.Errorf("raro.jpg: resultado %v, debía copiarse sin cambios", r)
	}
	// Ya es pequeña y comprimida: reducirla no la hace más ligera.
	pequena := jpegGrande(t, 40, 30)
	if salida, r, _ := procesar("mini.jpg", pequena); r == reducida && len(salida) >= len(pequena) {
		t.Errorf("mini.jpg: se marcó como reducida pero no pesa menos")
	}
}
