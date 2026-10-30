// The CEO chat (/p/<project>/lead): polls the live part, sends messages and
// keys to the lead, and keeps what the owner is typing and the scroll
// position across updates. Images pasted, dropped or attached in the box go
// to …/lead/image first; the send carries their paths. Spawning the CEO is
// workshop.js's data-spawn-lead.
(function () {
  var main = document.querySelector('[data-live-url]');
  if (!main) return;
  var form = document.querySelector('form[data-ceo-send]');
  var text = form.querySelector('textarea');
  var status = form.querySelector('.wb-site-msg');
  var scroll = document.querySelector('[data-scroll]');
  var keyURL = form.getAttribute('data-key-url');
  var imageURL = form.getAttribute('data-image-url');
  var imageBox = form.querySelector('[data-images]');
  var imageInput = form.querySelector('[data-image-input]');
  var imageChip = form.querySelector('template[data-image-chip]');
  var imageTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
  var imageMax = 10 << 20, imagesPerMessage = 5;
  // images: {path, el} per chip, path null while it uploads.
  var images = [];
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

  // A question card the owner has started answering stays as it is while
  // the same question is still on the CEO's screen.
  function keepAskCard(slot, t) {
    var mine = slot.querySelector('form[data-ask-question][data-dirty]');
    if (!mine) return false;
    var next = t.content.querySelector('form[data-ask-question]');
    return !!next && next.getAttribute('data-ask-question') === mine.getAttribute('data-ask-question');
  }

  function pickOption(btn) {
    var form = btn.closest('form');
    var on = btn.getAttribute('aria-pressed') !== 'true';
    if (!form.getAttribute('data-multi')) {
      form.querySelectorAll('[data-ask-pick]').forEach(function (b) {
        b.setAttribute('aria-pressed', 'false');
        b.classList.remove('wb-choice-opt--on');
      });
      if (on) form.querySelector('[data-ask-other]').value = '';
    }
    btn.setAttribute('aria-pressed', on ? 'true' : 'false');
    btn.classList.toggle('wb-choice-opt--on', on);
    form.setAttribute('data-dirty', '1');
  }

  function submitAsk(form) {
    var msg = form.querySelector('[data-ask-msg]');
    var picks = Array.prototype.filter.call(form.querySelectorAll('[data-ask-pick]'), function (b) {
      return b.getAttribute('aria-pressed') === 'true';
    }).map(function (b) { return parseInt(b.getAttribute('data-ask-pick'), 10); });
    var other = form.querySelector('[data-ask-other]').value.trim();
    if (!picks.length && !other) { msg.textContent = 'Pick an answer first.'; return; }
    msg.textContent = 'Answering…';
    post(form.getAttribute('data-ask-url'), { question: form.getAttribute('data-ask-question'), picks: picks, other: other }).then(function (res) {
      if (!res.ok) { msg.textContent = 'Not answered: ' + (res.body.error || 'error'); return; }
      form.removeAttribute('data-dirty');
      msg.textContent = 'Answered in ' + (res.body.session || 'the CEO') + '.';
      setTimeout(poll, 400);
    }).catch(function (e) { msg.textContent = 'Not answered: ' + e; });
  }

  document.addEventListener('submit', function (e) {
    var form = e.target.closest('form[data-ask-question]');
    if (!form) return;
    e.preventDefault();
    submitAsk(form);
  });
  document.addEventListener('input', function (e) {
    var other = e.target.closest('[data-ask-other]');
    if (!other) return;
    var form = other.closest('form');
    // One answer for a single choice: typing Other clears a pick.
    if (!form.getAttribute('data-multi') && other.value) {
      form.querySelectorAll('[data-ask-pick]').forEach(function (b) {
        b.setAttribute('aria-pressed', 'false');
        b.classList.remove('wb-choice-opt--on');
      });
    }
    form.setAttribute('data-dirty', '1');
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

  function showImages() { imageBox.hidden = images.length === 0; }

  function dropImage(img) {
    images = images.filter(function (i) { return i !== img; });
    img.el.remove();
    showImages();
  }

  // addImages uploads each file and shows it as a chip; a refused file says
  // why and leaves the rest of the box alone.
  function addImages(files) {
    Array.prototype.forEach.call(files, function (file) {
      if (imageTypes.indexOf(file.type) < 0) { say('Not attached: ' + (file.name || 'that file') + ' is not a PNG, JPEG, GIF or WebP image.'); return; }
      if (file.size > imageMax) { say('Not attached: ' + (file.name || 'that image') + ' is over 10 MB.'); return; }
      if (images.length >= imagesPerMessage) { say('Not attached: at most ' + imagesPerMessage + ' images in one message.'); return; }
      var img = { path: null, el: imageChip.content.firstElementChild.cloneNode(true) };
      var pic = img.el.querySelector('img');
      pic.alt = file.name || 'image';
      pic.src = URL.createObjectURL(file);
      img.el.querySelector('button').addEventListener('click', function () { dropImage(img); });
      imageBox.appendChild(img.el);
      images.push(img);
      showImages();
      say('Uploading ' + (file.name || 'image') + '…');
      var fd = new FormData();
      fd.append('file', file, file.name || 'pasted.png');
      fetch(imageURL, { method: 'POST', body: fd })
        .then(function (r) { return r.json().catch(function () { return {}; }).then(function (j) { return { ok: r.ok, body: j }; }); })
        .then(function (res) {
          if (images.indexOf(img) < 0) return;
          if (!res.ok || !res.body.path) { dropImage(img); say('Not attached: ' + (res.body.error || 'upload failed') + '.'); return; }
          img.path = res.body.path;
          img.el.title = img.path;
          img.el.classList.remove('wb-image-chip--busy');
          say(images.some(function (i) { return !i.path; }) ? 'Uploading…' : 'Attached. It goes with your next message.');
        })
        .catch(function (e) { dropImage(img); say('Not attached: ' + e); });
    });
  }

  function clearImages() {
    images.forEach(function (img) { img.el.remove(); });
    images = [];
    showImages();
  }

  function send(now) {
    var msg = text.value.replace(/\s+$/, '');
    if (images.some(function (i) { return !i.path; })) { say('Wait: an image is still uploading.'); return; }
    var paths = images.map(function (i) { return i.path; });
    if (!msg.trim() && !paths.length) return;
    say('Sending…');
    post(form.getAttribute('data-ceo-send'), { text: msg, images: paths }).then(function (res) {
      if (!res.ok || res.body.status === 'error') {
        var step = (res.body.steps || [])[0];
        say('Not sent' + (res.body.error ? ': ' + res.body.error : step ? ': ' + step.name + (step.detail ? ' (' + step.detail + ')' : '') : '.'));
        return;
      }
      text.value = '';
      clearImages();
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

  // Paste: image files on the clipboard attach; text pastes as text.
  text.addEventListener('paste', function (e) {
    var files = Array.prototype.filter.call((e.clipboardData && e.clipboardData.files) || [], function (f) { return f.type.indexOf('image/') === 0; });
    if (!files.length) return;
    if (!e.clipboardData.getData('text/plain')) e.preventDefault();
    addImages(files);
  });
  form.addEventListener('dragover', function (e) {
    if (text.disabled || !e.dataTransfer || Array.prototype.indexOf.call(e.dataTransfer.types, 'Files') < 0) return;
    e.preventDefault();
    form.classList.add('wb-composer--drop');
  });
  form.addEventListener('dragleave', function (e) { if (!form.contains(e.relatedTarget)) form.classList.remove('wb-composer--drop'); });
  form.addEventListener('drop', function (e) {
    form.classList.remove('wb-composer--drop');
    if (text.disabled || !e.dataTransfer || !e.dataTransfer.files.length) return;
    e.preventDefault();
    addImages(e.dataTransfer.files);
  });
  imageInput.addEventListener('change', function () { addImages(imageInput.files); imageInput.value = ''; });

  document.addEventListener('click', function (e) {
    var btn = e.target.closest('button');
    if (!btn || btn.disabled) return;
    if (btn.hasAttribute('data-send-now')) { send(true); return; }
    if (btn.hasAttribute('data-ask-pick')) { pickOption(btn); return; }
    if (btn.hasAttribute('data-attach')) { imageInput.click(); return; }
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
