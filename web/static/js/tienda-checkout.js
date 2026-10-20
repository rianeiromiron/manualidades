(function () {
  // Al volver con «Atrás» el navegador puede restaurar esta página tal como quedó,
  // con el botón en «Procesando…» y el resumen viejo, aunque el pedido ya se
  // haya creado y el carrito esté vacío. Se recarga para armarla de nuevo con el
  // carrito de ahora.
  window.addEventListener('pageshow', function (e) {
    if (e.persisted) window.location.reload();
  });

  // Un carrito con más de lo disponible se corrige antes de mostrar el resumen; el
  // servidor lo vuelve a comprobar al crear el pedido.
  var ajustes = Carrito.ajustarAlStock();
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
  // El envío a domicilio se suma al total que se muestra. Es solo informativo:
  // el servidor calcula siempre el monto real que se cobra.
  var envioRow = document.getElementById('tResumenEnvio');
  var costoEnvio = parseFloat(envioRow.getAttribute('data-costo')) || 0;
  function actualizarTotal(domicilio) {
    var envio = domicilio && costoEnvio > 0 ? costoEnvio : 0;
    envioRow.hidden = envio === 0;
    document.getElementById('tResumenEnvioMonto').textContent = 'Q ' + envio.toFixed(2);
    document.getElementById('tResumenTotal').textContent = 'Q ' + (Carrito.totalPrecio(items) + envio).toFixed(2);
  }
  actualizarTotal(false);

  var radios = document.querySelectorAll('input[name="metodo_entrega"]');
  var direccionRow = document.getElementById('tDireccionRow');
  var direccionInput = direccionRow.querySelector('input');
  function syncEntrega() {
    var domicilio = document.querySelector('input[name="metodo_entrega"]:checked').value === 'domicilio';
    direccionRow.hidden = !domicilio;
    direccionInput.required = domicilio;
    actualizarTotal(domicilio);
  }
  radios.forEach(function (r) { r.addEventListener('change', syncEntrega); });
  syncEntrega();

  var form = document.getElementById('tFormCheckout');
  var errorBox = document.getElementById('tCheckoutError');
  var btn = document.getElementById('tPagarBtn');
  if (ajustes.length) {
    errorBox.textContent = ajustes.map(function (a) {
      return 'Ajustamos «' + a.nombre + '» a lo que hay disponible (' + a.ahora + ').';
    }).join(' ');
    errorBox.hidden = false;
  }

  // Forma de pago: tarjeta o transferencia (solo si la tienda tiene cuentas).
  var tarjetaCampos = document.getElementById('tTarjetaCampos');
  var transfInfo = document.getElementById('tTransferenciaInfo');
  var emailInput = form.querySelector('input[name="cliente_email"]');
  var emailEtiqueta = document.getElementById('tEmailEtiqueta');
  function esTransferencia() {
    var c = document.querySelector('input[name="metodo_pago"]:checked');
    return !!c && c.value === 'transferencia';
  }
  function etiquetaBoton() {
    return esTransferencia() ? 'Continuar con la transferencia' : 'Pagar';
  }
  function syncPago() {
    var transf = esTransferencia();
    tarjetaCampos.hidden = transf;
    Array.prototype.forEach.call(tarjetaCampos.querySelectorAll('input'), function (i) { i.required = !transf; });
    if (transfInfo) transfInfo.hidden = !transf;
    emailInput.required = transf;
    emailEtiqueta.textContent = transf ? 'Email (obligatorio: ahí te enviamos el enlace de tu pedido)' : 'Email (opcional)';
    btn.textContent = etiquetaBoton();
  }
  Array.prototype.forEach.call(document.querySelectorAll('input[name="metodo_pago"]'), function (r) {
    r.addEventListener('change', syncPago);
  });
  syncPago();

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    errorBox.hidden = true;
    btn.disabled = true;
    btn.textContent = 'Procesando…';

    var transf = esTransferencia();
    var fd = new FormData(form);
    var payload = {
      cliente_nombre: fd.get('cliente_nombre'),
      cliente_telefono: fd.get('cliente_telefono'),
      cliente_email: fd.get('cliente_email'),
      cliente_nit: fd.get('cliente_nit'),
      metodo_entrega: fd.get('metodo_entrega'),
      direccion_entrega: fd.get('direccion_entrega') || '',
      notas: fd.get('notas') || '',
      tarjeta_numero: transf ? '' : fd.get('tarjeta_numero'),
      tarjeta_titular: transf ? '' : fd.get('tarjeta_titular'),
      tarjeta_expiracion: transf ? '' : fd.get('tarjeta_expiracion'),
      tarjeta_cvv: transf ? '' : fd.get('tarjeta_cvv'),
      items: Carrito.obtener().map(function (i) {
        return { producto_id: i.productoId, cantidad: i.cantidad };
      })
    };

    fetch(transf ? '/checkout/transferencia' : '/checkout/confirmar', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    })
      .then(function (r) { return r.json().then(function (data) { return { status: r.status, data: data }; }); })
      .then(function (res) {
        if (res.data.ok) {
          Carrito.vaciar();
          // El pedido por transferencia lleva a su página privada; solo se
          // acepta una ruta propia (/pedido/...), nunca otra dirección.
          var destino = transf && typeof res.data.redirect === 'string' && res.data.redirect.indexOf('/pedido/') === 0
            ? res.data.redirect
            : '/pedido/' + res.data.pedido_id + '/confirmacion';
          window.location.href = destino;
        } else {
          errorBox.textContent = res.data.error || 'No se pudo procesar el pedido.';
          errorBox.hidden = false;
          btn.disabled = false;
          btn.textContent = etiquetaBoton();
        }
      })
      .catch(function () {
        errorBox.textContent = 'No se pudo conectar con el servidor.';
        errorBox.hidden = false;
        btn.disabled = false;
        btn.textContent = etiquetaBoton();
      });
  });
})();
