// YOLO mode on the Workshop Board: the switch opens the turn-on dialog (L1),
// the hazard band's Extend / Turn off now, and the station's review after the
// fact (Agree / Back to <status>). The endpoints are yolo_view.go's.
(function () {
  const hook = document.querySelector('[data-yolo-prefix]');
  if (!hook || hook.dataset.wired) return;
  hook.dataset.wired = '1';
  const prefix = hook.dataset.yoloPrefix;

  function say(from, text) {
    const box = from && from.closest('form, .wb-hazard');
    const msg = box && box.querySelector('.wb-site-msg');
    if (msg) msg.textContent = text;
    else if (text) alert(text);
  }

  async function post(url, body, from) {
    say(from, '');
    const res = await fetch(url, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body || {}),
    }).catch(() => null);
    if (!res || !res.ok) {
      const data = res ? await res.json().catch(() => ({})) : {};
      say(from, data.error || (res ? res.statusText : 'The viewer did not answer.'));
      return false;
    }
    location.reload();
    return true;
  }

  const dialog = document.querySelector('.wb-yolo-dialog');
  const form = dialog && dialog.querySelector('[data-yolo-form]');
  if (form) {
    form.addEventListener('change', (e) => {
      if (e.target.name !== 'until') return;
      form.querySelector('[data-yolo-label]').textContent = e.target.dataset.label;
    });
    form.addEventListener('submit', (e) => {
      e.preventDefault();
      const words = form.words.value.trim();
      if (!words) {
        say(form.words, 'Say it in your words: turning YOLO on is your decision, like any gate.');
        form.words.focus();
        return;
      }
      post(prefix + '/yolo/on', {
        until: form.until.value,
        words: words,
        ci_valve: form.ci_valve.checked,
        releases: !form.no_releases.checked,
      }, form.words);
    });
  }

  document.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-yolo]');
    if (!btn) return;
    switch (btn.dataset.yolo) {
      case 'dialog':
        if (dialog) dialog.showModal();
        break;
      case 'cancel':
        if (dialog) dialog.close();
        break;
      case 'band': {
        const band = document.querySelector('[data-testid="yolo.band"]');
        if (band) band.scrollIntoView({block: 'nearest'});
        const off = band && band.querySelector('[data-yolo="off"]');
        if (off) off.focus();
        break;
      }
      case 'extend':
        post(prefix + '/yolo/extend', {until: btn.dataset.until}, btn);
        break;
      case 'off':
        post(prefix + '/yolo/off', {}, btn);
        break;
    }
  });

  // The station: each pass is a form; the pressed button is the verdict.
  const review = document.querySelector('[data-review]');
  if (review) {
    review.addEventListener('submit', (e) => {
      const pass = e.target.closest('.wb-yolo-pass');
      if (!pass) return;
      e.preventDefault();
      const verdict = e.submitter ? e.submitter.value : 'agree';
      const words = pass.words.value.trim();
      if (verdict === 'back' && !words) {
        say(pass.words, 'Say why it goes back: your words reach the agent.');
        pass.words.focus();
        return;
      }
      post(review.dataset.review, {status: pass.dataset.status, verdict: verdict, words: words}, pass.words);
    });
  }
})();
