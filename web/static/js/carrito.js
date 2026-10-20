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

  // limiteDe es lo máximo que se puede llevar de un producto: su stock disponible,
  // o infinito si no se conoce. El servidor vuelve a comprobarlo al pagar; esto
  // solo evita que el cliente se entere recién al final.
  function limiteDe(stock) {
    var n = Number(stock);
    return isFinite(n) ? Math.max(0, Math.floor(n)) : Infinity;
  }

  function cantidadEnCarrito(productoId) {
    var existente = obtener().find(function (i) { return i.productoId === productoId; });
    return existente ? existente.cantidad : 0;
  }

  // agregar suma al carrito sin pasar del stock disponible. Devuelve cuántas
  // unidades se agregaron de verdad, cuántas hay ya en el carrito y si se llegó al
  // máximo, para que la página pueda explicárselo al cliente.
  function agregar(item, cantidad) {
    cantidad = Math.max(1, Math.floor(cantidad || 1));
    var items = obtener();
    var existente = items.find(function (i) { return i.productoId === item.productoId; });
    var limite = limiteDe(item.stock);
    var previa = existente ? existente.cantidad : 0;
    var agregado = Math.max(0, Math.min(cantidad, limite - previa));
    if (agregado > 0) {
      if (existente) {
        existente.cantidad += agregado;
        existente.stock = item.stock; // la página trae el stock de ahora
      } else {
        items.push({
          productoId: item.productoId,
          nombre: item.nombre,
          precio: item.precio,
          foto: item.foto || '',
          stock: item.stock,
          cantidad: agregado
        });
      }
      guardar(items);
    } else if (existente) {
      existente.stock = item.stock;
      guardar(items);
    }
    var total = previa + agregado;
    return { agregado: agregado, enCarrito: total, limite: limite, enLimite: total >= limite };
  }

  // ajustarAlStock baja las cantidades que superen el stock conocido (por ejemplo,
  // las que quedaron de antes de que el carrito respetara el límite) y quita las
  // que ya no tienen stock. Devuelve lo que cambió.
  function ajustarAlStock() {
    var ajustes = [];
    var items = obtener().filter(function (i) {
      var limite = limiteDe(i.stock);
      if (i.cantidad <= limite) return true;
      ajustes.push({ nombre: i.nombre, antes: i.cantidad, ahora: limite });
      i.cantidad = limite;
      return limite > 0;
    });
    if (ajustes.length) guardar(items);
    return ajustes;
  }

  // sincronizarBoton deja un botón «Agregar al carrito» coherente con lo que ya
  // hay en el carrito: si ya se llevó todo el stock, se desactiva y lo dice.
  function sincronizarBoton(btn, textoBase) {
    var stock = parseFloat(btn.getAttribute('data-stock'));
    if (!isFinite(stock) || stock < 1) return; // agotado: lo deja el servidor
    var lleno = cantidadEnCarrito(parseInt(btn.getAttribute('data-id'), 10)) >= Math.floor(stock);
    btn.disabled = lleno;
    btn.textContent = lleno ? 'Ya tienes el máximo en tu carrito' : textoBase;
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
    limiteDe: limiteDe,
    cantidadEnCarrito: cantidadEnCarrito,
    ajustarAlStock: ajustarAlStock,
    sincronizarBoton: sincronizarBoton,
    agregar: agregar,
    actualizarCantidad: actualizarCantidad,
    quitar: quitar,
    vaciar: vaciar,
    totalCantidad: totalCantidad,
    totalPrecio: totalPrecio,
    actualizarContador: actualizarContador
  };
})();
