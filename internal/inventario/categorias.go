package inventario

import "database/sql"

type Categoria struct {
	ID          int
	Nombre      string
	Descripcion string
}

func ListCategorias(conn *sql.DB) ([]Categoria, error) {
	rows, err := conn.Query(`SELECT id, nombre, descripcion FROM categorias ORDER BY nombre`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Categoria
	for rows.Next() {
		var c Categoria
		if err := rows.Scan(&c.ID, &c.Nombre, &c.Descripcion); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func GetCategoria(conn *sql.DB, id int) (Categoria, error) {
	var c Categoria
	err := conn.QueryRow(`SELECT id, nombre, descripcion FROM categorias WHERE id = $1`, id).
		Scan(&c.ID, &c.Nombre, &c.Descripcion)
	return c, err
}

func CreateCategoria(conn *sql.DB, nombre, descripcion string) error {
	_, err := conn.Exec(`INSERT INTO categorias (nombre, descripcion) VALUES ($1, $2)`, nombre, descripcion)
	return err
}

func UpdateCategoria(conn *sql.DB, id int, nombre, descripcion string) error {
	_, err := conn.Exec(`UPDATE categorias SET nombre = $1, descripcion = $2 WHERE id = $3`, nombre, descripcion, id)
	return err
}

func DeleteCategoria(conn *sql.DB, id int) error {
	_, err := conn.Exec(`DELETE FROM categorias WHERE id = $1`, id)
	return err
}
