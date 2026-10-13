package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registra el decodificador WebP
)

// Errores que NormalizarImagen devuelve por entradas del usuario. Sus
// mensajes están pensados para mostrarse tal cual.
var (
	ErrImagenInvalida = errors.New("el archivo no es una imagen válida: usa JPG, PNG o WebP")
	ErrImagenGrande   = errors.New("la imagen pesa demasiado: el máximo es 10 MB")
)

const (
	// MaxBytesImagen es el tamaño máximo de un archivo subido.
	MaxBytesImagen = 10 << 20
	// LadoMaximo es el lado más largo, en píxeles, con que se guarda una
	// imagen. Las más grandes se reducen; las más pequeñas no se amplían.
	LadoMaximo = 1200
	// maxPixeles evita las "bombas de descompresión": un archivo pequeño que
	// declara dimensiones enormes y agota la memoria al decodificarlo.
	maxPixeles  = 50_000_000
	calidadJPEG = 85
)

// NormalizarImagen valida y reprocesa una imagen subida por un usuario.
//
//   - Decide el tipo por el contenido real, no por el nombre ni por lo que
//     declare el navegador: solo acepta JPEG, PNG y WebP.
//   - La decodifica y la vuelve a codificar, lo que descarta metadatos (EXIF
//     con ubicación GPS) y cualquier dato añadido tras la imagen.
//   - Reduce el lado más largo a LadoMaximo píxeles, sin ampliar.
//   - Respeta la orientación EXIF de los JPEG de móvil antes de descartarla.
//
// Devuelve los bytes a guardar y la extensión (".jpg" o ".png"). Un PNG sigue
// siendo PNG para conservar la transparencia; JPEG y WebP se guardan como JPEG
// (la transparencia de un WebP se aplana sobre blanco).
func NormalizarImagen(r io.Reader) ([]byte, string, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxBytesImagen+1))
	if err != nil {
		return nil, "", err
	}
	if len(raw) > MaxBytesImagen {
		return nil, "", ErrImagenGrande
	}

	tipo := http.DetectContentType(raw)
	if tipo != "image/jpeg" && tipo != "image/png" && tipo != "image/webp" {
		return nil, "", ErrImagenInvalida
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixeles {
		return nil, "", ErrImagenInvalida
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", ErrImagenInvalida
	}

	// Primero se reduce y luego se gira: girar una imagen ya pequeña es mucho
	// más barato que girar una foto de 12 megapíxeles.
	img = reducir(img)
	if tipo == "image/jpeg" {
		img = aplicarOrientacion(img, orientacionEXIF(raw))
	}

	var salida bytes.Buffer
	if tipo == "image/png" {
		if err := png.Encode(&salida, img); err != nil {
			return nil, "", err
		}
		return salida.Bytes(), ".png", nil
	}
	if err := jpeg.Encode(&salida, sobreBlanco(img), &jpeg.Options{Quality: calidadJPEG}); err != nil {
		return nil, "", err
	}
	return salida.Bytes(), ".jpg", nil
}

func reducir(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= LadoMaximo && h <= LadoMaximo {
		return img
	}
	nw, nh := LadoMaximo, h*LadoMaximo/w
	if h > w {
		nw, nh = w*LadoMaximo/h, LadoMaximo
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// sobreBlanco aplana la imagen sobre fondo blanco, porque JPEG no tiene
// transparencia y el negro por defecto se vería mal en una foto de producto.
func sobreBlanco(img image.Image) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// orientacionEXIF devuelve la etiqueta de orientación (1 a 8) de un JPEG, o 1
// si no la tiene o está mal formada. Solo lee lo necesario del segmento APP1.
func orientacionEXIF(jpg []byte) int {
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(jpg) {
		if jpg[i] != 0xFF {
			return 1
		}
		marca := jpg[i+1]
		if marca == 0xFF { // relleno
			i++
			continue
		}
		if marca == 0xDA || marca == 0xD9 { // empieza la imagen: ya no hay metadatos
			return 1
		}
		largo := int(binary.BigEndian.Uint16(jpg[i+2 : i+4]))
		if largo < 2 || i+2+largo > len(jpg) {
			return 1
		}
		if marca == 0xE1 {
			seg := jpg[i+4 : i+2+largo]
			if bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
				return orientacionTIFF(seg[6:])
			}
		}
		i += 2 + largo
	}
	return 1
}

func orientacionTIFF(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var orden binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		orden = binary.LittleEndian
	case "MM":
		orden = binary.BigEndian
	default:
		return 1
	}
	if orden.Uint16(t[2:4]) != 42 {
		return 1
	}
	ifd := int(orden.Uint32(t[4:8]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := int(orden.Uint16(t[ifd : ifd+2]))
	for k := 0; k < n; k++ {
		e := ifd + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if orden.Uint16(t[e:e+2]) == 0x0112 {
			if v := int(orden.Uint16(t[e+8 : e+10])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// aplicarOrientacion deja la imagen como la mostraría un visor que respeta
// EXIF. Los valores son los de la etiqueta Orientation (1 = sin cambios).
func aplicarOrientacion(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			dst.Set(x, y, img.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}
