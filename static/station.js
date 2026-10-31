// The station (L3): the owner's buttons. Every one posts to an endpoint the
// classic issue page already uses: approve (the stamp, with your words),
// comments (an answer, send back for fixes), check-script, agents/open,
// dispatch/reprompt and edit-in-nvim; and, taken over from the classic page,
// the issue update (title, fields, status after the transition preview,
// body), type, delete, dispatch, custom actions and checkbox.
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
    var box = el.closest('form, .wb-step, .wb-st-confirm, .wb-sagent, .wb-bodyidx, .wb-fields, .wb-panel');
    var msg = box && box.querySelector('.wb-site-msg');
    if (msg) msg.textContent = text;
  }
  function stepError(body) {
    var bad = (body.steps || []).filter(function (s) { return s.status === 'error'; })[0];
    return bad ? bad.name + (bad.detail ? ': ' + bad.detail : '') : (body.error || body.message || 'unknown error');
  }

  // The clarify round: every question's picked option and words, posted
  // together; each answer is recorded on its own and the reply says which.
  document.querySelectorAll('[data-clarify-send]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var card = btn.closest('[data-clarify-url]');
      var answers = [];
      card.querySelectorAll('fieldset[data-clarify-q]:not([disabled])').forEach(function (q) {
        var pick = q.querySelector('input[type="radio"]:checked');
        if (!pick) return;
        var words = q.querySelector('[data-clarify-words]');
        answers.push({ id: q.getAttribute('data-clarify-q'), option: parseInt(pick.value, 10), words: words ? words.value : '', text: q.getAttribute('data-text') });
      });
      if (!answers.length) { say(btn, 'Pick an option first.'); return; }
      btn.disabled = true;
      say(btn, 'Recording…');
      post(card.getAttribute('data-clarify-url'), { answers: answers }).then(function (res) {
        var bad = (res.body.results || []).filter(function (r) { return r.error; });
        if (!res.ok && !bad.length) { btn.disabled = false; say(btn, res.body.error || 'Could not record the answers.'); return; }
        if (bad.length) { btn.disabled = false; say(btn, bad.map(function (r) { return r.error; }).join(' · ')); return; }
        location.reload();
      }, function () { btn.disabled = false; say(btn, 'The viewer did not answer.'); });
    });
  });

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
  function get(url) {
    return fetch(url).then(function (r) {
      return r.text().then(function (t) {
        var j = {};
        try { j = JSON.parse(t); } catch (e) { j = { error: t }; }
        return { ok: r.ok, status: r.status, body: j };
      });
    });
  }
  function postStatus(url, body) {
    return fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}) })
      .then(function (r) {
        return r.text().then(function (t) {
          var j = {};
          try { j = JSON.parse(t); } catch (e) { j = { error: t.trim() }; }
          return { ok: r.ok, status: r.status, body: j };
        });
      });
  }
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function reloadSoon(ms) { setTimeout(function () { location.reload(); }, ms || 900); }

  // Rename: the title edits in place; a new slug opens the new URL.
  var renameBtn = document.querySelector('[data-st-rename]');
  var renameForm = document.querySelector('form[data-st-rename-form]');
  if (renameBtn && renameForm) {
    var titleH = document.querySelector('[data-st-title]');
    var input = renameForm.querySelector('input[name=title]');
    function closeRename() { renameForm.hidden = true; titleH.parentNode.hidden = false; }
    renameBtn.addEventListener('click', function () {
      renameForm.hidden = false; titleH.parentNode.hidden = true; input.focus(); input.select();
    });
    renameForm.querySelector('[data-st-rename-cancel]').addEventListener('click', closeRename);
    input.addEventListener('keydown', function (e) { if (e.key === 'Escape') closeRename(); });
    renameForm.addEventListener('submit', function (e) {
      e.preventDefault();
      var title = input.value.trim();
      if (!title) { say(renameForm, 'The title cannot be empty.'); return; }
      say(renameForm, 'Saving…');
      postStatus(renameForm.getAttribute('data-st-rename-form'), { title: title }).then(function (res) {
        if (!res.ok) { say(renameForm, 'Not saved: ' + (res.body.error || res.status)); return; }
        var slug = res.body.slug, old = renameForm.getAttribute('data-slug');
        if (slug && slug !== old) location.href = renameForm.getAttribute('data-prefix') + '/issue/' + slug;
        else location.reload();
      }).catch(function (err) { say(renameForm, 'Not saved: ' + err); });
    });
  }

  // Delete: a confirm line, then the store.
  var delBtn = document.querySelector('[data-st-delete]');
  var delBox = document.querySelector('[data-st-delete-confirm]');
  if (delBtn && delBox) {
    delBtn.addEventListener('click', function () { delBox.hidden = false; });
    delBox.querySelector('[data-st-delete-cancel]').addEventListener('click', function () { delBox.hidden = true; });
    delBox.querySelector('[data-st-delete-go]').addEventListener('click', function () {
      var go = this;
      go.disabled = true;
      say(go, 'Deleting…');
      postStatus(delBtn.getAttribute('data-st-delete')).then(function (res) {
        if (!res.ok) { go.disabled = false; say(go, 'Not deleted: ' + (res.body.error || res.status)); return; }
        location.href = delBtn.getAttribute('data-store');
      }).catch(function (err) { go.disabled = false; say(go, 'Not deleted: ' + err); });
    });
  }

  // Dispatch to Claude or Codex, or run a custom action: the steps show
  // under the buttons. A refusal at a gate says so and offers Dispatch anyway.
  document.querySelectorAll('[data-st-dispatch]').forEach(function (box) {
    var steps = box.querySelector('[data-st-steps]');
    function show(res, label, again) {
      var d = res.body || {};
      steps.hidden = false;
      steps.textContent = '';
      if (d.status === 'waiting-on-gate') {
        var line = el('div', 'wb-st-step--err', 'Waits for your approval of ' + d.gate + ': a new agent would only stop at the gate. Nothing was started. ');
        var a = el('a', null, 'Stamp it');
        a.href = '#move';
        var force = el('button', 'wb-btn wb-btn--link wb-btn--sm', 'Dispatch anyway');
        force.type = 'button';
        force.setAttribute('data-testid', 'station.dispatch.force');
        force.addEventListener('click', function () { again(true); });
        line.append(a, ' or ', force);
        steps.append(line);
        return;
      }
      if (!res.ok && !(d.steps || []).length) { steps.append(el('div', 'wb-st-step--err', label + ' failed: ' + (d.error || d.message || res.status))); return; }
      if (d.status === 'reattached') steps.append(el('div', 'wb-st-step--re', 'The session was already running: attached without sending the prompt again. Use Send the prompt if the agent never got it.'));
      (d.steps || []).forEach(function (st) {
        var cls = st.status === 'ok' ? 'wb-st-step--ok' : st.status === 'reattached' ? 'wb-st-step--re' : 'wb-st-step--err';
        steps.append(el('div', cls, st.name + (st.detail ? ' (' + st.detail + ')' : '')));
      });
      if (d.session) steps.append(el('div', 'wb-note', 'tmux session: ' + d.session));
      if (d.prompt) {
        var det = el('details'); det.append(el('summary', null, 'The prompt sent'), el('pre', null, d.prompt));
        steps.append(det);
      }
    }
    function run(url, body, label, btn) {
      btn.disabled = true;
      steps.hidden = false;
      steps.textContent = label + '…';
      postStatus(url, body).then(function (res) {
        btn.disabled = false;
        show(res, label, function (force) { run(url, Object.assign({}, body, { force: force }), label, btn); });
      }).catch(function (err) { btn.disabled = false; steps.textContent = label + ' failed: ' + err; });
    }
    box.querySelectorAll('[data-st-agent]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var agent = btn.getAttribute('data-st-agent');
        // "" lets the workflow's routing: rules pick the agent.
        run(box.getAttribute('data-st-dispatch'), { agent: agent, force: false }, agent ? 'Dispatching to ' + agent : 'Dispatching (routing)', btn);
      });
    });
    box.querySelectorAll('[data-st-action]').forEach(function (btn) {
      btn.addEventListener('click', function () { run(btn.getAttribute('data-st-action'), {}, btn.getAttribute('data-label'), btn); });
    });
  });

  // The fields: each saves on change. Status goes through the transition
  // preview first, as on the classic board; the type through /type.
  var fields = document.querySelector('[data-st-fields]');
  if (fields) {
    var card = fields.closest('.wb-fields');
    var msg = card.querySelector('[data-st-fields-msg]');
    var url = fields.getAttribute('data-st-fields');
    function note(text) { msg.textContent = text; }
    function save(body, done) {
      note('Saving…');
      postStatus(url, body).then(function (res) {
        if (!res.ok) { note('Not saved: ' + (res.body.error || res.status)); return; }
        note('Saved.');
        if (done) done(res.body);
      }).catch(function (err) { note('Not saved: ' + err); });
    }
    fields.querySelectorAll('[data-st-field]').forEach(function (inp) {
      if (inp.getAttribute('data-st-field') === 'status') return;
      inp.addEventListener('change', function () {
        var b = {}; b[inp.getAttribute('data-st-field')] = inp.value.trim();
        save(b);
      });
      if (inp.tagName === 'INPUT') inp.addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); inp.blur(); } });
    });

    // Labels: × removes, Enter adds; the whole list is saved.
    var labels = fields.querySelector('[data-st-labels]');
    function current() {
      return Array.prototype.map.call(labels.querySelectorAll('[data-label]'), function (p) { return p.getAttribute('data-label'); });
    }
    function pill(name) {
      var p = el('span', 'wb-st-label-pill', name);
      p.setAttribute('data-label', name);
      var x = el('button', null, '×');
      x.type = 'button'; x.setAttribute('aria-label', 'Remove ' + name); x.setAttribute('data-st-label-remove', '');
      p.append(x);
      return p;
    }
    labels.addEventListener('click', function (e) {
      var x = e.target.closest('[data-st-label-remove]');
      if (!x) return;
      x.parentNode.remove();
      save({ labels: current() });
    });
    var addInp = labels.querySelector('[data-st-label-add]');
    addInp.addEventListener('keydown', function (e) {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      var name = addInp.value.trim();
      if (!name || current().indexOf(name) >= 0) { addInp.value = ''; return; }
      labels.insertBefore(pill(name), addInp);
      addInp.value = '';
      save({ labels: current() });
    });

    // Type: a 409 means the status is not on the new path: pick one on it.
    var typeSel = fields.querySelector('[data-st-type]');
    var pick = card.querySelector('[data-st-typepick]');
    function changeType(type, status) {
      var b = { type: type };
      if (status) b.status = status;
      note('Saving…');
      postStatus(fields.getAttribute('data-type-url'), b).then(function (res) {
        if (res.ok) { note('Saved.'); reloadSoon(); return; }
        if (res.status === 409 && res.body.statuses) {
          var sel = pick.querySelector('[data-st-typepick-status]');
          sel.textContent = '';
          res.body.statuses.forEach(function (st) { var o = el('option', null, st); o.value = st; sel.append(o); });
          pick.querySelector('[data-st-typepick-msg]').textContent = 'The status is not on ' + type + "'s path. Move it to:";
          pick.hidden = false;
          note('');
          return;
        }
        note('Not saved: ' + (res.body.error || res.status));
      }).catch(function (err) { note('Not saved: ' + err); });
    }
    if (typeSel) {
      typeSel.addEventListener('change', function () { changeType(typeSel.value); });
      pick.querySelector('[data-st-typepick-go]').addEventListener('click', function () {
        changeType(typeSel.value, pick.querySelector('[data-st-typepick-status]').value);
      });
      pick.querySelector('[data-st-typepick-cancel]').addEventListener('click', function () {
        typeSel.value = fields.getAttribute('data-type'); pick.hidden = true;
      });
    }

    // Status: the preview says what the move needs; Move commits it.
    var statusSel = fields.querySelector('[data-st-field="status"]');
    var form = card.querySelector('form[data-st-preview]');
    var from = fields.getAttribute('data-status');
    var want = '', needsApproval = false;
    function cancelMove() { form.hidden = true; statusSel.value = from; form.classList.remove('wb-st-preview--you'); }
    statusSel.addEventListener('change', function () {
      want = statusSel.value;
      if (want === from) { cancelMove(); return; }
      note('Checking the move…');
      get(fields.getAttribute('data-preview-url') + '?to=' + encodeURIComponent(want)).then(function (res) {
        var p = res.body || {};
        if (!res.ok) { note('Can\'t move: ' + (p.error || res.status)); statusSel.value = from; return; }
        needsApproval = !p.allowed && /not human-approved/i.test(p.validation_error || '');
        note('');
        form.querySelector('[data-st-preview-title]').textContent = from + ' → ' + want + (p.allowed || needsApproval ? '' : ': blocked');
        var list = form.querySelector('[data-st-preview-steps]');
        list.textContent = '';
        (p.steps || []).forEach(function (st) {
          var cls = st.outcome === 'failed' ? 'wb-st-step--err' : st.outcome === 'passed' || st.outcome === 'changed' ? 'wb-st-step--ok' : 'wb-note';
          list.append(el('li', cls, st.summary + (st.message ? ': ' + st.message : '')));
        });
        if (!p.allowed && !needsApproval) list.append(el('li', 'wb-st-step--err', p.validation_error || 'The workflow does not allow this move.'));
        var fl = form.querySelector('[data-st-preview-fields]');
        fl.textContent = '';
        (p.fields || []).forEach(function (f) {
          var lab = el('label', 'wb-words-label', f.prompt + (f.required ? ' *' : ''));
          var inp = f.type === 'multiline' ? el('textarea', 'wb-words') : el('input', 'wb-st-input');
          if (f.type !== 'multiline') inp.type = 'text';
          inp.setAttribute('data-field-name', f.name);
          if (f.required) inp.setAttribute('data-required', '');
          lab.append(inp);
          fl.append(lab);
        });
        form.querySelector('[data-st-preview-approve]').hidden = !needsApproval;
        form.classList.toggle('wb-st-preview--you', needsApproval);
        var go = form.querySelector('[data-st-preview-go]');
        go.disabled = !p.allowed && !needsApproval;
        go.textContent = needsApproval ? 'Approve and move' : 'Move';
        form.querySelector('.wb-site-msg').textContent = '';
        form.hidden = false;
      }).catch(function (err) { note('Can\'t check the move: ' + err); statusSel.value = from; });
    });
    form.querySelector('[data-st-preview-cancel]').addEventListener('click', cancelMove);
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var answers = {};
      var inputs = form.querySelectorAll('[data-field-name]');
      for (var i = 0; i < inputs.length; i++) {
        var v = inputs[i].value.trim();
        if (inputs[i].hasAttribute('data-required') && !v) { say(form, 'Fill in: ' + inputs[i].parentNode.firstChild.textContent.replace(/ \*$/, '')); inputs[i].focus(); return; }
        answers[inputs[i].getAttribute('data-field-name')] = v;
      }
      var go = form.querySelector('[data-st-preview-go]');
      go.disabled = true;
      function commit() {
        say(form, 'Moving…');
        postStatus(url, { status: want, fields: answers }).then(function (res) {
          go.disabled = false;
          if (!res.ok) { say(form, 'Not moved: ' + (res.body.error || res.status)); return; }
          say(form, 'Moved to ' + want + '.');
          reloadSoon(1200);
        }).catch(function (err) { go.disabled = false; say(form, 'Not moved: ' + err); });
      }
      if (!needsApproval) { commit(); return; }
      say(form, 'Approving…');
      var words = form.querySelector('textarea[name=words]').value.trim();
      postStatus(fields.getAttribute('data-approve-url'), { status: want, words: words }).then(function (res) {
        if (!res.ok || res.body.human_approval !== want) { go.disabled = false; say(form, 'Not approved: ' + (res.body.error || res.status)); return; }
        commit();
      }).catch(function (err) { go.disabled = false; say(form, 'Not approved: ' + err); });
    });
  }

  // The body: folded by section; a box ticks its line in the file; Edit
  // saves the whole body. Both send the hash of the body the page started
  // from, and a 409 (the file changed since) says to reload.
  var body = document.querySelector('[data-st-body]');
  if (body) {
    var bmsg = body.querySelector('[data-st-body-msg]');
    var view = body.querySelector('[data-st-body-view]');
    var editor = body.querySelector('form[data-st-editor]');
    var hash = body.getAttribute('data-body-hash');
    body.querySelectorAll('.wb-st-md input[type=checkbox][data-line]').forEach(function (cb) {
      cb.disabled = false;
      cb.addEventListener('change', function () {
        bmsg.textContent = 'Saving…';
        postStatus(body.getAttribute('data-checkbox-url'), { line: parseInt(cb.dataset.line, 10), checked: cb.checked, base_body_hash: hash }).then(function (res) {
          if (res.ok) { hash = res.body.body_hash || hash; editor.querySelector('textarea').value = res.body.body || editor.querySelector('textarea').value; bmsg.textContent = 'Saved.'; return; }
          cb.checked = !cb.checked;
          bmsg.textContent = (res.body.error || 'Not saved.') + (res.status === 409 ? ' Reloading…' : '');
          if (res.status === 409) reloadSoon(2500);
        }).catch(function (err) { cb.checked = !cb.checked; bmsg.textContent = 'Not saved: ' + err; });
      });
    });
    var expand = body.querySelector('[data-st-expand]');
    expand.addEventListener('click', function () {
      var secs = body.querySelectorAll('details.wb-st-sec');
      var open = Array.prototype.some.call(secs, function (d) { return !d.open; });
      secs.forEach(function (d) { d.open = open; });
      expand.textContent = open ? 'Fold all' : 'Expand all';
    });
    body.querySelector('[data-st-edit]').addEventListener('click', function () {
      editor.hidden = false; view.hidden = true; editor.querySelector('textarea').focus();
    });
    editor.querySelector('[data-st-edit-cancel]').addEventListener('click', function () { editor.hidden = true; view.hidden = false; });
    editor.addEventListener('submit', function (e) {
      e.preventDefault();
      bmsg.textContent = 'Saving…';
      postStatus(body.getAttribute('data-st-body'), { body: editor.querySelector('textarea').value, base_body_hash: hash }).then(function (res) {
        if (!res.ok) { bmsg.textContent = 'Not saved: ' + (res.body.error || res.status) + (res.status === 409 ? ' Copy your text, then reload.' : ''); return; }
        bmsg.textContent = 'Saved.';
        reloadSoon();
      }).catch(function (err) { bmsg.textContent = 'Not saved: ' + err; });
    });
    // #body-<section> (the body index's links) opens that section.
    function openFromHash() {
      var id = decodeURIComponent(location.hash.slice(1));
      if (id.indexOf('body-') !== 0) return;
      var d = document.getElementById(id);
      if (d && d.tagName === 'DETAILS') { d.open = true; d.scrollIntoView({ block: 'start' }); }
    }
    window.addEventListener('hashchange', openFromHash);
    openFromHash();
  }
})();
