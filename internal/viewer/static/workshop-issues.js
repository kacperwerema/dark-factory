// The crate store's New issue dialog (wb/create-issue): the classic create
// modal's behaviour on the Workshop Board. The body follows the status's
// template until you type in it; Create posts to /issues/create and opens
// the new issue's station.
(function () {
  var dlg = document.querySelector('[data-create]');
  if (!dlg) return;
  var form = dlg.querySelector('form');
  var title = dlg.querySelector('#newissue-title');
  var status = dlg.querySelector('#newissue-status');
  var body = dlg.querySelector('#newissue-body');
  var err = dlg.querySelector('[data-create-error]');
  var submit = dlg.querySelector('[data-create-submit]');
  var templates = {};
  try { templates = JSON.parse(dlg.getAttribute('data-templates') || '{}') || {}; } catch (e) { templates = {}; }
  var dirty = false;

  function fill() { if (!dirty && status) body.value = templates[status.value] || ''; }
  function fail(text) { err.textContent = text; err.hidden = false; submit.disabled = false; }

  function open() {
    err.hidden = true;
    dirty = false;
    fill();
    if (typeof dlg.showModal === 'function') dlg.showModal(); else dlg.setAttribute('open', '');
    window.__createModalOpen = true;
    title.focus();
  }
  function close() {
    if (typeof dlg.close === 'function') dlg.close(); else dlg.removeAttribute('open');
    window.__createModalOpen = false;
  }

  body.addEventListener('input', function () { dirty = true; });
  if (status) status.addEventListener('change', fill);
  dlg.addEventListener('close', function () { window.__createModalOpen = false; });
  dlg.querySelector('[data-create-cancel]').addEventListener('click', close);
  document.querySelectorAll('[data-create-open]').forEach(function (a) {
    a.addEventListener('click', function (e) { e.preventDefault(); open(); });
  });

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var t = title.value.trim();
    if (!t) return fail('Write a title first.');
    if (!body.value.trim()) return fail('Write a body first: the issue needs one.');
    var val = function (id) { var el = dlg.querySelector(id); return el ? el.value : ''; };
    var labels = val('#newissue-labels').split(',').map(function (l) { return l.trim(); }).filter(Boolean);
    submit.disabled = true;
    err.hidden = true;
    fetch(dlg.getAttribute('data-post'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: t, status: status ? status.value : '', system: val('#newissue-system'),
        priority: val('#newissue-priority'), labels: labels, body: body.value, type: val('#newissue-type') })
    }).then(function (res) {
      if (res.ok) {
        return res.json().then(function (d) {
          window.__createModalOpen = false;
          location.href = dlg.getAttribute('data-post').replace(/\/issues\/create$/, '/issue/') + d.slug;
        });
      }
      return res.text().then(function (text) { fail(text.trim() || 'Create failed (' + res.status + ').'); });
    }).catch(function (e2) { fail('The viewer did not answer: ' + e2); });
  });

  // ?new=1 (the hall's "+" or an old link) opens the dialog on load.
  if (dlg.hasAttribute('data-open')) {
    var q = new URLSearchParams(location.search);
    q.delete('new');
    var rest = q.toString();
    history.replaceState(null, '', location.pathname + (rest ? '?' + rest : '') + location.hash);
    open();
  }
})();
