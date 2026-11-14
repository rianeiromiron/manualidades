/* Fondo animado: cascada de pinceles y frascos de pintura.

   En vez de repetir un mosaico (que deja un patrón visible), cada vez que se
   carga la página se genera una distribución nueva y al azar:

   - Las posiciones salen de un muestreo con distancia mínima entre centros, así
     que las figuras nunca se traslapan (cada una cabe en un círculo de radio
     RADIO_MAX y los círculos no se tocan).
   - La distribución es más alta que la pantalla y "da la vuelta" en vertical:
     al caer una altura completa queda idéntica al inicio, de modo que el
     movimiento es continuo y ninguna zona se repite a la vista.
   - Todas las figuras caen a la misma velocidad; si no, unas pasarían por
     encima de otras.

   Lo usan la tienda y la vista previa de Admin > Sitio web. Cada elemento
   .fondo-cascada se configura con data-cantidad (1 pocas, 2 normal, 3
   bastantes) y data-velocidad (1 lenta a 10 rápida). */
(function () {
  'use strict';

  // Distancia mínima entre centros, en px, según la cantidad de figuras.
  var DISTANCIA = { 1: 170, 2: 120, 3: 90 };
  // Ninguna figura (ya rotada y escalada) sale de un círculo de este radio.
  var RADIO_MAX = 40;
  // Píxeles que cae por segundo cada punto de velocidad.
  var PX_POR_SEG = 4;

  // [mango, casquillo, cerdas]
  var PINCELES = [
    ['d8c4b6', 'c5d9e8', 'e2c6c6'],
    ['d8c4b6', 'e2c6c6', 'b5d8cc'],
    ['e6d8ce', 'd3e8e0', 'dce8f1'],
    ['efe4dc', 'e5eff5', 'f2e6e6'],
    ['efe4dc', 'e3f0eb', 'e5eff5']
  ];
  // [cuerpo, base, tapa del cuerpo, tapón, tapa del tapón]
  var FRASCOS = [
    ['b5d8cc', 'a3cbbd', 'cbe5dc', 'd8c4b6', 'e5d5c9'],
    ['dce8f1', 'cddde9', 'e8f1f7', 'ebdada', 'f3e7e7'],
    ['ebdada', 'e0cccc', 'f4e8e8', 'd3e8e0', 'e2f1eb'],
    ['e3f0eb', 'd6e7e0', 'eef7f4', 'efe4dc', 'f6eee8'],
    ['f2e6e6', 'e8d8d8', 'f8efef', 'e5eff5', 'eff6fa']
  ];

  var contador = 0;

  // Dibujos centrados en (0, 0), de hasta ~35 px de radio sin escalar.
  function pincelA(c) {
    return '<g transform="translate(0,-6)">' +
      '<path d="M -2.5 5 L 2.5 5 L 1.6 38 C 1.5 40.5 -1.5 40.5 -1.6 38 Z" fill="#' + c[0] + '"/>' +
      '<rect x="-4" y="-6" width="8" height="11" rx="1" fill="#' + c[1] + '"/>' +
      '<path d="M -4.2 -6 C -5.5 -15 -3 -24 0 -28 C 3 -24 5.5 -15 4.2 -6 Z" fill="#' + c[2] + '"/></g>';
  }
  function pincelB(c) {
    return '<g transform="translate(0,-6)">' +
      '<path d="M -2.2 4 L 2.2 4 L 1.4 34 C 1.3 36 -1.3 36 -1.4 34 Z" fill="#' + c[0] + '"/>' +
      '<rect x="-3.5" y="-5" width="7" height="9" rx="1" fill="#' + c[1] + '"/>' +
      '<path d="M -3.8 -5 C -4.8 -13 -2.5 -21 0 -24 C 2.5 -21 4.8 -13 3.8 -5 Z" fill="#' + c[2] + '"/></g>';
  }
  function frasco(c) {
    return '<rect x="-11" y="-14" width="22" height="32" rx="2" fill="#' + c[0] + '"/>' +
      '<ellipse cx="0" cy="18" rx="11" ry="3.2" fill="#' + c[1] + '"/>' +
      '<ellipse cx="0" cy="-14" rx="11" ry="3" fill="#' + c[2] + '"/>' +
      '<rect x="-8.5" y="-21" width="17" height="7" rx="1.5" fill="#' + c[3] + '"/>' +
      '<ellipse cx="0" cy="-21" rx="8.5" ry="2.4" fill="#' + c[4] + '"/>';
  }

  // generarPuntos reparte puntos al azar en un rectángulo ancho x alto, a una
  // distancia mínima dmin entre sí. En vertical "da la vuelta" (el borde de
  // abajo es vecino del de arriba), por eso necesita alto >= 3 * dmin.
  function generarPuntos(ancho, alto, dmin, azar) {
    azar = azar || Math.random;
    var cols = Math.max(1, Math.floor(ancho / dmin));
    var filas = Math.max(3, Math.floor(alto / dmin));
    var cw = ancho / cols;
    var ch = alto / filas;
    var rejilla = [];
    for (var i = 0; i < cols * filas; i++) rejilla.push([]);

    var puntos = [];
    var d2 = dmin * dmin;
    var intentos = Math.round(60 * (ancho * alto) / d2) + 500;
    while (intentos-- > 0) {
      var x = azar() * ancho;
      var y = azar() * alto;
      var cx = Math.min(cols - 1, Math.floor(x / cw));
      var cy = Math.min(filas - 1, Math.floor(y / ch));
      var libre = true;
      for (var dx = -1; dx <= 1 && libre; dx++) {
        var gx = cx + dx;
        if (gx < 0 || gx >= cols) continue;
        for (var dy = -1; dy <= 1 && libre; dy++) {
          var gy = (cy + dy + filas) % filas;
          var celda = rejilla[gy * cols + gx];
          for (var k = 0; k < celda.length; k++) {
            var ddx = celda[k].x - x;
            var ddy = Math.abs(celda[k].y - y);
            ddy = Math.min(ddy, alto - ddy);
            if (ddx * ddx + ddy * ddy < d2) { libre = false; break; }
          }
        }
      }
      if (libre) {
        var p = { x: x, y: y };
        rejilla[cy * cols + cx].push(p);
        puntos.push(p);
      }
    }
    return puntos;
  }

  function elegir(lista) {
    return lista[Math.floor(Math.random() * lista.length)];
  }

  function nivelDe(el) {
    var n = parseInt(el.getAttribute('data-velocidad'), 10);
    return n >= 1 && n <= 10 ? n : 5;
  }

  // ajustarVelocidad cambia solo la duración de la caída (sin regenerar).
  function ajustarVelocidad(el) {
    if (!el._periodo) return;
    el.style.setProperty('--cascada-duracion', (el._periodo / (PX_POR_SEG * nivelDe(el))) + 's');
  }

  // montar genera una distribución nueva dentro del elemento.
  function montar(el) {
    var ancho = el.clientWidth;
    var alto = el.clientHeight;
    if (!ancho || !alto) return;

    var dmin = DISTANCIA[parseInt(el.getAttribute('data-cantidad'), 10)] || DISTANCIA[2];
    // El periodo (lo que mide la distribución) debe ser mayor que lo que se ve.
    var periodo = Math.max(Math.round(alto * 1.2), 3 * dmin);
    var puntos = generarPuntos(ancho, periodo, dmin);

    var id = 'casc' + (++contador);
    var defs = '';
    PINCELES.forEach(function (c, i) {
      defs += '<g id="' + id + 'pa' + i + '">' + pincelA(c) + '</g>';
      defs += '<g id="' + id + 'pb' + i + '">' + pincelB(c) + '</g>';
    });
    FRASCOS.forEach(function (c, i) {
      defs += '<g id="' + id + 'f' + i + '">' + frasco(c) + '</g>';
    });

    // La capa mide dos periodos: la copia de arriba (fuera de pantalla) y la de
    // abajo (la que se ve al empezar). Al bajar un periodo, la de arriba ocupa
    // el lugar de la de abajo y todo recomienza sin salto.
    var usos = '';
    puntos.forEach(function (p) {
      var ref;
      var r = Math.random();
      if (r < 0.3) ref = 'pa' + Math.floor(Math.random() * PINCELES.length);
      else if (r < 0.55) ref = 'pb' + Math.floor(Math.random() * PINCELES.length);
      else ref = 'f' + Math.floor(Math.random() * FRASCOS.length);
      var giro = Math.round(Math.random() * 360);
      var escala = (0.7 + Math.random() * 0.45).toFixed(2);
      for (var k = -1; k <= 2; k++) {
        var y = p.y + k * periodo;
        if (y < -RADIO_MAX || y > 2 * periodo + RADIO_MAX) continue;
        usos += '<use href="#' + id + ref + '" transform="translate(' + p.x.toFixed(1) + ',' +
          y.toFixed(1) + ') rotate(' + giro + ') scale(' + escala + ')"/>';
      }
    });

    var capa = document.createElement('div');
    capa.className = 'cascada-capa';
    capa.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="' + ancho + '" height="' +
      (2 * periodo) + '" viewBox="0 0 ' + ancho + ' ' + (2 * periodo) + '"><defs>' + defs +
      '</defs>' + usos + '</svg>';

    el.textContent = '';
    el.appendChild(capa);
    el._ancho = ancho;
    el._periodo = periodo;
    el._puntos = puntos.length;
    el.style.setProperty('--cascada-alto', periodo + 'px');
    ajustarVelocidad(el);
  }

  function montarTodos() {
    Array.prototype.forEach.call(document.querySelectorAll('.fondo-cascada'), montar);
  }

  // Si la ventana cambia de ancho (o crece más que el periodo), se regenera.
  // Los cambios pequeños de alto (la barra del navegador en el celular) no.
  var espera;
  window.addEventListener('resize', function () {
    clearTimeout(espera);
    espera = setTimeout(function () {
      Array.prototype.forEach.call(document.querySelectorAll('.fondo-cascada'), function (el) {
        if (el.clientWidth !== el._ancho || el.clientHeight > (el._periodo || 0)) montar(el);
      });
    }, 250);
  });

  window.Cascada = { montar: montar, ajustarVelocidad: ajustarVelocidad, generarPuntos: generarPuntos };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', montarTodos);
  } else {
    montarTodos();
  }
})();
