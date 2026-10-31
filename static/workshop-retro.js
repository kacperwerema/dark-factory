// Workshop Board, the hall's Retro page: the Claude | Codex choice and "Let
// an agent pre-sort" (POST retros/review), close / reopen a retro (POST
// retros/retro/<file>/status), and the browser-only marks of findings sent
// to the CEO. Sending is a form[data-send] that workshop.js posts to
// lead/send; it fires wb:sent when the CEO got it.
(function () {
  var page = document.querySelector('[data-testid="retro.page"]');
  var project = page ? page.getAttribute('data-project') : '';

  // Claude | Codex, then the review agent.
  document.querySelectorAll('[data-presort]').forEach(function (box) {
    var agent = 'claude';
    box.querySelectorAll('[data-agent]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        agent = btn.getAttribute('data-agent');
        box.querySelectorAll('[data-agent]').forEach(function (b) {
          var on = b === btn;
          b.classList.toggle('wb-seg-item--on', on);
          b.setAttribute('aria-pressed', on);
        });
      });
    });
    var run = box.querySelector('[data-presort-run]');
    var msg = box.querySelector('.wb-presort-msg');
    run.addEventListener('click', function () {
      run.disabled = true;
      msg.textContent = 'Starting ' + agent + '…';
      fetch(box.getAttribute('data-presort'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ agent: agent }) })
        .then(function (r) { return r.json(); })
        .then(function (res) {
          if (res.status !== 'dispatched') throw new Error(res.status || 'not started');
          msg.textContent = agent + ' is sorting in ' + res.session + '. It marks what it is sure of processed.';
        })
        .catch(function (e) { msg.textContent = 'Not started: ' + e.message; run.disabled = false; });
    });
  });

  // Close the retro as processed / reopen it.
  document.querySelectorAll('[data-retro-status]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var msg = btn.parentNode.querySelector('.wb-site-msg');
      btn.disabled = true;
      fetch(btn.getAttribute('data-retro-status'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ status: btn.getAttribute('data-status') }) })
        .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); location.reload(); })
        .catch(function (e) { msg.textContent = 'Not saved: ' + e.message; btn.disabled = false; });
    });
  });

  // A tool bug report's status (POST retros/bug/<file>/status), then reload.
  document.querySelectorAll('[data-bug-status]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var msg = btn.parentNode.querySelector('.wb-site-msg');
      btn.disabled = true;
      fetch(btn.getAttribute('data-bug-status'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ status: btn.getAttribute('data-status') }) })
        .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); location.reload(); })
        .catch(function (e) { msg.textContent = 'Not saved: ' + e.message; btn.disabled = false; });
    });
  });

  // Sends marked in this browser: localStorage, per project and finding.
  function key(form) { return 'wb-retro-sent:' + project + ':' + form.getAttribute('data-mark'); }
  function store(k, on) { try { if (on) localStorage.setItem(k, '1'); else localStorage.removeItem(k); } catch (e) {} }
  function marked(k) { try { return localStorage.getItem(k) === '1'; } catch (e) { return false; } }
  function show(form, on) {
    form.querySelector('[type=submit]').hidden = on;
    form.querySelector('[data-unmark]').hidden = !on;
    var card = form.closest('[data-finding]');
    if (card) card.classList.toggle('wb-rfind--done', on);
  }
  function recount() {
    var cards = document.querySelectorAll('[data-finding]');
    var done = 0;
    var bins = {};
    cards.forEach(function (card) {
      var on = card.classList.contains('wb-rfind--done');
      if (on) done++;
      var seg = document.querySelector('[data-seg="' + card.getAttribute('data-finding') + '"]');
      if (seg) seg.classList.toggle('wb-rprog-seg--on', on);
      var b = card.getAttribute('data-bin');
      bins[b] = (bins[b] || 0) + (on ? 1 : 0);
    });
    var num = document.querySelector('[data-retro-done]');
    if (num) num.textContent = done + '/' + cards.length;
    var close = document.querySelector('[data-status="processed"]');
    if (close) close.classList.toggle('wb-rprog-close--ready', cards.length > 0 && done === cards.length);
    document.querySelectorAll('[data-bin-key]').forEach(function (bin) {
      var n = bin.querySelector('[data-bin-done]');
      if (n) n.textContent = bins[bin.getAttribute('data-bin-key')] || 0;
    });
  }
  document.querySelectorAll('form[data-mark]').forEach(function (form) {
    if (marked(key(form))) show(form, true);
    form.addEventListener('wb:sent', function () { store(key(form), true); show(form, true); recount(); });
    form.querySelector('[data-unmark]').addEventListener('click', function () {
      store(key(form), false); show(form, false); recount();
      var msg = form.querySelector('.wb-site-msg');
      if (msg) msg.textContent = 'Unmarked here; the CEO still has the message.';
    });
  });
  recount();
})();
