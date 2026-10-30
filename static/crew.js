// Workshop Board, the Crew page (L1): Copy buttons and "Open in a terminal"
// (POST agents/open), which opens the agent's tmux session or brings the one
// already open to the front; it never opens a second window.
(function () {
  document.querySelectorAll('[data-copy]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var text = btn.getAttribute('data-copy');
      var done = function () { btn.textContent = 'Copied'; setTimeout(function () { btn.textContent = 'Copy'; }, 1500); };
      if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, function () { btn.textContent = 'Select and copy'; });
    });
  });

  document.querySelectorAll('[data-open-session]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var name = btn.getAttribute('data-session');
      var msg = btn.closest('[data-open-box]').querySelector('.wb-site-msg');
      msg.textContent = 'Opening ' + name + '…';
      fetch(btn.getAttribute('data-open-session'), {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session: name })
      }).then(function (r) { return r.json(); }).then(function (d) {
        var steps = d.steps || [];
        var headless = steps.some(function (s) { return s.name.indexOf('Terminal: headless') === 0; });
        var failed = steps.filter(function (s) { return s.status === 'error'; })[0];
        var hint = 'Inside tmux: ' + d.switch_cmd + ' · From a shell: ' + d.attach_cmd;
        if (d.status === 'no-session') {
          msg.textContent = name + ' is not running.';
        } else if (d.status === 'selected') {
          msg.textContent = 'Selected ' + name + ' in tmux session ' + d.session.split(':')[0] + ' (terminal: none). ' + hint;
        } else if (d.status === 'focused') {
          msg.textContent = 'Switched to the open terminal for ' + name + '.';
        } else if (d.status === 'already-open') {
          msg.textContent = name + ' is already open in a terminal, but its window could not be brought to the front (it needs i3 and a window titled ' + name + ').';
        } else if (failed) {
          msg.textContent = 'Could not open ' + name + ': ' + failed.name + (failed.detail ? ' (' + failed.detail + ')' : '');
        } else if (headless) {
          msg.textContent = 'No terminal configured (terminal: none). ' + hint;
        } else {
          msg.textContent = 'Opened ' + name + ' in a terminal.';
        }
      }).catch(function (e) { msg.textContent = 'Could not open: ' + e.message; });
    });
  });
})();
