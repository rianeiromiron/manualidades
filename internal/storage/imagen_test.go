package storage

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jpegDe(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func dimensiones(t *testing.T, datos []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(datos))
	if err != nil {
		t.Fatalf("la salida no es una imagen: %v", err)
	}
	return cfg.Width, cfg.Height
}

// conOrientacion inserta un segmento EXIF con la orientación indicada justo
// después de la marca SOI de un JPEG.
func conOrientacion(jpg []byte, o uint16) []byte {
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1}
	entrada := make([]byte, 12)
	binary.BigEndian.PutUint16(entrada[0:], 0x0112)
	binary.BigEndian.PutUint16(entrada[2:], 3)
	binary.BigEndian.PutUint32(entrada[4:], 1)
	binary.BigEndian.PutUint16(entrada[8:], o)
	tiff = append(tiff, entrada...)
	tiff = append(tiff, 0, 0, 0, 0)

	cuerpo := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(cuerpo)+2))
	seg = append(seg, cuerpo...)

	salida := append([]byte{}, jpg[:2]...)
	salida = append(salida, seg...)
	return append(salida, jpg[2:]...)
}

func TestNormalizarReduceFotosGrandes(t *testing.T) {
	datos, ext, err := NormalizarImagen(bytes.NewReader(jpegDe(t, 3000, 2000)))
	if err != nil {
		t.Fatal(err)
	}
	if ext != ".jpg" {
		t.Fatalf("extensión %q, quería .jpg", ext)
	}
	if w, h := dimensiones(t, datos); w != LadoMaximo || h != 800 {
		t.Fatalf("quedó en %dx%d, quería 1200x800", w, h)
	}
}

func TestNormalizarReduceFotosVerticales(t *testing.T) {
	datos, _, err := NormalizarImagen(bytes.NewReader(jpegDe(t, 1500, 3000)))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := dimensiones(t, datos); w != 600 || h != LadoMaximo {
		t.Fatalf("quedó en %dx%d, quería 600x1200", w, h)
	}
}

func TestNormalizarNoAmpliaFotosPequenas(t *testing.T) {
	datos, _, err := NormalizarImagen(bytes.NewReader(jpegDe(t, 300, 200)))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := dimensiones(t, datos); w != 300 || h != 200 {
		t.Fatalf("quedó en %dx%d, quería 300x200", w, h)
	}
}

func TestNormalizarPNGConservaTransparencia(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	img.Set(5, 5, color.NRGBA{R: 255, A: 128})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	datos, ext, err := NormalizarImagen(&b)
	if err != nil {
		t.Fatal(err)
	}
	if ext != ".png" {
		t.Fatalf("extensión %q, quería .png", ext)
	}
	salida, err := png.Decode(bytes.NewReader(datos))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := salida.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("el fondo debía seguir transparente, alfa %d", a)
	}
}

func TestNormalizarAceptaWebPYLoGuardaComoJPEG(t *testing.T) {
	webp, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	datos, ext, err := NormalizarImagen(bytes.NewReader(webp))
	if err != nil {
		t.Fatalf("un WebP válido debía aceptarse: %v", err)
	}
	if ext != ".jpg" {
		t.Fatalf("extensión %q, quería .jpg", ext)
	}
	dimensiones(t, datos)
}

func TestNormalizarRechazaLoQueNoEsImagen(t *testing.T) {
	casos := map[string][]byte{
		"html":       []byte("<html><script>alert(1)</script></html>"),
		"texto":      []byte("hola, esto no es una foto"),
		"vacío":      {},
		"svg":        []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"gif":        []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
		"ejecutable": []byte("MZ\x90\x00\x03\x00\x00\x00"),
	}
	for nombre, contenido := range casos {
		if _, _, err := NormalizarImagen(bytes.NewReader(contenido)); !errors.Is(err, ErrImagenInvalida) {
			t.Errorf("%s: error %v, quería ErrImagenInvalida", nombre, err)
		}
	}
}

func TestNormalizarRechazaImagenesTruncadas(t *testing.T) {
	jpg := jpegDe(t, 200, 200)
	if _, _, err := NormalizarImagen(bytes.NewReader(jpg[:len(jpg)/3])); !errors.Is(err, ErrImagenInvalida) {
		t.Fatalf("un JPEG cortado debía rechazarse, error: %v", err)
	}
}

func TestNormalizarRechazaArchivosDemasiadoGrandes(t *testing.T) {
	grande := append(jpegDe(t, 10, 10), make([]byte, MaxBytesImagen)...)
	if _, _, err := NormalizarImagen(bytes.NewReader(grande)); !errors.Is(err, ErrImagenGrande) {
		t.Fatalf("error %v, quería ErrImagenGrande", err)
	}
}

