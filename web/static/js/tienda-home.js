(function () {
  var checkboxes = Array.prototype.slice.call(document.querySelectorAll('.t-cat-checkbox'));
  var btn = document.getElementById('tFiltroBtn');
  var panel = document.getElementById('tFiltroPanel');
  var wrap = document.getElementById('tFiltroCategorias');
  var count = document.getElementById('tFiltroCount');

  // El filtro y la paginación los resuelve el servidor (?cat=..&pagina=..):
  // aquí solo se refleja cuántas categorías hay marcadas mientras se elige.
  function aplicar() {
    var n = checkboxes.filter(function (cb) { return cb.checked; }).length;
    count.textContent = n === checkboxes.length ? '' : '(' + n + '/' + checkboxes.length + ')';
  }

  checkboxes.forEach(function (cb) { cb.addEventListener('change', aplicar); });

  panel.addEventListener('click', function (e) {
    var action = e.target.getAttribute('data-action');
    if (action === 'all') {
      checkboxes.forEach(function (cb) { cb.checked = true; });
      aplicar();
    } else if (action === 'none') {
      checkboxes.forEach(function (cb) { cb.checked = false; });
      aplicar();
    }
  });

  btn.addEventListener('click', function () {
    panel.hidden = !panel.hidden;
  });
  document.addEventListener('click', function (e) {
    if (!wrap.contains(e.target)) panel.hidden = true;
  });

  Array.prototype.slice.call(document.querySelectorAll('.t-agregar')).forEach(function (btn) {
    btn.addEventListener('click', function () {
      Carrito.agregar({
        productoId: parseInt(btn.getAttribute('data-id'), 10),
        nombre: btn.getAttribute('data-nombre'),
        precio: parseFloat(btn.getAttribute('data-precio')),
        foto: btn.getAttribute('data-foto'),
        stock: parseFloat(btn.getAttribute('data-stock'))
      }, 1);
      var textoOriginal = btn.textContent;
      btn.textContent = 'Agregado ✓';
      setTimeout(function () { btn.textContent = textoOriginal; }, 1200);
    });
  });
})();
