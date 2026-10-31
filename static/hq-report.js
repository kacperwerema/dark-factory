// The HQ report (/p/<project>/hq/report): Expand all / Fold all opens or
// closes every box's fold.
(function () {
  var btn = document.querySelector('[data-hqr-toggle]');
  if (!btn) return;
  btn.addEventListener('click', function () {
    var open = btn.getAttribute('aria-pressed') !== 'true';
    document.querySelectorAll('.wb-hqr-box').forEach(function (b) { b.open = open; });
    btn.setAttribute('aria-pressed', open ? 'true' : 'false');
    btn.textContent = open ? 'Fold all' : 'Expand all';
  });
})();
