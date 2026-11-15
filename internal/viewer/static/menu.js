// The ⋯ More menu (wb-menu, docs/UI/workshop-board.md "One primary action").
// A <details class="wb-menu"> opens and closes by its summary with no script;
// this only closes it: on a pick inside it, on Esc (focus back to its
// summary) and on a click outside. Opening one closes the others.
(function () {
  'use strict';
  function menus() { return document.querySelectorAll('details.wb-menu[open]'); }
  function closeAll(except) {
    menus().forEach(function (m) { if (m !== except) m.open = false; });
  }
  document.addEventListener('toggle', function (ev) {
    var m = ev.target;
    if (m.matches && m.matches('details.wb-menu') && m.open) closeAll(m);
  }, true);
  document.addEventListener('click', function (ev) {
    var open = menus();
    if (!open.length) return;
    var inMenu = ev.target.closest ? ev.target.closest('details.wb-menu') : null;
    if (!inMenu) { closeAll(null); return; }
    // A pick: a control inside the list (not the summary). Close after the
    // control's own handler has run.
    var pick = ev.target.closest('.wb-menu-list button, .wb-menu-list a, .wb-menu-list [role=menuitem]');
    if (pick && !pick.disabled) setTimeout(function () { inMenu.open = false; }, 0);
  });
  document.addEventListener('keydown', function (ev) {
    if (ev.key !== 'Escape') return;
    menus().forEach(function (m) {
      m.open = false;
      var s = m.querySelector('summary');
      if (s && m.contains(document.activeElement)) s.focus();
    });
  });
})();
