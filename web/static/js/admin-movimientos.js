(function () {
  var tipoSelect = document.getElementById('tipoSelect');
  var productoSelect = document.getElementById('productoSelect');
  var categoriaFiltro = document.getElementById('categoriaFiltro');
  var esVentaRow = document.getElementById('esVentaRow');
  var esVentaCheckbox = document.getElementById('esVentaCheckbox');
  var precioVentaRow = document.getElementById('precioVentaRow');
  var precioVentaInput = document.getElementById('precioVentaInput');

  function precioDefaultProducto() {
    var opt = productoSelect.options[productoSelect.selectedIndex];
    return opt ? (opt.getAttribute('data-precio-venta') || '0') : '0';
  }

  function syncTipo() {
    var esEgreso = tipoSelect.value === 'consumo';
    esVentaCheckbox.disabled = !esEgreso;
    esVentaCheckbox.checked = esEgreso;
    esVentaRow.classList.toggle('checkbox-row-disabled', !esEgreso);
    syncPrecio();
  }

  function syncPrecio() {
    var activo = !esVentaCheckbox.disabled && esVentaCheckbox.checked;
    precioVentaInput.disabled = !activo;
    precioVentaRow.classList.toggle('checkbox-row-disabled', !activo);
    if (activo) {
      precioVentaInput.value = precioDefaultProducto();
    } else {
      precioVentaInput.value = '';
    }
  }

  function syncCategoria() {
    var categoriaID = categoriaFiltro.value;
    var opciones = productoSelect.options;
    for (var i = 0; i < opciones.length; i++) {
      var opt = opciones[i];
      if (!opt.value) continue; // "Selecciona…" siempre visible
      opt.hidden = categoriaID !== '' && opt.getAttribute('data-categoria-id') !== categoriaID;
    }
    // Si el producto elegido queda oculto por el filtro, se limpia la
    // selección en vez de dejar un producto de otra categoría escondido.
    var actual = productoSelect.options[productoSelect.selectedIndex];
    if (actual && actual.hidden) {
      productoSelect.value = '';
      syncPrecio();
    }
  }

  tipoSelect.addEventListener('change', syncTipo);
  esVentaCheckbox.addEventListener('change', syncPrecio);
  productoSelect.addEventListener('change', syncPrecio);
  categoriaFiltro.addEventListener('change', syncCategoria);
  syncTipo();
})();
