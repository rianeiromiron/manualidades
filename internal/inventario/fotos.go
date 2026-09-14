package inventario

import "database/sql"

type Foto struct {
	ID         int
	ProductoID int
	Ruta       string
	Orden      int
}

func ListFotos(conn *sql.DB, productoID int) ([]Foto, error) {
	rows, err := conn.Query(
		`SELECT id, producto_id, ruta, orden FROM producto_fotos WHERE producto_id = $1 ORDER BY orden, id`,
		productoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Foto
	for rows.Next() {
		var f Foto
		if err := rows.Scan(&f.ID, &f.ProductoID, &f.Ruta, &f.Orden); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func AddFoto(conn *sql.DB, productoID int, ruta string, orden int) error {
	_, err := conn.Exec(
		`INSERT INTO producto_fotos (producto_id, ruta, orden) VALUES ($1, $2, $3)`,
		productoID, ruta, orden,
	)
	return err
}

// DeleteFoto borra el registro y devuelve la ruta guardada para que el
// llamador también pueda borrar el archivo físico.
func DeleteFoto(conn *sql.DB, id int) (string, error) {
	var ruta string
	err := conn.QueryRow(`DELETE FROM producto_fotos WHERE id = $1 RETURNING ruta`, id).Scan(&ruta)
	return ruta, err
}

func CountFotos(conn *sql.DB, productoID int) (int, error) {
	var n int
	err := conn.QueryRow(`SELECT COUNT(*) FROM producto_fotos WHERE producto_id = $1`, productoID).Scan(&n)
	return n, err
}
