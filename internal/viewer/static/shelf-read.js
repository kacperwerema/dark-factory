// The output reader (shelf_read.go): lights the passages the open threads
// are on, turns a selection or a heading's 💬 into a comment, posts
// comments, replies and resolves, and keeps the Day / Night sheet.
(function () {
  'use strict';
  var page = document.querySelector('[data-testid="odoc.page"]');
  if (!page) return;
  var sheet = page.querySelector('.wb-odoc-sheet');
  var form = page.querySelector('[data-odoc-new]');
  var pop = page.querySelector('[data-odoc-pop]');
  var output = page.getAttribute('data-output');
  var anchor = null; // the anchor the comment box is on, null = whole document
  var CONTEXT = 30;

  function fold(s) { return s.replace(/\s+/g, ' '); }

  // The sheet's text as one folded string, with each character's text node
  // and offset, skipping the 💬 buttons.
  function textMap() {
    var chars = [], nodes = [], offs = [], space = false;
    if (!sheet) return { text: '', nodes: nodes, offs: offs };
    var walk = document.createTreeWalker(sheet, NodeFilter.SHOW_TEXT, {
      acceptNode: function (n) { return n.parentNode.closest('.wb-odoc-hc') ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT; }
    });
    for (var n = walk.nextNode(); n; n = walk.nextNode()) {
      var v = n.nodeValue;
      for (var i = 0; i < v.length; i++) {
        if (/\s/.test(v[i])) {
          if (space || chars.length === 0) continue;
          space = true;
          chars.push(' ');
        } else {
          space = false;
          chars.push(v[i]);
        }
        nodes.push(n);
        offs.push(i);
      }
    }
    return { text: chars.join(''), nodes: nodes, offs: offs };
  }

  // Wrap folded characters [from, to) in <mark>s, one per text node.
  function wrap(map, from, to, id) {
    var runs = [];
    for (var i = from; i < to; i++) {
      var last = runs[runs.length - 1];
      if (last && last.node === map.nodes[i]) last.end = map.offs[i] + 1;
      else runs.push({ node: map.nodes[i], start: map.offs[i], end: map.offs[i] + 1 });
    }
    runs.reverse().forEach(function (r) {
      var node = r.node;
      if (r.end < node.nodeValue.length) node.splitText(r.end);
      var mid = r.start > 0 ? node.splitText(r.start) : node;
      if (!mid.nodeValue.trim()) return;
      var mark = document.createElement('mark');
      mark.className = 'wb-odoc-mark';
      mark.setAttribute('data-thread', id);
      mid.parentNode.insertBefore(mark, mid);
      mark.appendChild(mid);
    });
  }

  function lightPassages() {
    var el = document.getElementById('odoc-marks');
    if (!sheet || !el) return;
    var marks = [];
    try { marks = JSON.parse(el.textContent) || []; } catch (e) { return; }
    marks.forEach(function (m) {
      if (m.place !== 'here' || !m.quote) return;
      var map = textMap();
      var at = -1;
      if (m.prefix || m.suffix) {
        at = map.text.indexOf(m.prefix + m.quote + m.suffix);
        if (at >= 0) at += m.prefix.length;
      }
      if (at < 0) at = map.text.indexOf(m.quote);
      if (at >= 0) wrap(map, at, at + m.quote.length, m.id);
    });
  }

  function select(id, scroll) {
    page.querySelectorAll('.wb-odoc-mark--on').forEach(function (m) { m.classList.remove('wb-odoc-mark--on'); });
    page.querySelectorAll('.wb-odoc-thread--on').forEach(function (t) { t.classList.remove('wb-odoc-thread--on'); });
    var marks = page.querySelectorAll('.wb-odoc-mark[data-thread="' + id + '"]');
    marks.forEach(function (m) { m.classList.add('wb-odoc-mark--on'); });
    var th = document.getElementById('thread-' + id);
    if (th) th.classList.add('wb-odoc-thread--on');
    if (scroll === 'mark' && marks[0]) marks[0].scrollIntoView({ block: 'center', behavior: 'smooth' });
    if (scroll === 'thread' && th) th.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }

  function headingBefore(node) {
    var hs = sheet ? sheet.querySelectorAll('h2[id], h3[id], h4[id]') : [];
    var found = null;
    hs.forEach(function (h) {
      if (h === node || h.contains(node) || (h.compareDocumentPosition(node) & Node.DOCUMENT_POSITION_FOLLOWING)) found = h;
    });
    return found;
  }

  function headingText(h) {
    var c = h.cloneNode(true);
    c.querySelectorAll('.wb-odoc-hc').forEach(function (b) { b.remove(); });
    return fold(c.textContent).trim();
  }

  function setAnchor(a) {
    anchor = a;
    var where = form.querySelector('[data-odoc-new-where]');
    var quote = form.querySelector('[data-odoc-new-quote]');
    var clear = form.querySelector('[data-odoc-new-clear]');
    where.textContent = !a ? 'On the whole document' : a.quote ? 'On a passage' + (a.heading_text ? ' under ' + a.heading_text : '') : 'On the heading ' + a.heading_text;
    quote.hidden = !(a && a.quote);
    quote.textContent = a && a.quote ? a.quote : '';
    clear.hidden = !a;
    form.querySelector('textarea').focus();
  }

  // A selection inside the sheet: its folded text, context and heading.
  function selectionAnchor() {
    var sel = window.getSelection();
    if (!sheet || !sel || sel.isCollapsed || !sel.rangeCount) return null;
    var range = sel.getRangeAt(0);
    if (!sheet.contains(range.commonAncestorContainer)) return null;
    var quote = fold(sel.toString()).trim();
    if (!quote) return null;
    var map = textMap();
    var startNode = range.startContainer, startOff = range.startOffset;
    if (startNode.nodeType !== Node.TEXT_NODE) {
      var probe = document.createTreeWalker(startNode.childNodes[startOff] || startNode, NodeFilter.SHOW_TEXT).nextNode();
      if (probe) { startNode = probe; startOff = 0; }
    }
    var at = -1;
    for (var i = 0; i < map.nodes.length; i++) {
      if (map.nodes[i] === startNode && map.offs[i] >= startOff) { at = map.text.indexOf(quote, i); break; }
    }
    if (at < 0) at = map.text.indexOf(quote);
    var h = headingBefore(range.startContainer);
    return {
      quote: quote.slice(0, 500),
      prefix: at > 0 ? map.text.slice(Math.max(0, at - CONTEXT), at) : '',
      suffix: at >= 0 ? map.text.slice(at + quote.length, at + quote.length + CONTEXT) : '',
      heading: h ? h.id : '',
      heading_text: h ? headingText(h) : ''
    };
  }

  function post(url, body, msgEl, done) {
    if (msgEl) msgEl.textContent = 'Saving…';
    fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok) { if (msgEl) msgEl.textContent = res.j.error || 'Could not save.'; return; }
        done(res.j);
      })
      .catch(function (e) { if (msgEl) msgEl.textContent = 'Could not save: ' + e; });
  }

  function reloadAt(thread) {
    var u = new URL(window.location.href);
    u.hash = thread ? 'thread-' + thread : '';
    window.location.replace(u.toString());
    window.location.reload();
  }

  // Heading 💬 buttons.
  if (sheet) {
    sheet.querySelectorAll('h2[id], h3[id], h4[id]').forEach(function (h) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'wb-odoc-hc';
      b.title = 'Comment on this heading';
      b.setAttribute('aria-label', 'Comment on the heading ' + headingText(h));
      b.textContent = '💬';
      b.addEventListener('click', function () { setAnchor({ heading: h.id, heading_text: headingText(h) }); });
      h.appendChild(b);
    });
  }
  lightPassages();

  // On a phone the contents start folded.
  var toc = page.querySelector('[data-odoc-toc]');
  if (toc && window.matchMedia('(max-width: 820px)').matches) toc.removeAttribute('open');

  // The selection button.
  function placePop() {
    var a = selectionAnchor();
    if (!a) { pop.hidden = true; return; }
    var rect = window.getSelection().getRangeAt(0).getBoundingClientRect();
    var box = page.getBoundingClientRect();
    pop.style.top = Math.max(0, rect.top - box.top - 40) + 'px';
    pop.style.left = Math.max(0, rect.left - box.left) + 'px';
    pop.hidden = false;
  }
  document.addEventListener('mouseup', function (e) { if (e.target !== pop) setTimeout(placePop, 0); });
  document.addEventListener('keyup', function (e) { if (e.key && e.key.indexOf('Shift') === 0) placePop(); });
  document.addEventListener('selectionchange', function () {
    if (window.matchMedia('(max-width: 820px)').matches) placePop();
  });
  pop.addEventListener('mousedown', function (e) { e.preventDefault(); });
  pop.addEventListener('click', function () {
    var a = selectionAnchor();
    pop.hidden = true;
    if (a) setAnchor(a);
  });

  form.querySelector('[data-odoc-new-clear]').addEventListener('click', function () { setAnchor(null); });
  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var text = form.querySelector('textarea').value.trim();
    if (!text) return;
    post(page.getAttribute('data-comment-url'), { id: output, text: text, anchor: anchor }, form.querySelector('[data-odoc-msg]'), function (j) { reloadAt(j.thread); });
  });

  // Threads: select, reply, resolve, reopen.
  page.querySelectorAll('[data-thread]').forEach(function (el) {
    if (el.classList.contains('wb-odoc-mark')) return;
    var id = el.getAttribute('data-thread');
    el.addEventListener('click', function (e) {
      if (e.target.closest('button, a, textarea, form')) return;
      select(id, 'mark');
    });
    var reply = el.querySelector('[data-odoc-reply]');
    var open = el.querySelector('[data-odoc-reply-open]');
    if (open && reply) {
      open.addEventListener('click', function () { reply.classList.add('wb-odoc-reply--on'); reply.querySelector('textarea').focus(); });
      reply.querySelector('[data-odoc-reply-cancel]').addEventListener('click', function () { reply.classList.remove('wb-odoc-reply--on'); });
      reply.addEventListener('submit', function (e) {
        e.preventDefault();
        var text = reply.querySelector('textarea').value.trim();
        if (!text) return;
        post(page.getAttribute('data-comment-url'), { id: output, thread: id, text: text }, reply.querySelector('[data-odoc-msg]'), function () { reloadAt(id); });
      });
    }
    var resolve = el.querySelector('[data-odoc-resolve]');
    if (resolve) resolve.addEventListener('click', function () {
      post(page.getAttribute('data-resolve-url'), { id: output, thread: id }, null, function () { reloadAt(''); });
    });
    var reopen = el.querySelector('[data-odoc-reopen]');
    if (reopen) reopen.addEventListener('click', function () {
      post(page.getAttribute('data-resolve-url'), { id: output, thread: id, reopen: true }, null, function () { reloadAt(id); });
    });
  });
  page.addEventListener('click', function (e) {
    var m = e.target.closest && e.target.closest('.wb-odoc-mark');
    if (m) select(m.getAttribute('data-thread'), 'thread');
  });
  if (window.location.hash.indexOf('#thread-') === 0) select(window.location.hash.slice(8), 'thread');

  // The send buttons are disabled in the page until the handlers above
  // are on, so an early click never submits the form natively.
  page.querySelectorAll('[data-odoc-ready]').forEach(function (b) { b.disabled = false; });

  // Day / Night: the sheet only, remembered in this browser.
  var KEY = 'odoc-sheet';
  function setSheet(v) {
    if (sheet) sheet.setAttribute('data-sheet', v);
    page.querySelectorAll('[data-sheet-set]').forEach(function (b) { b.setAttribute('aria-pressed', String(b.getAttribute('data-sheet-set') === v)); });
  }
  var saved = 'day';
  try { saved = window.localStorage.getItem(KEY) || 'day'; } catch (e) { /* no storage */ }
  setSheet(saved === 'night' ? 'night' : 'day');
  page.querySelectorAll('[data-sheet-set]').forEach(function (b) {
    b.addEventListener('click', function () {
      var v = b.getAttribute('data-sheet-set');
      setSheet(v);
      try { window.localStorage.setItem(KEY, v); } catch (e) { /* no storage */ }
    });
  });
})();
