package web

import (
	"testing"
	"time"
)

func TestTokenDeSesionLlevaElIDFirmado(t *testing.T) {
	expiry := time.Now().Add(time.Hour).Unix()
	llave := "c2VjcmV0by1kZS1wcnVlYmE="
	tok := buildSessionToken("user", "7", "abc123", llave, expiry)

	kind, ident, sid, exp, payload, sig, ok := decodeSessionToken(tok)
	if !ok || kind != "user" || ident != "7" || sid != "abc123" || exp != expiry {
		t.Fatalf("decodificación incorrecta: %v %v %v %v %v", ok, kind, ident, sid, exp)
	}
	if sig != computeSignature(payload, llave) {
		t.Error("la firma no corresponde al payload")
	}
}

func TestTokenSinIDNoEsValido(t *testing.T) {
	// Formato anterior a las sesiones individuales: "kind|ident|expiry".
	viejo := "dXNlcnw3fDEyMzQ1Ng.firma" // user|7|123456
	if _, _, _, _, _, _, ok := decodeSessionToken(viejo); ok {
		t.Error("un token sin id de sesión no debería aceptarse")
	}
}
