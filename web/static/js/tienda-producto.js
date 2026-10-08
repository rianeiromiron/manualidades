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
  btn.addEventListener('click', function () {
    var cantidad = parseInt(document.getElementById('tCantidad').value, 10) || 1;
    Carrito.agregar({
      productoId: parseInt(btn.getAttribute('data-id'), 10),
      nombre: btn.getAttribute('data-nombre'),
      precio: parseFloat(btn.getAttribute('data-precio')),
      foto: btn.getAttribute('data-foto'),
      stock: parseFloat(btn.getAttribute('data-stock'))
    }, cantidad);
    btn.textContent = 'Agregado ✓';
    setTimeout(function () { btn.textContent = 'Agregar al carrito'; }, 1200);
  });
})();
