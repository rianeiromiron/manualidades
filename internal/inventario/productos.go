package inventario

import (
	"database/sql"

	"github.com/lib/pq"
)

type Producto struct {
	ID              int
	CategoriaID     int
	CategoriaNombre string
	Nombre          string
	Descripcion     string
	PrecioCompra    float64
	PrecioVenta     float64
	Activo          bool
	Stock           float64 // existencia física según el kardex
	// Disponible es Stock menos lo reservado por pedidos en pago. Lo calcula
	// la tienda (tienda.ReservadoPorProducto); ListProductos/GetProducto lo
	// dejan en 0 y el admin sigue usando Stock.
	Disponible  float64
	FotoPortada string   // ruta de la primera foto, si existe
	Fotos       []string // rutas de todas las fotos, en orden
}

const productoListaSQL = `
SELECT
	p.id, p.categoria_id, c.nombre, p.nombre, p.descripcion,
	p.precio_compra, p.precio_venta, p.activo,
	COALESCE(m.stock, 0) AS stock,
	COALESCE(f.ruta, '') AS foto_portada,
	COALESCE(todas.rutas, '{}') AS fotos
FROM productos p
JOIN categorias c ON c.id = p.categoria_id
LEFT JOIN (
	SELECT producto_id,
		SUM(CASE WHEN tipo = 'ingreso' THEN cantidad ELSE -cantidad END) AS stock
	FROM movimientos_inventario
	GROUP BY producto_id
) m ON m.producto_id = p.id
LEFT JOIN LATERAL (
	SELECT ruta FROM producto_fotos
	WHERE producto_id = p.id
	ORDER BY orden, id
	LIMIT 1
) f ON true
LEFT JOIN LATERAL (
	SELECT array_agg(ruta ORDER BY orden, id) AS rutas
	FROM producto_fotos
	WHERE producto_id = p.id
) todas ON true
`

func ListProductos(conn *sql.DB) ([]Producto, error) {
	rows, err := conn.Query(productoListaSQL + ` ORDER BY p.nombre`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProductos(rows)
}

func GetProducto(conn *sql.DB, id int) (Producto, error) {
	rows, err := conn.Query(productoListaSQL+` WHERE p.id = $1`, id)
	if err != nil {
		return Producto{}, err
	}
	defer rows.Close()

	list, err := scanProductos(rows)
	if err != nil {
		return Producto{}, err
	}
	if len(list) == 0 {
		return Producto{}, sql.ErrNoRows
	}
	return list[0], nil
}

func scanProductos(rows *sql.Rows) ([]Producto, error) {
	var out []Producto
	for rows.Next() {
		var p Producto
		if err := rows.Scan(
			&p.ID, &p.CategoriaID, &p.CategoriaNombre, &p.Nombre, &p.Descripcion,
			&p.PrecioCompra, &p.PrecioVenta, &p.Activo, &p.Stock, &p.FotoPortada,
			pq.Array(&p.Fotos),
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func CreateProducto(conn *sql.DB, p Producto) (int, error) {
	var id int
	err := conn.QueryRow(
		`INSERT INTO productos (categoria_id, nombre, descripcion, precio_compra, precio_venta, activo)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		p.CategoriaID, p.Nombre, p.Descripcion, p.PrecioCompra, p.PrecioVenta, p.Activo,
	).Scan(&id)
	return id, err
}

func UpdateProducto(conn *sql.DB, p Producto) error {
	_, err := conn.Exec(
		`UPDATE productos SET categoria_id = $1, nombre = $2, descripcion = $3,
		 precio_compra = $4, precio_venta = $5, activo = $6 WHERE id = $7`,
		p.CategoriaID, p.Nombre, p.Descripcion, p.PrecioCompra, p.PrecioVenta, p.Activo, p.ID,
	)
	return err
}

func DeleteProducto(conn *sql.DB, id int) error {
	_, err := conn.Exec(`DELETE FROM productos WHERE id = $1`, id)
	return err
}