// Un PNG diminuto que declara 60000x60000 píxeles no debe llegar a decodificarse.
func TestNormalizarRechazaBombasDeDescompresion(t *testing.T) {
	chunk := func(tipo string, datos []byte) []byte {
		var b bytes.Buffer
		binary.Write(&b, binary.BigEndian, uint32(len(datos)))
		b.WriteString(tipo)
		b.Write(datos)
		binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(tipo), datos...)))
		return b.Bytes()
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 60000)
	binary.BigEndian.PutUint32(ihdr[4:], 60000)
	ihdr[8], ihdr[9] = 8, 2

	bomba := []byte("\x89PNG\r\n\x1a\n")
	bomba = append(bomba, chunk("IHDR", ihdr)...)
	bomba = append(bomba, chunk("IEND", nil)...)

	if _, _, err := NormalizarImagen(bytes.NewReader(bomba)); !errors.Is(err, ErrImagenInvalida) {
		t.Fatalf("error %v, quería ErrImagenInvalida", err)
	}
}

func TestNormalizarRespetaLaOrientacionEXIF(t *testing.T) {
	// Una foto de 40x20 con orientación 6 (girar 90° a la derecha) debe quedar
	// vertical: 20x40.
	datos, _, err := NormalizarImagen(bytes.NewReader(conOrientacion(jpegDe(t, 40, 20), 6)))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := dimensiones(t, datos); w != 20 || h != 40 {
		t.Fatalf("quedó en %dx%d, quería 20x40", w, h)
	}
	// Orientación 3 (180°) no cambia las dimensiones.
	datos, _, err = NormalizarImagen(bytes.NewReader(conOrientacion(jpegDe(t, 40, 20), 3)))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := dimensiones(t, datos); w != 40 || h != 20 {
		t.Fatalf("quedó en %dx%d, quería 40x20", w, h)
	}
}

func TestAplicarOrientacionMueveLosPixelesALugarCorrecto(t *testing.T) {
	// 2x1: izquierda roja, derecha azul.
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	rojo, azul := color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}
	img.Set(0, 0, rojo)
	img.Set(1, 0, azul)

	// 6 = girar 90° a la derecha: queda 1x2 con el rojo arriba.
	giro := aplicarOrientacion(img, 6)
	if giro.Bounds().Dx() != 1 || giro.Bounds().Dy() != 2 {
		t.Fatalf("dimensiones %v", giro.Bounds())
	}
	if giro.At(0, 0) != color.Color(rojo) || giro.At(0, 1) != color.Color(azul) {
		t.Fatalf("orientación 6 mal aplicada: %v %v", giro.At(0, 0), giro.At(0, 1))
	}
	// 8 = girar 90° a la izquierda: el azul arriba.
	giro = aplicarOrientacion(img, 8)
	if giro.At(0, 0) != color.Color(azul) || giro.At(0, 1) != color.Color(rojo) {
		t.Fatalf("orientación 8 mal aplicada: %v %v", giro.At(0, 0), giro.At(0, 1))
	}
	// 2 = espejo horizontal.
	giro = aplicarOrientacion(img, 2)
	if giro.At(0, 0) != color.Color(azul) || giro.At(1, 0) != color.Color(rojo) {
		t.Fatalf("orientación 2 mal aplicada")
	}
}

func TestOrientacionEXIFConDatosMalFormadosNoFalla(t *testing.T) {
	jpg := jpegDe(t, 10, 10)
	for _, basura := range [][]byte{nil, {0xFF}, {0xFF, 0xD8}, {0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF}, jpg[:30]} {
		if o := orientacionEXIF(basura); o != 1 {
			t.Errorf("datos mal formados dieron orientación %d", o)
		}
	}
	// Segmento Exif truncado a mitad de la tabla.
	roto := conOrientacion(jpg, 6)
	if o := orientacionEXIF(roto[:20]); o != 1 {
		t.Errorf("EXIF truncado dio orientación %d", o)
	}
}

func TestLocalSaveIgnoraLaExtensionDelNombre(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLocal(dir, "/media")
	if err != nil {
		t.Fatal(err)
	}
	ruta, err := l.Save("trampa.html", bytes.NewReader(jpegDe(t, 50, 50)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(ruta, ".jpg") {
		t.Fatalf("la ruta %q debía terminar en .jpg", ruta)
	}
	if _, err := os.Stat(filepath.Join(dir, ruta)); err != nil {
		t.Fatalf("el archivo no se guardó: %v", err)
	}
}

func TestLocalSaveNoDejaArchivosSiLaImagenEsInvalida(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir, "/media")
	if _, err := l.Save("virus.jpg", strings.NewReader("<script>alert(1)</script>")); !errors.Is(err, ErrImagenInvalida) {
		t.Fatalf("error %v, quería ErrImagenInvalida", err)
	}
	if entradas, _ := os.ReadDir(dir); len(entradas) != 0 {
		t.Fatalf("quedaron %d archivos en disco", len(entradas))
	}
}
