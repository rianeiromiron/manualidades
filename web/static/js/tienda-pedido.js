(function () {
  // Cambiar la entrega: la dirección solo se pide (y se ve) si es a domicilio.
  var form = document.getElementById('tFormEntrega');
  if (form) {
    var fila = document.getElementById('tDireccionRow');
    var entrada = fila ? fila.querySelector('input') : null;
    var nota = document.getElementById('tEnvioNota');
    var radios = Array.prototype.slice.call(form.querySelectorAll('input[name="metodo_entrega"]'));
    function sincronizar() {
      var elegido = radios.filter(function (r) { return r.checked; })[0];
      var domicilio = !!elegido && elegido.value === 'domicilio';
      if (fila) fila.hidden = !domicilio;
      if (entrada) entrada.required = domicilio;
      if (nota) nota.hidden = !domicilio;
    }
    radios.forEach(function (r) { r.addEventListener('change', sincronizar); });
    sincronizar();
  }

  // Los formularios con data-confirm piden confirmación antes de enviarse (la
  // política de seguridad no permite onsubmit en la propia etiqueta).
  Array.prototype.slice.call(document.querySelectorAll('form[data-confirm]')).forEach(function (f) {
    f.addEventListener('submit', function (e) {
      if (!window.confirm(f.getAttribute('data-confirm'))) e.preventDefault();
    });
  });
})();
