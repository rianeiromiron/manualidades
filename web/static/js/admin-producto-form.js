// Al guardar un producto nuevo pregunta si se quiere registrar la existencia
// inicial; si sí, la cantidad viaja en "existencia_inicial" y el servidor la
// registra como un ingreso (INGRESO INICIAL).
(function () {
  var dlg = document.getElementById('dlgExistencia');
  var campo = document.getElementById('existenciaInicial');
  if (!dlg || !campo || typeof dlg.showModal !== 'function') return;
  var form = campo.form;
  var cantidad = document.getElementById('dlgCantidad');
  var confirmado = false;

  form.addEventListener('submit', function (e) {
    if (confirmado) return;
    e.preventDefault();
    cantidad.value = '';
    dlg.showModal();
    cantidad.focus();
  });

  function enviar() {
    confirmado = true;
    dlg.close();
    form.submit();
  }

  document.getElementById('dlgSi').addEventListener('click', function () {
    var n = parseFloat(cantidad.value);
    if (!(n > 0)) { cantidad.focus(); return; }
    campo.value = String(n);
    enviar();
  });
  document.getElementById('dlgNo').addEventListener('click', function () {
    campo.value = '';
    enviar();
  });
  document.getElementById('dlgCancelar').addEventListener('click', function () { dlg.close(); });
})();
