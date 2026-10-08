(function () {
  var table = document.getElementById('productosTable');
  var rows = Array.prototype.slice.call(table.querySelectorAll('tbody tr[data-categoria-id]'));
  var textInput = document.getElementById('textoFiltro');
  var catCheckboxes = Array.prototype.slice.call(document.querySelectorAll('.cat-filter-checkbox'));
  var catBtn = document.getElementById('catFiltroBtn');
  var catPanel = document.getElementById('catFiltroPanel');
  var catCount = document.getElementById('catFiltroCount');
  var catWrap = document.getElementById('categoriaFiltro');
  var sinResultados = document.getElementById('sinResultados');

  function movRowFor(productoId) {
    return table.querySelector('.mov-row[data-producto-id="' + productoId + '"]');
  }

  function categoriasActivas() {
    var activas = {};
    catCheckboxes.forEach(function (cb) {
      if (cb.checked) activas[cb.value] = true;
    });
    return activas;
  }

  function applyFilters() {
    var texto = textInput.value.trim().toLowerCase();
    var activas = categoriasActivas();
    var visibles = 0;

    rows.forEach(function (row) {
      var matchCategoria = !!activas[row.dataset.categoriaId];
      var matchTexto = texto === '' || row.dataset.search.toLowerCase().indexOf(texto) !== -1;
      var visible = matchCategoria && matchTexto;
      row.hidden = !visible;
      if (visible) visibles++;

      // Si el producto queda oculto por el filtro, colapsa también su
      // panel de movimientos (si estaba abierto) para no dejarlo huérfano.
      if (!visible) {
        var movRow = movRowFor(row.dataset.productoId);
        if (movRow) movRow.hidden = true;
      }
    });

    sinResultados.hidden = visibles !== 0;

    var total = catCheckboxes.length;
    var activos = Object.keys(activas).length;
    catCount.textContent = '(' + activos + '/' + total + ')';
  }

  textInput.addEventListener('input', applyFilters);
  catCheckboxes.forEach(function (cb) {
    cb.addEventListener('change', applyFilters);
  });

  catPanel.addEventListener('click', function (e) {
    var action = e.target.getAttribute('data-action');
    if (action === 'all') {
      catCheckboxes.forEach(function (cb) { cb.checked = true; });
      applyFilters();
    } else if (action === 'none') {
      catCheckboxes.forEach(function (cb) { cb.checked = false; });
      applyFilters();
    }
  });

  catBtn.addEventListener('click', function () {
    catPanel.hidden = !catPanel.hidden;
  });
  document.addEventListener('click', function (e) {
    if (!catWrap.contains(e.target)) {
      catPanel.hidden = true;
    }
  });

  Array.prototype.slice.call(document.querySelectorAll('.mov-toggle')).forEach(function (btn) {
    btn.addEventListener('click', function () {
      var id = btn.getAttribute('data-producto-id');
      var movRow = movRowFor(id);
      if (!movRow) return;

      movRow.hidden = !movRow.hidden;
      if (movRow.hidden) return;

      var panel = movRow.querySelector('.mov-panel');
      panel.innerHTML = '<p class="empty-hint">Cargando…</p>';
      fetch('/admin/mantenimiento/inventario/productos/' + id + '/movimientos')
        .then(function (r) {
          if (!r.ok) throw new Error('respuesta no válida');
          return r.text();
        })
        .then(function (html) { panel.innerHTML = html; })
        .catch(function () {
          panel.innerHTML = '<p class="alert alert-error">No se pudo cargar el historial.</p>';
        });
    });
  });

  applyFilters();
})();
