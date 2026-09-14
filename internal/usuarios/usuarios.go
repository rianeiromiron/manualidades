package usuarios

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const (
	RolSuperusuario   = "superusuario"
	RolAdministrativo = "administrativo"
)

var ErrRolInvalido = errors.New("rol de usuario inválido")

type Usuario struct {
	ID           int
	Usuario      string
	PasswordHash string
	SecretKey    string
	Rol          string
	Activo       bool
}

func esRolValido(rol string) bool {
	return rol == RolSuperusuario || rol == RolAdministrativo
}

func ListUsuarios(conn *sql.DB) ([]Usuario, error) {
	rows, err := conn.Query(`SELECT id, usuario, password_hash, secret_key, rol, activo FROM usuarios ORDER BY usuario`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Usuario
	for rows.Next() {
		var u Usuario
		if err := rows.Scan(&u.ID, &u.Usuario, &u.PasswordHash, &u.SecretKey, &u.Rol, &u.Activo); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func GetUsuario(conn *sql.DB, id int) (Usuario, error) {
	var u Usuario
	err := conn.QueryRow(`SELECT id, usuario, password_hash, secret_key, rol, activo FROM usuarios WHERE id = $1`, id).
		Scan(&u.ID, &u.Usuario, &u.PasswordHash, &u.SecretKey, &u.Rol, &u.Activo)
	return u, err
}

func GetUsuarioPorNombre(conn *sql.DB, usuario string) (Usuario, error) {
	var u Usuario
	err := conn.QueryRow(`SELECT id, usuario, password_hash, secret_key, rol, activo FROM usuarios WHERE usuario = $1`, usuario).
		Scan(&u.ID, &u.Usuario, &u.PasswordHash, &u.SecretKey, &u.Rol, &u.Activo)
	return u, err
}

func generarSecretKey() (string, error) {
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", fmt.Errorf("generando llave de sesión: %w", err)
	}
	return base64.StdEncoding.EncodeToString(keyBytes), nil
}

// CreateUsuario crea un usuario nuevo (superusuario o administrativo) y,
// si es administrativo, le asigna de una vez el conjunto de módulos dado.
func CreateUsuario(conn *sql.DB, usuario, password, rol string, moduloIDs []int) (int, error) {
	if !esRolValido(rol) {
		return 0, ErrRolInvalido
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf("generando hash de contraseña: %w", err)
	}
	secretKey, err := generarSecretKey()
	if err != nil {
		return 0, err
	}

	tx, err := conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var id int
	err = tx.QueryRow(
		`INSERT INTO usuarios (usuario, password_hash, secret_key, rol) VALUES ($1, $2, $3, $4) RETURNING id`,
		usuario, string(hash), secretKey, rol,
	).Scan(&id)
	if err != nil {
		return 0, err
	}

	if rol == RolAdministrativo {
		if err := setModulosTx(tx, id, moduloIDs); err != nil {
			return 0, err
		}
	}

	return id, tx.Commit()
}

// UpdateUsuario actualiza rol y módulos asignados. La contraseña solo se
// cambia si nuevaPassword no es nil (dejar el campo en blanco en el
// formulario significa "no cambiarla"); al cambiarla, también rota
// secret_key, cerrando únicamente la sesión de ese usuario.
func UpdateUsuario(conn *sql.DB, id int, rol string, moduloIDs []int, nuevaPassword *string) error {
	if !esRolValido(rol) {
		return ErrRolInvalido
	}

	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if nuevaPassword != nil {
		hash, err := bcrypt.GenerateFromPassword([]byte(*nuevaPassword), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("generando hash de contraseña: %w", err)
		}
		secretKey, err := generarSecretKey()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE usuarios SET rol = $1, password_hash = $2, secret_key = $3 WHERE id = $4`,
			rol, string(hash), secretKey, id); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`UPDATE usuarios SET rol = $1 WHERE id = $2`, rol, id); err != nil {
			return err
		}
	}

	if rol == RolAdministrativo {
		if err := setModulosTx(tx, id, moduloIDs); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`DELETE FROM usuario_modulos WHERE usuario_id = $1`, id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func setModulosTx(tx *sql.Tx, usuarioID int, moduloIDs []int) error {
	if _, err := tx.Exec(`DELETE FROM usuario_modulos WHERE usuario_id = $1`, usuarioID); err != nil {
		return err
	}
	for _, moduloID := range moduloIDs {
		if _, err := tx.Exec(`INSERT INTO usuario_modulos (usuario_id, modulo_id) VALUES ($1, $2)`, usuarioID, moduloID); err != nil {
			return err
		}
	}
	return nil
}

func SetActivo(conn *sql.DB, id int, activo bool) error {
	_, err := conn.Exec(`UPDATE usuarios SET activo = $1 WHERE id = $2`, activo, id)
	return err
}

func DeleteUsuario(conn *sql.DB, id int) error {
	_, err := conn.Exec(`DELETE FROM usuarios WHERE id = $1`, id)
	return err
}

// ModulosAsignados devuelve los IDs de módulo asignados a un usuario
// administrativo (vacío para superusuario, que no los necesita).
func ModulosAsignados(conn *sql.DB, usuarioID int) ([]int, error) {
	rows, err := conn.Query(`SELECT modulo_id FROM usuario_modulos WHERE usuario_id = $1`, usuarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ModulosClavesAsignadas es lo mismo que ModulosAsignados pero devuelve
// las claves ('inventario', 'sitio', ...) en vez de los IDs — lo que
// necesita el middleware de permisos para comparar contra la ruta.
func ModulosClavesAsignadas(conn *sql.DB, usuarioID int) ([]string, error) {
	rows, err := conn.Query(`
		SELECT m.clave FROM modulos m
		JOIN usuario_modulos um ON um.modulo_id = m.id
		WHERE um.usuario_id = $1`, usuarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var clave string
		if err := rows.Scan(&clave); err != nil {
			return nil, err
		}
		out = append(out, clave)
	}
	return out, rows.Err()
}

func VerifyPassword(u Usuario, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}

// UpdatePropiaPassword deja que un usuario cambie su propia contraseña
// (flujo de /admin/cambiar-password). Rota secret_key igual que
// UpdateUsuario, así que cierra su sesión actual.
func UpdatePropiaPassword(conn *sql.DB, id int, nuevaPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(nuevaPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("generando hash de contraseña: %w", err)
	}
	secretKey, err := generarSecretKey()
	if err != nil {
		return err
	}
	_, err = conn.Exec(`UPDATE usuarios SET password_hash = $1, secret_key = $2 WHERE id = $3`, string(hash), secretKey, id)
	return err
}
