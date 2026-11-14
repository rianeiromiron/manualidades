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

  // Cascada: la vista previa sigue en vivo lo que se mueve en los controles.
  var activa = document.getElementById('cascadaActiva');
  var opacidad = document.getElementById('cascadaOpacidad');
  var velocidad = document.getElementById('cascadaVelocidad');
  var cantidad = document.getElementById('cascadaCantidad');
  var previa = document.getElementById('cascadaPrevia');
  var caja = document.getElementById('cascadaPreviaCaja');
  var bloque = document.getElementById('cascadaBloque');
  function syncCascada() {
    document.getElementById('cascadaOpacidadValor').textContent = opacidad.value + '%';
    document.getElementById('cascadaVelocidadValor').textContent = velocidad.value;
    previa.style.setProperty('--cascada-opacidad', opacidad.value);
    previa.setAttribute('data-velocidad', velocidad.value);
    window.Cascada.ajustarVelocidad(previa);
    caja.style.background = inputs.fondo.value; // el color de fondo elegido arriba
    bloque.classList.toggle('cascada-apagada', !activa.checked);
    // Cambiar la cantidad regenera la distribución (es la única que lo necesita).
    if (previa.getAttribute('data-cantidad') !== cantidad.value) {
      previa.setAttribute('data-cantidad', cantidad.value);
      window.Cascada.montar(previa);
    }
  }
  [activa, opacidad, velocidad, cantidad, inputs.fondo].forEach(function (el) {
    el.addEventListener('input', syncCascada);
    el.addEventListener('change', syncCascada);
  });
  syncCascada();
})();
