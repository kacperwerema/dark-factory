// Workshop Board, the hall's Health page: Copy buttons and "File a refactor
// issue" (POST issues/create). "Give it to an agent" is a form[data-send]
// that workshop.js already posts to the CEO.
(function () {
  document.querySelectorAll('[data-copy]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var text = btn.getAttribute('data-copy');
      var done = function () { btn.textContent = 'Copied'; setTimeout(function () { btn.textContent = 'Copy'; }, 1500); };
      if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, function () { btn.textContent = 'Select and copy'; });
    });
  });

  document.querySelectorAll('[data-create-issue]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var msg = btn.parentNode.querySelector('.wb-site-msg');
      var url = btn.getAttribute('data-create-issue');
      btn.disabled = true;
      msg.textContent = 'Filing…';
      fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: btn.getAttribute('data-title'), body: btn.getAttribute('data-body') }) })
        .then(function (r) { return r.ok ? r.json() : r.text().then(function (t) { throw new Error(t); }); })
        .then(function (res) {
          msg.textContent = '';
          var a = document.createElement('a');
          a.href = url.replace(/\/issues\/create$/, '/issue/' + res.slug);
          a.textContent = 'Filed as ' + res.slug + ' (status idea)';
          msg.appendChild(a);
        })
        .catch(function (e) { msg.textContent = 'Not filed: ' + e.message; btn.disabled = false; });
    });
  });
})();
