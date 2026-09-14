package usuarios

import "database/sql"

// Modulo es una entrada del catálogo fijo de módulos asignables. No se
// editan desde la UI: viven en el código (ver schema.go) y solo se leen
// para armar el checklist de permisos de un usuario administrativo.
type Modulo struct {
	ID     int
	Clave  string
	Nombre string
	Orden  int
}

func ListModulos(conn *sql.DB) ([]Modulo, error) {
	rows, err := conn.Query(`SELECT id, clave, nombre, orden FROM modulos ORDER BY orden`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Modulo
	for rows.Next() {
		var m Modulo
		if err := rows.Scan(&m.ID, &m.Clave, &m.Nombre, &m.Orden); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
