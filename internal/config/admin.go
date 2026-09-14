package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

// AdminConfig protege el panel de administración. Vive en su propio
// archivo (separado de config.json) para no mezclar credenciales de la
// aplicación con las de Postgres.
type AdminConfig struct {
	Usuario      string `json:"usuario"`
	PasswordHash string `json:"password_hash"`
	// SecretKey firma las cookies de sesión (HMAC-SHA256). Generada una
	// sola vez al crear el usuario admin; cambiarla invalida todas las
	// sesiones activas.
	SecretKey string `json:"secret_key"`
}

const adminFilePath = "admin.json"

// AdminExists indica si ya se completó la configuración inicial del acceso.
// Mientras no exista, /admin/setup queda abierto para crear el primer
// usuario; una vez existe, /admin/setup deja de ser accesible.
func AdminExists() bool {
	_, err := os.Stat(adminFilePath)
	return err == nil
}

func LoadAdmin() (AdminConfig, error) {
	data, err := os.ReadFile(adminFilePath)
	if err != nil {
		return AdminConfig{}, fmt.Errorf("leyendo %s: %w", adminFilePath, err)
	}
	var cfg AdminConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AdminConfig{}, fmt.Errorf("parseando %s: %w", adminFilePath, err)
	}
	return cfg, nil
}

func saveAdmin(cfg AdminConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serializando admin config: %w", err)
	}
	if err := os.WriteFile(adminFilePath, data, 0o600); err != nil {
		return fmt.Errorf("escribiendo %s: %w", adminFilePath, err)
	}
	return nil
}

// CreateAdmin crea el primer (y único) usuario admin: hashea la
// contraseña con bcrypt y genera una llave de firma aleatoria para las
// cookies de sesión. Falla si ya existe un admin configurado.
func CreateAdmin(usuario, password string) error {
	if AdminExists() {
		return fmt.Errorf("ya existe un usuario administrador configurado")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("generando hash de contraseña: %w", err)
	}
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return fmt.Errorf("generando llave de sesión: %w", err)
	}
	return saveAdmin(AdminConfig{
		Usuario:      usuario,
		PasswordHash: string(hash),
		SecretKey:    base64.StdEncoding.EncodeToString(keyBytes),
	})
}

// VerifyPassword compara una contraseña en texto plano contra el hash
// guardado.
func (c AdminConfig) VerifyPassword(password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(password)) == nil
}

// UpdatePassword genera un nuevo hash para la contraseña y, a propósito,
// también una nueva llave de firma de sesión: cambiar la contraseña
// invalida todas las sesiones activas (incluida la que se usó para
// cambiarla), forzando a iniciar sesión de nuevo.
func UpdatePassword(current AdminConfig, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("generando hash de contraseña: %w", err)
	}
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return fmt.Errorf("generando llave de sesión: %w", err)
	}
	current.PasswordHash = string(hash)
	current.SecretKey = base64.StdEncoding.EncodeToString(keyBytes)
	return saveAdmin(current)
}
