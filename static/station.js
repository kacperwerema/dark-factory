// The station (L3): the owner's buttons. Every one posts to an endpoint the
// classic issue page already uses: approve (the stamp, with your words),
// comments (an answer, send back for fixes), check-script, agents/open,
// dispatch/reprompt and edit-in-nvim.
(function () {
  function post(url, body) {
    return fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}) })
      .then(function (r) {
        return r.text().then(function (t) {
          var j = {};
          try { j = JSON.parse(t); } catch (e) { j = { error: t }; }
          return { ok: r.ok, body: j };
        });
      });
  }
  function say(el, text) {
    var box = el.closest('form, .wb-step, .wb-sagent, .wb-bodyidx');
    var msg = box && box.querySelector('.wb-site-msg');
    if (msg) msg.textContent = text;
  }
  function stepError(body) {
    var bad = (body.steps || []).filter(function (s) { return s.status === 'error'; })[0];
    return bad ? bad.name + (bad.detail ? ': ' + bad.detail : '') : (body.error || body.message || 'unknown error');
  }

  // Copy a command.
  document.querySelectorAll('[data-copy]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var text = btn.getAttribute('data-copy');
      if (navigator.clipboard) {
        navigator.clipboard.writeText(text).then(function () { say(btn, 'Copied.'); }, function () { say(btn, 'Copy is not available here; select the command instead.'); });
      } else {
        say(btn, 'Copy is not available here; select the command instead.');
      }
    });
  });

  // Run the guided checks: the viewer opens tmux window check-script-<slug>.
  document.querySelectorAll('[data-run-checks]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      btn.disabled = true;
      say(btn, 'Starting the checks…');
      post(btn.getAttribute('data-run-checks')).then(function (res) {
        btn.disabled = false;
        var d = res.body;
        if (d.status === 'no-scripts') say(btn, 'No check script names this issue.');
        else if (d.status === 'error' || !res.ok) say(btn, 'Run checks failed: ' + stepError(d));
        else if ((d.steps || []).some(function (s) { return s.name.indexOf('Terminal: headless') === 0; })) say(btn, 'Started ' + d.session + '. No terminal configured. Inside tmux: ' + d.switch_cmd);
        else say(btn, (d.status === 'already-running' ? 'Brought back ' : 'Started ') + d.session + '.');
      }).catch(function (e) { btn.disabled = false; say(btn, 'Run checks failed: ' + e); });
    });
  });

  // Open the agent's session in tmux.
  document.querySelectorAll('[data-open-session]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var name = btn.getAttribute('data-open-session');
      btn.disabled = true;
      post(btn.getAttribute('data-open-url'), { session: name }).then(function (res) {
        btn.disabled = false;
        var d = res.body;
        if (d.status === 'no-session') say(btn, name + ' is not running.');
        else if (d.status === 'selected' || (d.steps || []).some(function (s) { return s.name.indexOf('Terminal: headless') === 0; })) say(btn, 'No terminal configured. Inside tmux: ' + d.switch_cmd);
        else say(btn, 'Opened ' + name + '.');
      }).catch(function (e) { btn.disabled = false; say(btn, 'Open failed: ' + e); });
    });
  });

  // Send the prompt again into the running session.
  document.querySelectorAll('[data-reprompt]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      if (!confirm('Re-send the current briefing into the running agent session?\n\nIf the agent is busy, it is queued as its next message.')) return;
      btn.disabled = true;
      say(btn, 'Sending the prompt…');
      post(btn.getAttribute('data-reprompt')).then(function (res) {
        btn.disabled = false;
        say(btn, res.ok && res.body.status !== 'error' ? 'Prompt sent to ' + (res.body.session || 'the agent') + '.' : 'Not sent: ' + stepError(res.body));
      }).catch(function (e) { btn.disabled = false; say(btn, 'Not sent: ' + e); });
    });
  });

  // Edit the body in nvim.
  document.querySelectorAll('[data-nvim]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      post(btn.getAttribute('data-nvim')).then(function (res) {
        say(btn, res.ok ? (res.body.message || 'Opened in nvim.') : (res.body.error || 'Failed to open nvim.'));
      }).catch(function (e) { say(btn, 'Failed to open nvim: ' + e); });
    });
  });

  // Answer the agent's question: a comment on the issue.
  document.querySelectorAll('form[data-answer]').forEach(function (form) {
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var text = form.querySelector('textarea').value.trim();
      if (!text) { say(form, 'Write your answer first.'); return; }
      say(form, 'Saving…');
      post(form.getAttribute('data-answer'), { block: 0, text: text }).then(function (res) {
        if (!res.ok) { say(form, 'Not saved: ' + stepError(res.body)); return; }
        say(form, 'Answer saved.');
        setTimeout(function () { location.reload(); }, 1200);
      }).catch(function (err) { say(form, 'Not saved: ' + err); });
    });
  });

  // The stamp: approve the gate with your words (POST …/approve, the same
  // call as the classic page's approve buttons). Approving what is already
  // approved takes the stamp back.
  document.querySelectorAll('form[data-stamp]').forEach(function (form) {
    var go = form.querySelector('[data-testid="station.stamp.go"]');
    var words = form.querySelector('textarea[name=words]');
    function chosen() {
      var c = form.querySelector('input[name=status]:checked');
      return c ? c.value : '';
    }
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var status = chosen();
      if (!status) { say(form, 'Tick the move you approve first.'); return; }
      go.disabled = true;
      say(form, 'Stamping…');
      post(form.getAttribute('data-stamp'), { status: status, words: words.value.trim() }).then(function (res) {
        go.disabled = false;
        if (!res.ok) { say(form, 'Not stamped: ' + stepError(res.body)); return; }
        var d = res.body;
        var what = d.human_approval ? 'Stamped → ' + d.human_approval : 'Stamp taken back';
        if (d.words_saved) what += ', with your words';
        if (d.words_error) what += ', but your words were not saved: ' + d.words_error;
        if (d.notified_session) what += '. Told ' + d.notified_session;
        say(form, what + '.');
        setTimeout(function () { location.reload(); }, 1500);
      }).catch(function (err) { go.disabled = false; say(form, 'Not stamped: ' + err); });
    });

    // Send back for fixes: the issue goes back a status with your words,
    // and the worker is told.
    var back = form.querySelector('[data-testid="station.stamp.back"]');
    back.addEventListener('click', function () {
      var text = words.value.trim();
      if (!text) { say(form, 'Write what to fix in the box first.'); words.focus(); return; }
      back.disabled = true;
      post(form.getAttribute('data-send-back'), { text: text }).then(function (res) {
        back.disabled = false;
        if (!res.ok) { say(form, 'Not sent back: ' + stepError(res.body)); return; }
        var d = res.body;
        var what = d.back_to === d.from ? 'Not approved, stays in ' + d.back_to : 'Sent back to ' + d.back_to;
        if (d.notified_session) what += '. Told ' + d.notified_session;
        else if (d.notification_error) what += ', ' + d.notification_error;
        words.value = '';
        say(form, what + '.');
        setTimeout(function () { location.reload(); }, 1500);
      }).catch(function (err) { back.disabled = false; say(form, 'Not sent: ' + err); });
    });
  });
})();
