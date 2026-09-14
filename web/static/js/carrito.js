// Carrito de compras de la tienda pública. Vive en localStorage del
// navegador del cliente; solo se envía al servidor completo al pagar
// (POST /checkout/confirmar). Ninguna otra página del sitio depende de
// sesiones de servidor para el carrito.
window.Carrito = (function () {
  var CLAVE = 'manualidades_carrito';

  function obtener() {
    try {
      var raw = localStorage.getItem(CLAVE);
      return raw ? JSON.parse(raw) : [];
    } catch (e) {
      return [];
    }
  }

  function guardar(items) {
    try {
      localStorage.setItem(CLAVE, JSON.stringify(items));
    } catch (e) {
      // almacenamiento no disponible (modo privado, etc.): el carrito
      // simplemente no persistirá entre recargas.
    }
    actualizarContador();
  }

  function agregar(item, cantidad) {
    cantidad = cantidad || 1;
    var items = obtener();
    var existente = items.find(function (i) { return i.productoId === item.productoId; });
    if (existente) {
      existente.cantidad += cantidad;
    } else {
      items.push({
        productoId: item.productoId,
        nombre: item.nombre,
        precio: item.precio,
        foto: item.foto || '',
        stock: item.stock,
        cantidad: cantidad
      });
    }
    guardar(items);
  }

  function actualizarCantidad(productoId, cantidad) {
    var items = obtener();
    items = items.map(function (i) {
      if (i.productoId === productoId) i.cantidad = cantidad;
      return i;
    }).filter(function (i) { return i.cantidad > 0; });
    guardar(items);
  }

  function quitar(productoId) {
    var items = obtener().filter(function (i) { return i.productoId !== productoId; });
    guardar(items);
  }

  function vaciar() {
    guardar([]);
  }

  function totalCantidad(items) {
    items = items || obtener();
    return items.reduce(function (sum, i) { return sum + i.cantidad; }, 0);
  }

  function totalPrecio(items) {
    items = items || obtener();
    return items.reduce(function (sum, i) { return sum + i.cantidad * i.precio; }, 0);
  }

  function actualizarContador() {
    var el = document.getElementById('tCartCount');
    if (!el) return;
    var n = totalCantidad();
    el.textContent = n;
    el.hidden = n === 0;
  }

  document.addEventListener('DOMContentLoaded', actualizarContador);

  return {
    obtener: obtener,
    agregar: agregar,
    actualizarCantidad: actualizarCantidad,
    quitar: quitar,
    vaciar: vaciar,
    totalCantidad: totalCantidad,
    totalPrecio: totalPrecio,
    actualizarContador: actualizarContador
  };
})();
