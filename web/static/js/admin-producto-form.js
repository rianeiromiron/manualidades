// Al guardar un producto nuevo pregunta si se quiere registrar la existencia
// inicial; si sí, la cantidad viaja en "existencia_inicial" y el servidor la
// registra como un ingreso (INGRESO INICIAL).
(function () {
  var form = document.getElementById('formProducto');
  if (!form) return;
  var guardar = document.getElementById('btnGuardar');
  var enviado = false;

  // Al volver con "atrás" el navegador puede restaurar el botón deshabilitado.
  window.addEventListener('pageshow', function () {
    enviado = false;
    guardar.disabled = false;
    guardar.textContent = "Guardar producto";
  });

  var dlg = document.getElementById('dlgExistencia');
  var campo = document.getElementById('existenciaInicial');
  if (!dlg || !campo || typeof dlg.showModal !== 'function') {
    // Edición: solo evitar el doble envío.
    form.addEventListener('submit', function (e) {
      if (enviado) { e.preventDefault(); return; }
      enviado = true;
      guardar.disabled = true;
      guardar.textContent = "Guardando…";
    });
    return;
  }
  var cantidad = document.getElementById('dlgCantidad');
  var confirmado = false;

  form.addEventListener('submit', function (e) {
    if (confirmado) return;
    e.preventDefault();
    cantidad.value = '';
    if (!dlg.open) dlg.showModal();
    cantidad.focus();
  });

  function enviar() {
    if (enviado) return;
    enviado = true;
    confirmado = true;
    guardar.disabled = true;
    guardar.textContent = "Guardando…";
    ['dlgSi', 'dlgNo', 'dlgCancelar'].forEach(function (id) {
      document.getElementById(id).disabled = true;
    });
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
