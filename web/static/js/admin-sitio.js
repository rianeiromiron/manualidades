(function () {
  var preview = document.getElementById('preview');
  var inputs = {
    fondo: document.getElementById('colorFondo'),
    texto: document.getElementById('colorTexto'),
    marco: document.getElementById('colorMarco'),
    acento: document.getElementById('colorAcento')
  };
  function sync() {
    preview.style.setProperty('--p-fondo', inputs.fondo.value);
    preview.style.setProperty('--p-texto', inputs.texto.value);
    preview.style.setProperty('--p-marco', inputs.marco.value);
    preview.style.setProperty('--p-acento', inputs.acento.value);
  }
  Object.keys(inputs).forEach(function (k) {
    inputs[k].addEventListener('input', sync);
  });
  sync();
})();
