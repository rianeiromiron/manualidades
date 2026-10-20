(function () {
  // Galería: al hacer clic en una miniatura pasa a ser la foto principal.
  var principal = document.getElementById('tFotoPrincipal');
  var minis = Array.prototype.slice.call(document.querySelectorAll('.t-galeria-mini img'));
  minis.forEach(function (mini) {
    mini.addEventListener('click', function () {
      if (principal) principal.src = mini.src;
      minis.forEach(function (el) { el.classList.remove('activa'); });
      mini.classList.add('activa');
    });
  });

  // Los datos del producto vienen en atributos data-* del botón (la CSP no
  // permite scripts inline donde la plantilla los pudiera incrustar).
  var btn = document.getElementById('tAgregarBtn');
  if (!btn) return;
  var TEXTO = 'Agregar al carrito';
  var aviso = document.getElementById('tAgregarAviso');
  function mostrarAviso(texto) {
    if (!aviso) return;
    aviso.textContent = texto;
    aviso.hidden = !texto;
  }
  btn.addEventListener('click', function () {
    var cantidad = parseInt(document.getElementById('tCantidad').value, 10) || 1;
    var r = Carrito.agregar({
      productoId: parseInt(btn.getAttribute('data-id'), 10),
      nombre: btn.getAttribute('data-nombre'),
      precio: parseFloat(btn.getAttribute('data-precio')),
      foto: btn.getAttribute('data-foto'),
      stock: parseFloat(btn.getAttribute('data-stock'))
    }, cantidad);
    if (r.agregado < cantidad) {
      mostrarAviso(r.agregado === 0
        ? 'Ya tienes en tu carrito todas las unidades disponibles (' + r.limite + ').'
        : 'Solo hay ' + r.limite + ' disponible' + (r.limite === 1 ? '' : 's') + ': se agregaron ' + r.agregado + ' y ya tienes el máximo en tu carrito.');
    } else {
      mostrarAviso('');
    }
    if (r.agregado === 0) {
      Carrito.sincronizarBoton(btn, TEXTO);
      return;
    }
    btn.textContent = 'Agregado ✓';
    setTimeout(function () { Carrito.sincronizarBoton(btn, TEXTO); }, 1200);
  });
  Carrito.sincronizarBoton(btn, TEXTO);
  window.addEventListener('pageshow', function (e) { if (e.persisted) Carrito.sincronizarBoton(btn, TEXTO); });
})();
