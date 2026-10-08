(function () {
  function render() {
    var items = Carrito.obtener();
    var vacio = document.getElementById('tCarritoVacio');
    var contenido = document.getElementById('tCarritoContenido');
    vacio.hidden = items.length > 0;
    contenido.hidden = items.length === 0;

    var lineas = document.getElementById('tLineas');
    lineas.innerHTML = '';
    items.forEach(function (item) {
      var div = document.createElement('div');
      div.className = 't-linea';
      // Los datos del producto se asignan con APIs del DOM (nunca como HTML)
      // para que un nombre con caracteres especiales se muestre como texto.
      function boton(accion, texto, estilo) {
        var b = document.createElement('button');
        b.type = 'button';
        b.dataset.accion = accion;
        b.textContent = texto;
        if (estilo) b.style.cssText = estilo;
        return b;
      }

      if (item.foto) {
        var img = document.createElement('img');
        img.src = '/media/productos/' + encodeURIComponent(item.foto);
        img.alt = '';
        div.appendChild(img);
      } else {
        div.appendChild(document.createElement('div'));
      }

      var info = document.createElement('div');
      var nombre = document.createElement('strong');
      nombre.textContent = item.nombre;
      var precio = document.createElement('span');
      precio.textContent = 'Q ' + Number(item.precio).toFixed(2);
      info.appendChild(nombre);
      info.appendChild(document.createElement('br'));
      info.appendChild(precio);
      div.appendChild(info);

      var qty = document.createElement('div');
      qty.className = 't-linea-qty';
      var cantidad = document.createElement('span');
      cantidad.textContent = String(item.cantidad);
      var menos = boton('menos', '−');
      var mas = boton('mas', '+');
      var quitar = boton('quitar', '✕', 'margin-left:10px;');
      qty.appendChild(menos);
      qty.appendChild(cantidad);
      qty.appendChild(mas);
      qty.appendChild(quitar);
      div.appendChild(qty);

      menos.addEventListener('click', function () {
        Carrito.actualizarCantidad(item.productoId, item.cantidad - 1);
        render();
      });
      mas.addEventListener('click', function () {
        var limite = item.stock || Infinity;
        Carrito.actualizarCantidad(item.productoId, Math.min(item.cantidad + 1, limite));
        render();
      });
      quitar.addEventListener('click', function () {
        Carrito.quitar(item.productoId);
        render();
      });
      lineas.appendChild(div);
    });

    document.getElementById('tTotal').textContent = 'Q ' + Carrito.totalPrecio(items).toFixed(2);
  }

  render();
})();
