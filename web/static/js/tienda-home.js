(function () {
  var cards = Array.prototype.slice.call(document.querySelectorAll('#tGrid .t-card'));
  var checkboxes = Array.prototype.slice.call(document.querySelectorAll('.t-cat-checkbox'));
  var btn = document.getElementById('tFiltroBtn');
  var panel = document.getElementById('tFiltroPanel');
  var wrap = document.getElementById('tFiltroCategorias');
  var count = document.getElementById('tFiltroCount');
  var sinResultados = document.getElementById('tSinResultados');

  function activas() {
    var set = {};
    checkboxes.forEach(function (cb) { if (cb.checked) set[cb.value] = true; });
    return set;
  }

  function aplicar() {
    var set = activas();
    var visibles = 0;
    cards.forEach(function (card) {
      var visible = !!set[card.dataset.categoriaId];
      card.hidden = !visible;
      if (visible) visibles++;
    });
    if (sinResultados) sinResultados.hidden = visibles !== 0;

    var total = checkboxes.length;
    var n = Object.keys(set).length;
    count.textContent = '(' + n + '/' + total + ')';
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

  aplicar();

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
