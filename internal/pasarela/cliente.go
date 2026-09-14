// Package pasarela es el cliente HTTP hacia el supuesto proveedor externo
// de cobros con tarjeta (proyecto hermano "pasarela-simulada"). Manualidades
// nunca decide por sí misma si un pago se aprueba: siempre le pregunta a
// este proveedor.
package pasarela

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// ErrNoDisponible indica que el proveedor no respondió (caído, timeout, o
// un error de transporte) — distinto de un rechazo de negocio, que sí es
// una respuesta válida del proveedor.
var ErrNoDisponible = errors.New("la pasarela de pago no respondió")

// BaseURL es la URL del proveedor simulado. Se puede sobreescribir con la
// variable de entorno PASARELA_URL sin necesidad de recompilar.
func BaseURL() string {
	if url := os.Getenv("PASARELA_URL"); url != "" {
		return url
	}
	return "http://localhost:8091"
}

type SolicitudCobro struct {
	Monto         float64
	NumeroTarjeta string
	NombreTitular string
	Expiracion    string
	CVV           string
}

type RespuestaCobro struct {
	Aprobado   bool
	Referencia string
	Marca      string
	Ultimos4   string
	// Motivo solo tiene sentido cuando Aprobado es false.
	Motivo string
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Cobrar le pide al proveedor que procese el cobro. Un error de Go (casi
// siempre ErrNoDisponible) significa que el proveedor no contestó; un
// RespuestaCobro con Aprobado=false es una respuesta de negocio normal
// (tarjeta rechazada), no un error.
func Cobrar(sol SolicitudCobro) (RespuestaCobro, error) {
	body, err := json.Marshal(map[string]any{
		"monto":          sol.Monto,
		"numero_tarjeta": sol.NumeroTarjeta,
		"nombre_titular": sol.NombreTitular,
		"expiracion":     sol.Expiracion,
		"cvv":            sol.CVV,
	})
	if err != nil {
		return RespuestaCobro{}, fmt.Errorf("armando solicitud de cobro: %w", err)
	}

	resp, err := httpClient.Post(BaseURL()+"/cobros", "application/json", bytes.NewReader(body))
	if err != nil {
		return RespuestaCobro{}, ErrNoDisponible
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return RespuestaCobro{}, ErrNoDisponible
	}

	var out struct {
		Aprobado   bool   `json:"aprobado"`
		Referencia string `json:"referencia"`
		Marca      string `json:"marca"`
		Ultimos4   string `json:"ultimos4"`
		Motivo     string `json:"motivo"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return RespuestaCobro{}, ErrNoDisponible
	}

	return RespuestaCobro{
		Aprobado:   out.Aprobado,
		Referencia: out.Referencia,
		Marca:      out.Marca,
		Ultimos4:   out.Ultimos4,
		Motivo:     out.Motivo,
	}, nil
}
