// The company floor (L1): the plan scaled to fit the view as a CSS
// transform, always: the whole floor is on screen, so there is no panning,
// zoom or minimap (if a bigger floor stops fitting, moving around can come
// back then). Picking a room opens it in the inspector rail; the CEO's and
// the leads' robots link their chats. Model: company_floor.go.
(function () {
  var app = document.querySelector('.wb-fl-app');
  var view = document.querySelector('.wb-fl-view');
  var plan = view && view.querySelector('.wb-fl-plan');
  if (!app || !plan) return;
  app.classList.add('wb-fl--js');

  var PW = +plan.dataset.w, PH = +plan.dataset.h;

  function lod(scale) {
    if (scale < 0.6) return 'far';
    if (scale < 1) return 'compact';
    if (scale < 1.3) return 'normal';
    return 'detail';
  }

  // Fit is never clamped: the whole plan always fits the view.
  function fit() {
    var s = Math.min(view.clientWidth / PW, view.clientHeight / PH);
    plan.style.transform = 'scale(' + s + ')';
    view.dataset.scale = s.toFixed(3);
    view.dataset.lod = lod(s);
    // The level also as a custom property, readable from computed style.
    view.style.setProperty('--lod', lod(s));
    view.style.setProperty('--inv', (1 / s).toFixed(3));
  }
  view.addEventListener('dragstart', function (e) { e.preventDefault(); });

  // Picking a room: its inspector in the rail, a ring on its tile, the
  // hash replaced (no history entry per pick).
  var inspectors = document.querySelectorAll('.wb-fl-insp');
  var nameEl = document.querySelector('[data-room-name]');
  var roomTab = document.querySelector('[data-tab="room"]');
  function tab(which) {
    document.querySelectorAll('[data-tab]').forEach(function (t) {
      t.setAttribute('aria-selected', t.dataset.tab === which ? 'true' : 'false');
    });
    document.querySelectorAll('[data-panel]').forEach(function (p) {
      p.hidden = p.dataset.panel !== which;
    });
  }
  function pick(box, setHash) {
    var found = null;
    inspectors.forEach(function (in_) {
      var on = in_.dataset.room === box;
      in_.hidden = !on;
      if (on) found = in_;
    });
    if (!found) return null;
    document.querySelectorAll('.wb-fl-tile--picked').forEach(function (t) { t.classList.remove('wb-fl-tile--picked'); });
    var t = plan.querySelector('[data-box="' + box + '"]');
    if (t) t.classList.add('wb-fl-tile--picked');
    document.querySelectorAll('.wb-fl-rooms [data-pick]').forEach(function (l) {
      if (l.dataset.pick === box) l.setAttribute('aria-current', 'true'); else l.removeAttribute('aria-current');
    });
    if (nameEl) nameEl.textContent = found.dataset.title;
    if (roomTab) roomTab.setAttribute('href', '#room-' + box);
    tab('room');
    if (setHash !== false) history.replaceState(null, '', '#room-' + box);
    return found;
  }
  document.addEventListener('click', function (e) {
    var a = e.target.closest('[data-pick]');
    if (a) { e.preventDefault(); pick(a.dataset.pick); document.querySelector('.wb-fl-rail-body').scrollTop = 0; return; }
    var tb = e.target.closest('[data-tab], [data-tab-go]');
    if (tb) { e.preventDefault(); tab(tb.dataset.tab || tb.dataset.tabGo); }
  });

  // Landing: #room-<box>, or an id inside an inspector (#dept-<key> from
  // Needs you and Check now) opens that room and scrolls to the element.
  function land() {
    var h = decodeURIComponent(location.hash.slice(1));
    var open = document.querySelector('.wb-fl-insp:not([hidden])');
    if (h === 'yolo') { tab('yolo'); }
    else if (h.indexOf('room-') === 0 && pick(h.slice(5), false)) { /* picked */ }
    // #dept-<key> with no element of that id (a room without connectors):
    // open the department's room.
    else if (h.indexOf('dept-') === 0 && !document.getElementById(h) && pick(h.slice(5), false)) { /* picked */ }
    else if (h) {
      var el = document.getElementById(h);
      var in_ = el && el.closest('.wb-fl-insp');
      if (in_) {
        pick(in_.dataset.room, false);
        if (el.tagName === 'DETAILS') el.open = true;
        // Scroll the rail only: scrollIntoView would also scroll the
        // overflow-hidden app and shift the whole page.
        var body = document.querySelector('.wb-fl-rail-body');
        if (body) body.scrollTop += el.getBoundingClientRect().top - body.getBoundingClientRect().top;
      } else if (open) pick(open.dataset.room, false);
    } else if (open) pick(open.dataset.room, false);
  }

  fit();
  land();
  window.addEventListener('hashchange', land);
  window.addEventListener('resize', fit);
  // The rail's tabs start on Room unless the hash says After YOLO.
  if (location.hash !== '#yolo') tab('room');
})();
