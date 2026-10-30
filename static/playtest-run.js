// ▶ Run playtest (handlers_playtest.go), on the station, the Issue tab's
// Needs-you band and the Evidence tab. A [data-playtest] block holds the
// button (data-run-playtest = the POST URL) and a [data-playtest-result]
// line. The click opens tmux window playtest-<slug>; the line says it is
// running until the window closes, then lists each judged step and offers
// Tick passed boxes, which runs the report in the same window and reloads
// the page when it ends.
(function () {
  var pollMs = 3000;
  var giveUpMs = 30 * 60 * 1000;

  // Keep auto-refresh (detail.html) from wiping the result while it matters.
  function hold() {
    if (window.suppressAutoRefresh) window.suppressAutoRefresh(giveUpMs);
  }

  function say(box, text) {
    box.hidden = false;
    box.textContent = text;
  }

  function failed(d) {
    var bad = (d.steps || []).filter(function (s) { return s.status === 'error'; })[0];
    return bad ? bad.name + (bad.detail ? ': ' + bad.detail : '') : 'unknown error';
  }

  function started(d) {
    if ((d.steps || []).some(function (s) { return s.name.indexOf('Terminal: headless') === 0; })) {
      return 'No terminal configured. Inside tmux: ' + d.switch_cmd;
    }
    return '';
  }

  // watch polls the window until it closes, then calls done with the state.
  function watch(url, since, box, done) {
    var t0 = Date.now();
    function tick() {
      if (Date.now() - t0 > giveUpMs) {
        say(box, 'Stopped watching after 30 minutes. Reload the page to see the result.');
        return;
      }
      fetch(url + '?since=' + encodeURIComponent(since || ''))
        .then(function (r) { return r.json(); })
        .then(function (d) {
          if (d.running) {
            hold();
            setTimeout(tick, document.hidden ? pollMs * 3 : pollMs);
            return;
          }
          done(d);
        })
        .catch(function (e) { say(box, 'Lost track of the playtest: ' + e); });
    }
    tick();
  }

  function showResult(block, url, box, d) {
    hold();
    box.hidden = false;
    box.textContent = '';
    if (!d.runs || !d.runs.length) {
      say(box, d.session + ' closed with no judged run since you started it.');
      return;
    }
    var head = document.createElement('p');
    var parts = [d.passed + ' passed', d.failed + ' failed'];
    if (d.open) parts.push(d.open + ' not judged');
    head.textContent = 'Playtest done: ' + parts.join(', ') + '.';
    box.appendChild(head);
    var list = document.createElement('ul');
    list.className = 'playtest-steps';
    d.runs.forEach(function (run) {
      run.steps.forEach(function (st) {
        var li = document.createElement('li');
        li.className = 'playtest-step playtest-step--' + (st.verdict || 'open');
        li.textContent = st.id + ' · ' + (st.verdict || 'not judged') + (st.title ? ' · ' + st.title : '') + (d.runs.length > 1 ? ' (' + run.session + ')' : '');
        list.appendChild(li);
      });
    });
    box.appendChild(list);
    var tick = document.createElement('button');
    tick.type = 'button';
    tick.className = block.getAttribute('data-playtest-btn') || 'btn';
    tick.textContent = 'Tick passed boxes';
    var note = document.createElement('p');
    note.className = 'playtest-note';
    note.textContent = 'Ticks the boxes of the steps that passed and comments the failures; nothing is ticked until you click.';
    tick.addEventListener('click', function () { report(url, box, tick, note); });
    box.appendChild(tick);
    box.appendChild(note);
  }

  function report(url, box, btn, note) {
    btn.disabled = true;
    note.textContent = 'Ticking the boxes that passed…';
    fetch(url + '?report=1', { method: 'POST' })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
      .then(function (res) {
        var d = res.d;
        if (!res.ok || d.status === 'error') {
          btn.disabled = false;
          note.textContent = 'Tick passed boxes failed: ' + failed(d);
          return;
        }
        if (d.status === 'already-running') {
          btn.disabled = false;
          note.textContent = d.session + ' is still open: close it, then click again.';
          return;
        }
        note.textContent = 'Running the report in ' + d.session + '. ' + started(d);
        watch(url, d.since, box, function () { location.reload(); });
      })
      .catch(function (e) { btn.disabled = false; note.textContent = 'Tick passed boxes failed: ' + e; });
  }

  document.querySelectorAll('[data-playtest]').forEach(function (block) {
    var btn = block.querySelector('[data-run-playtest]');
    var box = block.querySelector('[data-playtest-result]');
    if (!btn || !box) return;
    var url = btn.getAttribute('data-run-playtest');
    btn.addEventListener('click', function () {
      btn.disabled = true;
      say(box, 'Starting the playtest…');
      fetch(url, { method: 'POST' })
        .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
        .then(function (res) {
          var d = res.d;
          if (d.status === 'no-playtest') { btn.disabled = false; say(box, 'No playtest session names this issue.'); return; }
          if (d.status === 'off') { btn.disabled = false; say(box, 'The owner playtest is off for this project.'); return; }
          if (!res.ok || d.status === 'error') { btn.disabled = false; say(box, 'Run playtest failed: ' + failed(d)); return; }
          var verb = d.status === 'already-running' ? 'Brought back ' : 'Running in ';
          say(box, verb + d.session + '… judge the steps in the overlay, then close it. ' + started(d));
          watch(url, d.since, box, function (state) {
            btn.disabled = false;
            showResult(block, url, box, state);
          });
        })
        .catch(function (e) { btn.disabled = false; say(box, 'Run playtest failed: ' + e); });
    });
  });
})();
