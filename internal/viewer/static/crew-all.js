// Workshop Board, the Crew page across projects (/crew): the owner's
// actions, each behind a confirm or a sheet. Stop and Start a lead, Pause /
// Resume ask first in a confirm box placed under the row; Nudge and Tail
// open a bottom sheet. Every answer is JSON {ok, message, at}; the row's
// status line shows it. The routes refuse without the owner's sign-in.
// While a confirm or sheet is open ([data-hold-open]) workshop.js does not
// reload the page.
(function () {
  var live = document.querySelector('[data-checked]');
  if (live) {
    var at = Number(live.getAttribute('data-checked')) || Date.now();
    var tick = function () {
      var s = Math.max(0, Math.round((Date.now() - at) / 1000));
      live.textContent = 'live · tmux checked ' + (s < 2 ? 'just now' : s < 90 ? s + 's ago' : Math.round(s / 60) + ' min ago');
    };
    tick();
    setInterval(tick, 1000);
  }

  function post(url, body) {
    return fetch(url, {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify(body || {})
    }).then(function (r) {
      return r.text().then(function (t) {
        var j = {};
        try { j = JSON.parse(t); } catch (e) { j = { message: t.trim() }; }
        if (j.error && !j.message) j.message = j.error;
        if (typeof j.ok !== 'boolean') j.ok = r.ok;
        return j;
      });
    });
  }

  function say(row, ok, text) {
    var box = row && row.querySelector('[role=status]');
    if (!box) return;
    box.textContent = (ok ? '✓ ' : '✗ ') + text;
    box.className = 'wb-crewall-status ' + (ok ? 'wb-crewall-status--ok' : 'wb-crewall-status--bad');
  }

  function rowOf(el) { return el.closest('[data-crew-row]'); }
  function nameOf(row) { return row.getAttribute('data-crew-row'); }
  function prefixOf(row) { return row.getAttribute('data-prefix'); }

  // The confirm box moves under the row that asked; one at a time.
  var confirmBox = document.querySelector('[data-crew-confirm]');
  var pending = null;
  function ask(row, anchor, question, note, yes, run) {
    closeAll();
    confirmBox.querySelector('[data-crew-confirm-q]').textContent = question;
    confirmBox.querySelector('[data-crew-confirm-note]').textContent = note;
    var btn = confirmBox.querySelector('[data-crew-confirm-yes]');
    btn.textContent = yes;
    btn.disabled = false;
    pending = run;
    (anchor || row).appendChild(confirmBox);
    confirmBox.hidden = false;
    btn.focus();
  }
  confirmBox.querySelector('[data-crew-confirm-yes]').addEventListener('click', function () {
    var run = pending;
    pending = null;
    confirmBox.hidden = true;
    if (run) run();
  });
  confirmBox.querySelector('[data-crew-confirm-no]').addEventListener('click', function () {
    pending = null;
    confirmBox.hidden = true;
  });

  function settle(row, j) {
    say(row, j.ok, (j.message || (j.ok ? 'Done' : 'Refused')) + (j.at ? ' at ' + j.at : '') + (j.ok ? ' · logged' : ''));
    if (j.ok) setTimeout(function () { location.reload(); }, 2500);
  }

  document.querySelectorAll('[data-crew-stop]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var row = rowOf(btn), name = nameOf(row);
      ask(row, null, 'Stop ' + name + '?',
        'Ends this one tmux session. Its worktree, branch and issue stay; issue-cli dispatch starts it again.',
        'Stop the session', function () {
          say(row, true, 'Stopping…');
          post(prefixOf(row) + '/crew/stop', { session: name }).then(function (j) { settle(row, j); })
            .catch(function (e) { say(row, false, 'Not stopped: ' + e); });
        });
    });
  });

  document.querySelectorAll('[data-crew-start]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var row = rowOf(btn), what = btn.getAttribute('data-crew-what');
      ask(row, null, what + '?', 'Starts it in its own tmux session, exactly like issue-cli lead spawn.', what, function () {
        say(row, true, 'Starting…');
        post(btn.getAttribute('data-crew-start'), {}).then(function (j) { settle(row, j); })
          .catch(function (e) { say(row, false, 'Not started: ' + e); });
      });
    });
  });

  document.querySelectorAll('[data-crew-switch]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var proj = btn.closest('.wb-crewall-proj'), what = btn.getAttribute('data-crew-what');
      var anchor = btn.closest('.wb-crewall-ctl');
      var note = /queue/.test(what) ?
        (/^Pause/.test(what) ? 'queue next starts nothing, not even an item with its own quote, until you resume it here.' : 'queue next starts items again within its limits.') :
        (/^Pause/.test(what) ? 'Auto-start stops starting workers until you resume it. Running workers keep going.' : 'Auto-start starts workers again within its limits.');
      ask(anchor, anchor, what + '?', note, what, function () {
        post(btn.getAttribute('data-crew-switch'), {}).then(function (j) {
          // autostart/* answers the state, crew routes {ok, message}.
          if (j.message === undefined && j.ok) j.message = what + ' done';
          var line = proj.querySelector('.wb-crewall-ctl-status') || document.createElement('div');
          line.className = 'wb-crewall-ctl-status';
          line.setAttribute('role', 'status');
          line.textContent = (j.ok ? '✓ ' : '✗ ') + (j.message || j.error || '');
          anchor.after(line);
          if (j.ok) setTimeout(function () { location.reload(); }, 2000);
        });
      });
    });
  });

  // Sheets.
  var sheets = {};
  document.querySelectorAll('[data-crew-sheet]').forEach(function (s) { sheets[s.getAttribute('data-crew-sheet')] = s; });
  var current = null;
  function closeAll() {
    Object.keys(sheets).forEach(function (k) { sheets[k].hidden = true; });
    if (confirmBox) confirmBox.hidden = true;
    pending = null;
  }
  function open(kind, row) {
    closeAll();
    current = row;
    var s = sheets[kind];
    s.querySelectorAll('[data-crew-sheet-name]').forEach(function (el) { el.textContent = nameOf(row); });
    s.hidden = false;
    return s;
  }
  document.querySelectorAll('[data-crew-close]').forEach(function (b) { b.addEventListener('click', closeAll); });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') closeAll(); });

  var nudge = sheets.nudge, text = nudge.querySelector('[data-crew-text]'), count = nudge.querySelector('[data-crew-count]');
  var nudgeMsg = nudge.querySelector('[data-crew-sheet-msg]');
  function recount() { count.textContent = String(text.value.length); }
  text.addEventListener('input', recount);
  nudge.querySelectorAll('[data-crew-preset]').forEach(function (p) {
    p.addEventListener('click', function () { text.value = p.textContent; recount(); text.focus(); });
  });
  document.querySelectorAll('[data-crew-nudge]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      open('nudge', rowOf(btn));
      text.value = '';
      nudgeMsg.textContent = '';
      recount();
      text.focus();
    });
  });
  var send = nudge.querySelector('[data-crew-send]');
  send.addEventListener('click', function () {
    var row = current, msg = text.value.trim();
    if (!row || !msg) { nudgeMsg.textContent = 'Write a message first.'; return; }
    send.disabled = true;
    send.textContent = 'Sending…';
    post(prefixOf(row) + '/crew/nudge', { session: nameOf(row), text: msg }).then(function (j) {
      send.disabled = false;
      send.textContent = 'Send to the agent';
      if (j.ok) { closeAll(); say(row, true, j.message + (j.at ? ' at ' + j.at : '') + ' · logged'); }
      else nudgeMsg.textContent = '✗ ' + (j.message || 'Not delivered');
    }).catch(function (e) {
      send.disabled = false;
      send.textContent = 'Send to the agent';
      nudgeMsg.textContent = '✗ Not sent: ' + e;
    });
  });

  var tail = sheets.tail, pane = tail.querySelector('[data-crew-pane]'), tailAt = tail.querySelector('[data-crew-tail-at]');
  function loadTail() {
    var row = current;
    if (!row) return;
    pane.textContent = 'Reading the pane…';
    fetch(prefixOf(row) + '/crew/tail?session=' + encodeURIComponent(nameOf(row)), { headers: { 'Accept': 'application/json' }, cache: 'no-store' })
      .then(function (r) { return r.text().then(function (t) { return { ok: r.ok, t: t }; }); })
      .then(function (res) {
        if (!res.ok) { var m = res.t; try { m = JSON.parse(res.t).error || m; } catch (e) {} pane.textContent = m; return; }
        var j = JSON.parse(res.t);
        pane.textContent = (j.lines || []).join('\n');
        tailAt.textContent = 'Last ' + (j.lines || []).length + ' lines of its pane at ' + j.at + ' · read only';
        pane.scrollTop = pane.scrollHeight;
      })
      .catch(function (e) { pane.textContent = 'Could not read the pane: ' + e; });
  }
  document.querySelectorAll('[data-crew-tail]').forEach(function (btn) {
    btn.addEventListener('click', function () { open('tail', rowOf(btn)); loadTail(); });
  });
  tail.querySelector('[data-crew-refresh]').addEventListener('click', loadTail);
})();
