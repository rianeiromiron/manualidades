package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// DBConfig contiene los parámetros de conexión a Postgres.
// Vive en un archivo JSON en disco (no en la propia base de datos)
// para poder cambiarlos sin depender de una conexión ya existente.
type DBConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
	SSLMode  string `json:"sslmode"`
}

const filePath = "config.json"

// Default devuelve valores de ejemplo apuntando al contenedor Docker local.
func Default() DBConfig {
	return DBConfig{
		Host:     "localhost",
		Port:     5432,
		User:     "postgres",
		Password: "",
		DBName:   "manualidades",
		SSLMode:  "disable",
	}
}

// Load lee config.json. Si no existe, devuelve los valores por defecto
// (sin crear el archivo todavía).
func Load() (DBConfig, error) {
	data, err := os.ReadFile(filePath)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return DBConfig{}, fmt.Errorf("leyendo %s: %w", filePath, err)
	}

	var cfg DBConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DBConfig{}, fmt.Errorf("parseando %s: %w", filePath, err)
	}
	return cfg, nil
}

// Save escribe la configuración en config.json.
func Save(cfg DBConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serializando config: %w", err)
	}
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return fmt.Errorf("escribiendo %s: %w", filePath, err)
	}
	return nil
}

// DSN construye el connection string para lib/pq.
func (c DBConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode,
	)
}

// DSNMaintenance construye un DSN contra la base "postgres" (siempre
// existe en un servidor Postgres) para poder crear la base de datos
// de destino si todavía no existe.
func (c DBConfig) DSNMaintenance() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=postgres sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.SSLMode,
	)
}
