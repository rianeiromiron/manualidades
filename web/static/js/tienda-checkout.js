(function () {
  var items = Carrito.obtener();
  var vacio = document.getElementById('tCheckoutVacio');
  var contenido = document.getElementById('tCheckoutContenido');

  if (items.length === 0) {
    vacio.hidden = false;
    contenido.hidden = true;
    return;
  }
  vacio.hidden = true;
  contenido.hidden = false;

  var lineasEl = document.getElementById('tResumenLineas');
  items.forEach(function (item) {
    var tr = document.createElement('tr');
    var tdNombre = document.createElement('td');
    tdNombre.textContent = item.nombre;
    var tdCantidad = document.createElement('td');
    tdCantidad.className = 'num';
    tdCantidad.textContent = item.cantidad;
    var tdSubtotal = document.createElement('td');
    tdSubtotal.className = 'num';
    tdSubtotal.textContent = 'Q ' + (item.cantidad * item.precio).toFixed(2);
    tr.appendChild(tdNombre);
    tr.appendChild(tdCantidad);
    tr.appendChild(tdSubtotal);
    lineasEl.appendChild(tr);
  });
  document.getElementById('tResumenTotal').textContent = 'Q ' + Carrito.totalPrecio(items).toFixed(2);

  var radios = document.querySelectorAll('input[name="metodo_entrega"]');
  var direccionRow = document.getElementById('tDireccionRow');
  var direccionInput = direccionRow.querySelector('input');
  function syncEntrega() {
    var domicilio = document.querySelector('input[name="metodo_entrega"]:checked').value === 'domicilio';
    direccionRow.hidden = !domicilio;
    direccionInput.required = domicilio;
  }
  radios.forEach(function (r) { r.addEventListener('change', syncEntrega); });
  syncEntrega();

  var form = document.getElementById('tFormCheckout');
  var errorBox = document.getElementById('tCheckoutError');
  var btn = document.getElementById('tPagarBtn');

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    errorBox.hidden = true;
    btn.disabled = true;
    btn.textContent = 'Procesando…';

    var fd = new FormData(form);
    var payload = {
      cliente_nombre: fd.get('cliente_nombre'),
      cliente_telefono: fd.get('cliente_telefono'),
      cliente_email: fd.get('cliente_email'),
      cliente_nit: fd.get('cliente_nit'),
      metodo_entrega: fd.get('metodo_entrega'),
      direccion_entrega: fd.get('direccion_entrega') || '',
      notas: fd.get('notas') || '',
      tarjeta_numero: fd.get('tarjeta_numero'),
      tarjeta_titular: fd.get('tarjeta_titular'),
      tarjeta_expiracion: fd.get('tarjeta_expiracion'),
      tarjeta_cvv: fd.get('tarjeta_cvv'),
      items: Carrito.obtener().map(function (i) {
        return { producto_id: i.productoId, cantidad: i.cantidad };
      })
    };

    fetch('/checkout/confirmar', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    })
      .then(function (r) { return r.json().then(function (data) { return { status: r.status, data: data }; }); })
      .then(function (res) {
        if (res.data.ok) {
          Carrito.vaciar();
          window.location.href = '/pedido/' + res.data.pedido_id + '/confirmacion';
        } else {
          errorBox.textContent = res.data.error || 'No se pudo procesar el pedido.';
          errorBox.hidden = false;
          btn.disabled = false;
          btn.textContent = 'Pagar';
        }
      })
      .catch(function () {
        errorBox.textContent = 'No se pudo conectar con el servidor.';
        errorBox.hidden = false;
        btn.disabled = false;
        btn.textContent = 'Pagar';
      });
  });
})();
