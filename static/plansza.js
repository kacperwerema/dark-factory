// Plansza warsztatowa: the map's tabs, "Pokaż na mapie", the roads that carry
// the engine's release candidate, and a quiet refresh.
(function () {
  // Tabs in the right panel.
  document.querySelectorAll('.pl-tab').forEach(function (tab) {
    tab.addEventListener('click', function () {
      document.querySelectorAll('.pl-tab').forEach(function (t) {
        var on = t === tab;
        t.setAttribute('aria-selected', on);
        var panel = document.getElementById(t.getAttribute('aria-controls'));
        if (panel) panel.hidden = !on;
      });
    });
  });

  // "Pokaż na mapie": scroll to the lot and flash it.
  document.querySelectorAll('[data-show]').forEach(function (link) {
    link.addEventListener('click', function (e) {
      var lot = document.getElementById(link.getAttribute('data-show'));
      if (!lot) return;
      e.preventDefault();
      lot.scrollIntoView({ behavior: 'smooth', block: 'center' });
      lot.classList.remove('pl-lot--flash');
      void lot.offsetWidth;
      lot.classList.add('pl-lot--flash');
      setTimeout(function () { lot.classList.remove('pl-lot--flash'); }, 2400);
    });
  });

  // Roads: from the engine's lot to every project testing its candidate.
  // A line must carry something, so without a candidate there are no roads.
  var lots = document.getElementById('lots');
  var svg = document.getElementById('roads');
  function drawRoads() {
    if (!lots || !svg) return;
    svg.innerHTML = '';
    lots.querySelectorAll('.pl-truck').forEach(function (t) { t.remove(); });
    var engine = lots.getAttribute('data-rc-engine');
    var from = engine && document.getElementById('lot-' + engine);
    if (!from) return;
    var box = lots.getBoundingClientRect();
    var a = from.getBoundingClientRect();
    var ax = a.left + a.width / 2 - box.left, ay = a.top + a.height / 2 - box.top;
    var ns = 'http://www.w3.org/2000/svg';
    lots.querySelectorAll('.pl-lot[data-rc]').forEach(function (lot) {
      var b = lot.getBoundingClientRect();
      var bx = b.left + b.width / 2 - box.left, by = b.top + b.height / 2 - box.top;
      var midY = (ay + by) / 2;
      var d = Math.abs(ay - by) < 40 ? 'M' + ax + ' ' + ay + ' H' + bx : 'M' + ax + ' ' + ay + ' V' + midY + ' H' + bx + ' V' + by;
      [['#C7D0DC', 16, ''], ['#FFFFFF', 2, '7 7']].forEach(function (s) {
        var p = document.createElementNS(ns, 'path');
        p.setAttribute('d', d); p.setAttribute('fill', 'none'); p.setAttribute('stroke', s[0]); p.setAttribute('stroke-width', s[1]);
        p.setAttribute('stroke-linecap', 'round'); p.setAttribute('stroke-linejoin', 'round');
        if (s[2]) p.setAttribute('stroke-dasharray', s[2]);
        svg.appendChild(p);
      });
      var truck = document.createElement('div');
      truck.className = 'pl-truck';
      truck.innerHTML = '<svg width="18" height="14" viewBox="0 0 24 18" fill="none" stroke="#1B1F2A" stroke-width="2" stroke-linejoin="round" aria-hidden="true"><path d="M1 2h13v11H1zM14 6h5l4 4v3h-9z"/><circle cx="6" cy="15" r="2" fill="#fff"/><circle cx="18" cy="15" r="2" fill="#fff"/></svg>';
      truck.appendChild(document.createTextNode('RC ' + lot.getAttribute('data-rc') + ' · test'));
      truck.style.left = ((ax + bx) / 2) + 'px';
      truck.style.top = (Math.abs(ay - by) < 40 ? ay : midY) + 'px';
      lots.appendChild(truck);
    });
  }
  drawRoads();
  window.addEventListener('resize', drawRoads);
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(drawRoads);

  // Refresh once a minute while the page is in view, unless a details panel
  // is open.
  setInterval(function () {
    if (document.visibilityState === 'visible' && !document.querySelector('details[open]')) location.reload();
  }, 60000);
})();
