(function () {
  var soloVentas = document.getElementById('soloVentasCheckbox');
  var ingresosRow = document.getElementById('ingresosRow');
  var egresosRow = document.getElementById('egresosRow');
  function sync() {
    var deshabilitar = soloVentas.checked;
    ingresosRow.classList.toggle('checkbox-row-disabled', deshabilitar);
    egresosRow.classList.toggle('checkbox-row-disabled', deshabilitar);
    ingresosRow.querySelector('input').disabled = deshabilitar;
    egresosRow.querySelector('input').disabled = deshabilitar;
  }
  soloVentas.addEventListener('change', sync);
  sync();

  var btnImprimir = document.getElementById('btnImprimir');
  if (btnImprimir) {
    btnImprimir.addEventListener('click', function () { window.print(); });
  }
})();
