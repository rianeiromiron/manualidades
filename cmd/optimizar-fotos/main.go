// Command optimizar-fotos reduce las fotos ya guardadas con las mismas reglas
// que se aplican al subir una foto nueva (storage.NormalizarImagen): lado
// mayor de 1200 px como máximo, metadatos fuera, orientación respetada.
//
// Nunca modifica la carpeta de origen: escribe el resultado en otra carpeta,
// con los mismos nombres de archivo, para que la base de datos (que guarda
// solo el nombre) siga apuntando a las fotos sin tocar nada. Si una foto no se
// puede reducir con seguridad, se copia tal cual:
//   - si el resultado no es más pequeño que el original;
//   - si el tipo real no coincide con la extensión (p. ej. un WebP, que se
//     guardaría como JPEG): el servidor decide el tipo por la extensión.
//
// Uso:
//
//	go run ./cmd/optimizar-fotos -origen media/productos -destino salida/productos
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"manualidades/internal/storage"
)

type resultado int

const (
	reducida resultado = iota
	copiadaIgual
	copiadaTipoDistinto
	copiadaError
)

func main() {
	origen := flag.String("origen", "", "carpeta con las fotos originales (no se modifica)")
	destino := flag.String("destino", "", "carpeta donde se escriben las fotos reducidas")
	flag.Parse()

	if *origen == "" || *destino == "" {
		flag.Usage()
		os.Exit(2)
	}
	absO, errO := filepath.Abs(*origen)
	absD, errD := filepath.Abs(*destino)
	if errO != nil || errD != nil {
		log.Fatal("no se pudieron resolver las rutas")
	}
	if absO == absD {
		log.Fatal("el destino debe ser una carpeta distinta del origen: este comando nunca sobrescribe los originales")
	}
	if err := os.MkdirAll(absD, 0o755); err != nil {
		log.Fatalf("creando %s: %v", absD, err)
	}

	entradas, err := os.ReadDir(absO)
	if err != nil {
		log.Fatalf("leyendo %s: %v", absO, err)
	}

	var cuentas [4]int
	var antes, despues int64
	for _, e := range entradas {
		if e.IsDir() {
			continue
		}
		datos, err := os.ReadFile(filepath.Join(absO, e.Name()))
		if err != nil {
			log.Fatalf("leyendo %s: %v", e.Name(), err)
		}
		salida, r, motivo := procesar(e.Name(), datos)
		if err := os.WriteFile(filepath.Join(absD, e.Name()), salida, 0o644); err != nil {
			log.Fatalf("escribiendo %s: %v", e.Name(), err)
		}
		cuentas[r]++
		antes += int64(len(datos))
		despues += int64(len(salida))
		if r == copiadaTipoDistinto || r == copiadaError {
			fmt.Printf("  copiada sin cambios: %s (%s)\n", e.Name(), motivo)
		}
	}

	fmt.Printf("\nfotos reducidas:               %d\n", cuentas[reducida])
	fmt.Printf("copiadas (ya eran pequeñas):   %d\n", cuentas[copiadaIgual])
	fmt.Printf("copiadas (tipo distinto):      %d\n", cuentas[copiadaTipoDistinto])
	fmt.Printf("copiadas (con error):          %d\n", cuentas[copiadaError])
	fmt.Printf("tamaño total: %.1f MB -> %.1f MB (%.0f%% menos)\n",
		float64(antes)/(1<<20), float64(despues)/(1<<20), 100*(1-float64(despues)/float64(max(antes, 1))))
}

// procesar devuelve los bytes que deben quedar con el nombre dado: la foto
// reducida si es segura y más pequeña, o los originales en otro caso.
func procesar(nombre string, original []byte) ([]byte, resultado, string) {
	salida, ext, err := storage.NormalizarImagen(bytes.NewReader(original))
	if err != nil {
		return original, copiadaError, err.Error()
	}
	if !extensionCompatible(nombre, ext) {
		return original, copiadaTipoDistinto, "el archivo es " + ext + " pero se llama " + filepath.Ext(nombre)
	}
	if len(salida) >= len(original) {
		return original, copiadaIgual, ""
	}
	return salida, reducida, ""
}

// extensionCompatible indica si el resultado (".jpg" o ".png") puede guardarse
// con el nombre original sin que el servidor lo sirva con un tipo equivocado.
func extensionCompatible(nombre, extSalida string) bool {
	switch strings.ToLower(filepath.Ext(nombre)) {
	case ".jpg", ".jpeg":
		return extSalida == ".jpg"
	case ".png":
		return extSalida == ".png"
	}
	return false
}
