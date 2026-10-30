// The CEO chat (/p/<project>/lead): polls the live part, sends messages and
// keys to the lead, and keeps what the owner is typing and the scroll
// position across updates. Spawning the CEO is workshop.js's data-spawn-lead.
(function () {
  var main = document.querySelector('[data-live-url]');
  if (!main) return;
  var form = document.querySelector('form[data-ceo-send]');
  var text = form.querySelector('textarea');
  var status = form.querySelector('.wb-site-msg');
  var scroll = document.querySelector('[data-scroll]');
  var keyURL = form.getAttribute('data-key-url');
  var last = {};
  var live = null;

  function atBottom(el) { return el.scrollHeight - el.scrollTop - el.clientHeight < 40; }
  function toBottom() { scroll.scrollTop = scroll.scrollHeight; }
  function say(msg) { status.textContent = msg; }

  function post(url, body) {
    return fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
      .then(function (r) { return r.json().catch(function () { return {}; }).then(function (j) { return { ok: r.ok, body: j }; }); });
  }

  function setMode(mode) {
    document.querySelectorAll('[data-mode-label]').forEach(function (el) { el.textContent = mode; });
  }

  // Each <template data-part> replaces its slot only when it changed, so a
  // quiet CEO doesn't reset a hover or a half-read paragraph.
  function apply(html) {
    var doc = new DOMParser().parseFromString(html, 'text/html');
    var root = doc.querySelector('[data-live]');
    if (!root) return;
    var nowLive = root.getAttribute('data-live');
    if (live !== null && nowLive !== live) { location.reload(); return; }
    live = nowLive;
    setMode(root.getAttribute('data-mode'));
    root.querySelectorAll('template[data-part]').forEach(function (t) {
      var name = t.getAttribute('data-part');
      var slot = document.querySelector('[data-part-slot="' + name + '"]');
      if (!slot || last[name] === t.innerHTML) return;
      // A question card the owner has started answering stays as it is
      // while the same question is still open.
      if (name === 'prompt' && keepAskCard(slot, t)) return;
      var stick = name === 'conv' && atBottom(scroll);
      last[name] = t.innerHTML;
      slot.innerHTML = t.innerHTML;
      if (stick) toBottom();
    });
  }

  function keepAskCard(slot, t) {
    var mine = slot.querySelector('form[data-ask-id][data-dirty]');
    if (!mine) return false;
    var next = t.content.querySelector('form[data-ask-id="' + mine.getAttribute('data-ask-id') + '"]');
    return !!next && !next.querySelector('[data-ask-pick][disabled]');
  }

  // askAnswers reads a question card: per question the options picked (1 is
  // the first) and the Other text.
  function askAnswers(form) {
    return Array.prototype.map.call(form.querySelectorAll('[data-ask-q]'), function (set) {
      var picks = Array.prototype.filter.call(set.querySelectorAll('[data-ask-pick]'), function (b) {
        return b.getAttribute('aria-pressed') === 'true';
      }).map(function (b) { return parseInt(b.getAttribute('data-ask-pick'), 10); });
      return { picks: picks, other: set.querySelector('[data-ask-other]').value.trim() };
    });
  }

  function pickOption(btn) {
    var set = btn.closest('[data-ask-q]');
    var on = btn.getAttribute('aria-pressed') !== 'true';
    if (!set.getAttribute('data-multi')) {
      set.querySelectorAll('[data-ask-pick]').forEach(function (b) {
        b.setAttribute('aria-pressed', 'false');
        b.classList.remove('wb-choice-opt--on');
      });
      if (on) set.querySelector('[data-ask-other]').value = '';
    }
    btn.setAttribute('aria-pressed', on ? 'true' : 'false');
    btn.classList.toggle('wb-choice-opt--on', on);
    btn.closest('form').setAttribute('data-dirty', '1');
  }

  function submitAsk(form) {
    var msg = form.querySelector('[data-ask-msg]');
    var answers = askAnswers(form);
    var missing = answers.findIndex(function (a) { return !a.picks.length && !a.other; });
    if (missing >= 0) { msg.textContent = 'Question ' + (missing + 1) + ' has no answer yet.'; return; }
    msg.textContent = 'Answering…';
    post(form.getAttribute('data-ask-url'), { id: form.getAttribute('data-ask-id'), answers: answers }).then(function (res) {
      if (!res.ok) { msg.textContent = 'Not answered: ' + (res.body.error || 'error'); return; }
      form.removeAttribute('data-dirty');
      msg.textContent = 'Answered in ' + (res.body.session || 'the CEO') + '.';
      setTimeout(poll, 600);
    }).catch(function (e) { msg.textContent = 'Not answered: ' + e; });
  }

  document.addEventListener('submit', function (e) {
    var form = e.target.closest('form[data-ask-id]');
    if (!form) return;
    e.preventDefault();
    submitAsk(form);
  });
  document.addEventListener('input', function (e) {
    var other = e.target.closest('[data-ask-other]');
    if (!other) return;
    var set = other.closest('[data-ask-q]');
    // One answer for a single-choice question: typing Other clears a pick.
    if (!set.getAttribute('data-multi') && other.value) {
      set.querySelectorAll('[data-ask-pick]').forEach(function (b) {
        b.setAttribute('aria-pressed', 'false');
        b.classList.remove('wb-choice-opt--on');
      });
    }
    other.closest('form').setAttribute('data-dirty', '1');
  });

  function poll() {
    if (document.hidden) return;
    fetch(main.getAttribute('data-live-url'), { cache: 'no-store' })
      .then(function (r) { return r.ok ? r.text() : null; })
      .then(function (html) { if (html) apply(html); })
      .catch(function () {});
  }

  function pressKey(key, btn) {
    if (btn && btn.hasAttribute('data-confirm') && !window.confirm(btn.getAttribute('data-confirm'))) return;
    return post(keyURL, { key: key }).then(function (res) {
      if (!res.ok) { say('Not pressed: ' + (res.body.error || 'error')); return; }
      say('Pressed ' + key + ' in ' + (res.body.session || 'the CEO') + '.');
      setTimeout(poll, 300);
    }).catch(function (e) { say('Not pressed: ' + e); });
  }

  function send(now) {
    var msg = text.value.replace(/\s+$/, '');
    if (!msg.trim()) return;
    say('Sending…');
    post(form.getAttribute('data-ceo-send'), { text: msg }).then(function (res) {
      if (!res.ok || res.body.status === 'error') {
        var step = (res.body.steps || [])[0];
        say('Not sent' + (res.body.error ? ': ' + res.body.error : step ? ': ' + step.name + (step.detail ? ' (' + step.detail + ')' : '') : '.'));
        return;
      }
      text.value = '';
      say('Sent to ' + (res.body.session || 'the CEO') + '.');
      if (now) pressKey('SendNow');
      toBottom();
      setTimeout(poll, 300);
    }).catch(function (e) { say('Not sent: ' + e); });
  }

  form.addEventListener('submit', function (e) { e.preventDefault(); send(false); });
  text.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); send(false); }
  });

  document.addEventListener('click', function (e) {
    var btn = e.target.closest('button');
    if (!btn || btn.disabled) return;
    if (btn.hasAttribute('data-send-now')) { send(true); return; }
    if (btn.hasAttribute('data-ask-pick')) { pickOption(btn); return; }
    if (btn.hasAttribute('data-key')) { pressKey(btn.getAttribute('data-key'), btn); return; }
    if (btn.hasAttribute('data-answer')) {
      // The owner's reply starts in the box; nothing is sent until Send.
      var start = 'Re: "' + btn.getAttribute('data-answer') + '" ';
      text.value = text.value ? text.value + '\n' + start : start;
      text.focus();
      text.setSelectionRange(text.value.length, text.value.length);
      return;
    }
    if (btn.hasAttribute('data-view')) { showView(btn.getAttribute('data-view')); return; }
    if (btn.hasAttribute('data-open-tmux')) {
      say('Opening ' + btn.getAttribute('data-session') + '…');
      post(btn.getAttribute('data-open-tmux'), { session: btn.getAttribute('data-session') }).then(function (res) {
        var b = res.body || {};
        if (!res.ok) { say('Not opened: ' + ((b.steps || [])[0] || {}).name); return; }
        say(b.status === 'already-open' ? 'Already open in a terminal.' : 'Opened. From a terminal: ' + (b.attach_cmd || ''));
      }).catch(function (e) { say('Not opened: ' + e); });
    }
  });

  function showView(view) {
    document.querySelectorAll('[data-view]').forEach(function (b) {
      var on = b.getAttribute('data-view') === view;
      b.classList.toggle('wb-toggle-on', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
    });
    document.querySelectorAll('[data-view-pane]').forEach(function (p) { p.hidden = p.getAttribute('data-view-pane') !== view; });
    try { localStorage.setItem('ceo-chat-view', view); } catch (e) {}
  }

  // The page arrives with every part rendered: polls replace only what
  // changed since.
  document.querySelectorAll('[data-part-slot]').forEach(function (slot) {
    var t = document.createElement('template');
    t.innerHTML = slot.innerHTML;
    last[slot.getAttribute('data-part-slot')] = t.innerHTML;
  });
  live = main.getAttribute('data-live-state');

  try { var saved = localStorage.getItem('ceo-chat-view'); if (saved) showView(saved); } catch (e) {}
  setMode((document.querySelector('[data-testid="lead_chat.mode"] [data-mode-label]') || {}).textContent || 'default');
  toBottom();
  setInterval(poll, 1500);
})();
