(function () {
  var rolSelect = document.getElementById('rolSelect');
  var modulosBox = document.getElementById('modulosBox');
  function syncRol() {
    modulosBox.hidden = rolSelect.value !== 'administrativo';
  }
  rolSelect.addEventListener('change', syncRol);
  syncRol();

  // En edición, la contraseña solo se toca si se marca la casilla; así,
  // si el navegador autocompleta el campo por su cuenta, no se manda al
  // servidor (un input disabled no se incluye en el envío del formulario).
  var cambiarPasswordCheckbox = document.getElementById('cambiarPasswordCheckbox');
  if (cambiarPasswordCheckbox) {
    var passwordInput = document.getElementById('passwordInput');
    var confirmarInput = document.getElementById('confirmarInput');
    var passwordRow = document.getElementById('passwordRow');
    var confirmarRow = document.getElementById('confirmarRow');
    function syncPassword() {
      var activo = cambiarPasswordCheckbox.checked;
      passwordInput.disabled = !activo;
      confirmarInput.disabled = !activo;
      passwordInput.required = activo;
      confirmarInput.required = activo;
      passwordRow.classList.toggle('checkbox-row-disabled', !activo);
      confirmarRow.classList.toggle('checkbox-row-disabled', !activo);
      if (!activo) {
        passwordInput.value = '';
        confirmarInput.value = '';
      }
    }
    cambiarPasswordCheckbox.addEventListener('change', syncPassword);
    syncPassword();
  }
})();
