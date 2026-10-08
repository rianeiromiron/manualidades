// Pide confirmación antes de enviar los formularios marcados con
// data-confirm="mensaje" (eliminar un producto, un usuario, etc.). Va en un
// archivo aparte y no en un atributo onsubmit porque la CSP no permite
// JavaScript inline.
document.addEventListener('submit', function (e) {
  var mensaje = e.target.getAttribute && e.target.getAttribute('data-confirm');
  if (mensaje && !window.confirm(mensaje)) e.preventDefault();
});
