// The CEO chat (/p/<project>/lead): polls the live part, sends messages and
// keys to the lead, and keeps what the owner is typing and the scroll
// position across updates. Images pasted, dropped or attached in the box go
// to …/lead/image first, any other file (or an image too big or refused
// there) to …/lead/file; the send carries their paths. Spawning the CEO is
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
  var fileURL = form.getAttribute('data-file-url');
  var fileChip = form.querySelector('template[data-file-chip]');
  var imageTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
  var imageMax = 10 << 20, imagesPerMessage = 5;
  var fileMax = 50 << 20, filesPerMessage = 5;
  // images and files: {path, el} per chip, path null while it uploads.
  var images = [];
  var files = [];
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

  function showImages() { imageBox.hidden = images.length + files.length === 0; }

  function dropImage(img) {
    images = images.filter(function (i) { return i !== img; });
    files = files.filter(function (f) { return f !== img; });
    img.el.remove();
    showImages();
  }

  function uploading() { return images.concat(files).some(function (i) { return !i.path; }); }
  function attachedSay() { say(uploading() ? 'Uploading…' : 'Attached. It goes with your next message.'); }

  function upload(url, file) {
    var fd = new FormData();
    fd.append('file', file, file.name || 'pasted');
    return fetch(url, { method: 'POST', headers: { 'Accept': 'application/json' }, body: fd })
      .then(function (r) { return r.json().catch(function () { return {}; }).then(function (j) { return { ok: r.ok, code: r.status, body: j }; }); });
  }

  function sizeText(n) {
    if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB';
    if (n >= 1 << 10) return Math.floor(n / 1024) + ' KB';
    return n + ' B';
  }

  function badge(name) {
    var i = name.lastIndexOf('.');
    return i > 0 && i < name.length - 1 ? name.slice(i + 1).toUpperCase().slice(0, 5) : 'FILE';
  }

  // addAttachments sends each file the image way (a PNG, JPEG, GIF or WebP
  // of 10 MB or less) or the file way; a refused file says why and leaves
  // the rest of the box alone.
  function addAttachments(list) {
    Array.prototype.forEach.call(list, function (file) {
      if (imageTypes.indexOf(file.type) >= 0 && file.size <= imageMax) addImage(file);
      else addFile(file);
    });
  }

  // addFile uploads a file to …/lead/file and shows it as a file chip.
  function addFile(file) {
    var label = file.name || 'that file';
    if (!file.size) { say('Not attached: ' + label + ' is empty.'); return; }
    if (file.size > fileMax) { say('Not attached: ' + label + ' is over 50 MB.'); return; }
    if (files.length >= filesPerMessage) { say('Not attached: at most ' + filesPerMessage + ' files in one message.'); return; }
    var f = { path: null, el: fileChip.content.firstElementChild.cloneNode(true) };
    f.el.querySelector('.wb-file-badge').textContent = badge(file.name || '');
    f.el.querySelector('.wb-file-name').textContent = file.name || 'file';
    f.el.title = file.name || 'file';
    f.el.querySelector('button').addEventListener('click', function () { dropImage(f); });
    imageBox.appendChild(f.el);
    files.push(f);
    showImages();
    say('Uploading ' + (file.name || 'file') + '…');
    upload(fileURL, file).then(function (res) {
      if (files.indexOf(f) < 0) return;
      if (!res.ok || !res.body.path) { dropImage(f); say('Not attached: ' + label + ': ' + (res.body.error || 'upload failed') + '.'); return; }
      f.path = res.body.path;
      f.el.title = res.body.name + ' · ' + f.path;
      f.el.querySelector('.wb-file-name').textContent = res.body.name;
      f.el.querySelector('.wb-file-badge').textContent = res.body.type;
      f.el.querySelector('.wb-file-size').textContent = sizeText(res.body.size);
      f.el.setAttribute('data-state', 'ready');
      attachedSay();
    }).catch(function (e) { dropImage(f); say('Not attached: ' + e); });
  }

  // addImage uploads an image to …/lead/image and shows it as a thumbnail;
  // one the server doesn't take as an image (415) goes the file way.
  function addImage(file) {
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
    upload(imageURL, file).then(function (res) {
      if (images.indexOf(img) < 0) return;
      if (res.code === 415) { dropImage(img); addFile(file); return; }
      if (!res.ok || !res.body.path) { dropImage(img); say('Not attached: ' + (res.body.error || 'upload failed') + '.'); return; }
      img.path = res.body.path;
      img.el.title = img.path;
      img.el.classList.remove('wb-image-chip--busy');
      attachedSay();
    }).catch(function (e) { dropImage(img); say('Not attached: ' + e); });
  }

  function clearImages() {
    images.concat(files).forEach(function (img) { img.el.remove(); });
    images = [];
    files = [];
    showImages();
  }

  function send(now) {
    var msg = text.value.replace(/\s+$/, '');
    if (uploading()) { say('Wait: a file is still uploading.'); return; }
    var paths = images.map(function (i) { return i.path; });
    var filePaths = files.map(function (f) { return f.path; });
    if (!msg.trim() && !paths.length && !filePaths.length) return;
    say('Sending…');
    post(form.getAttribute('data-ceo-send'), { text: msg, images: paths, files: filePaths }).then(function (res) {
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

  // Paste: files on the clipboard attach; text pastes as text.
  text.addEventListener('paste', function (e) {
    var pasted = (e.clipboardData && e.clipboardData.files) || [];
    if (!pasted.length) return;
    if (!e.clipboardData.getData('text/plain')) e.preventDefault();
    addAttachments(pasted);
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
    addAttachments(e.dataTransfer.files);
  });
  imageInput.addEventListener('change', function () { addAttachments(imageInput.files); imageInput.value = ''; });

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
