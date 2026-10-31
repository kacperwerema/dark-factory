// Workshop Board: the map's tabs, "Show on map", the roads that carry
// the engine's release candidate, and a quiet refresh.
(function () {
  // Tabs in the right panel.
  document.querySelectorAll('.wb-tab').forEach(function (tab) {
    tab.addEventListener('click', function () {
      document.querySelectorAll('.wb-tab').forEach(function (t) {
        var on = t === tab;
        t.setAttribute('aria-selected', on);
        var panel = document.getElementById(t.getAttribute('aria-controls'));
        if (panel) panel.hidden = !on;
      });
    });
  });

  // "Show on map": scroll to the lot and flash it.
  document.querySelectorAll('[data-show]').forEach(function (link) {
    link.addEventListener('click', function (e) {
      var lot = document.getElementById(link.getAttribute('data-show'));
      if (!lot) return;
      e.preventDefault();
      lot.scrollIntoView({ behavior: 'smooth', block: 'center' });
      lot.classList.remove('wb-lot--flash');
      void lot.offsetWidth;
      lot.classList.add('wb-lot--flash');
      setTimeout(function () { lot.classList.remove('wb-lot--flash'); }, 2400);
    });
  });

  // Roads: the engine (dark-factory) in the middle, a road to every project.
  // A road leaves the engine's side, runs to the project's column and turns
  // to its lot, like the streets on the design. A project testing the
  // engine's release candidate gets a truck on its road; a building site's road
  // is paler (planned).
  var lots = document.getElementById('lots');
  var svg = document.getElementById('roads');
  var ns = 'http://www.w3.org/2000/svg';
  function path(d, stroke, width, dash) {
    var p = document.createElementNS(ns, 'path');
    p.setAttribute('d', d); p.setAttribute('fill', 'none'); p.setAttribute('stroke', stroke); p.setAttribute('stroke-width', width);
    p.setAttribute('stroke-linecap', 'round'); p.setAttribute('stroke-linejoin', 'round');
    if (dash) p.setAttribute('stroke-dasharray', dash);
    svg.appendChild(p);
  }
  function truck(text, x, y) {
    var t = document.createElement('div');
    t.className = 'wb-truck';
    t.innerHTML = '<svg width="18" height="14" viewBox="0 0 24 18" fill="none" stroke="#1B1F2A" stroke-width="2" stroke-linejoin="round" aria-hidden="true"><path d="M1 2h13v11H1zM14 6h5l4 4v3h-9z"/><circle cx="6" cy="15" r="2" fill="#fff"/><circle cx="18" cy="15" r="2" fill="#fff"/></svg>';
    t.appendChild(document.createTextNode(text));
    t.style.left = x + 'px'; t.style.top = y + 'px';
    lots.appendChild(t);
  }
  function drawRoads() {
    if (!lots || !svg) return;
    svg.innerHTML = '';
    lots.querySelectorAll('.wb-truck').forEach(function (t) { t.remove(); });
    var engine = lots.getAttribute('data-engine');
    var hub = engine && document.getElementById('lot-' + engine);
    if (!hub) return;
    var box = lots.getBoundingClientRect();
    var h = hub.getBoundingClientRect();
    var hl = h.left - box.left, hr = h.right - box.left, hy = h.top + h.height / 2 - box.top, hx = (hl + hr) / 2;
    // Every road's surface first, then every road's white strips on top, so
    // a road drawn later never covers another's strips.
    var roads = [];
    lots.querySelectorAll('[data-slug]').forEach(function (lot) {
      if (lot === hub) return;
      var b = lot.getBoundingClientRect();
      var cx = b.left + b.width / 2 - box.left, top = b.top - box.top, bottom = b.bottom - box.top;
      var d, tx, ty;
      if (cx > hl && cx < hr) {
        // Same column (narrow screens): straight up or down.
        var y0 = top > hy ? h.bottom - box.top : h.top - box.top;
        var y1 = top > hy ? top : bottom;
        d = 'M' + cx + ' ' + y0 + ' V' + y1; tx = cx; ty = (y0 + y1) / 2;
      } else {
        var sx = cx < hx ? hl : hr;
        if (bottom < hy || top > hy) {
          // Above or below the engine: along, then up or down to the lot.
          d = 'M' + sx + ' ' + hy + ' H' + cx + ' V' + (bottom < hy ? bottom : top);
          tx = (sx + cx) / 2;
        } else {
          // Level with the engine: straight to the lot's near side.
          var ex = cx < hx ? b.right - box.left : b.left - box.left;
          d = 'M' + sx + ' ' + hy + ' H' + ex;
          tx = (sx + ex) / 2;
        }
        ty = hy;
      }
      roads.push({ d: d, site: lot.hasAttribute('data-site') });
      if (lot.getAttribute('data-rc')) truck('RC ' + lot.getAttribute('data-rc') + ' · test', tx, ty);
    });
    roads.forEach(function (r) { path(r.d, r.site ? '#D3DAE4' : '#C7D0DC', 16); });
    roads.forEach(function (r) { path(r.d, '#FFFFFF', 2, '7 7'); });
  }

  // "Start CEO" on a building site: spawn the project's lead (or, with
  // data-spawn-what, the department lead the button names).
  document.querySelectorAll('[data-spawn-lead]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var box = btn.closest('.wb-site') || btn.closest('[data-spawn-box]') || btn.parentNode;
      var msg = box.querySelector('.wb-site-msg');
      var what = btn.getAttribute('data-spawn-what') || 'the CEO';
      btn.disabled = true;
      msg.textContent = 'Starting ' + what + '…';
      fetch(btn.getAttribute('data-spawn-lead'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
        .then(function (r) { return r.json(); })
        .then(function (resp) {
          if (resp.status === 'error') {
            var step = (resp.steps || []).filter(function (s) { return s.status === 'error'; })[0];
            msg.textContent = 'Could not start ' + what + (step ? ': ' + step.detail : '.');
            btn.disabled = false;
            return;
          }
          msg.textContent = 'Started ' + (resp.session || what) + '.';
          setTimeout(function () { location.reload(); }, 1500);
        })
        .catch(function (e) { msg.textContent = 'Could not start ' + what + ': ' + e; btn.disabled = false; });
    });
  });

  drawRoads();
  window.addEventListener('resize', drawRoads);
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(drawRoads);

  // Message to the CEO (L1): paste into the lead's session.
  document.querySelectorAll('form[data-send]').forEach(function (form) {
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var input = form.querySelector('input[name=text]');
      var msg = form.querySelector('.wb-site-msg');
      var text = input.value.trim();
      if (!text) return;
      msg.textContent = 'Sending…';
      fetch(form.getAttribute('data-send'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text: text }) })
        .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, body: j }; }); })
        .then(function (res) {
          if (!res.ok || res.body.status === 'error') {
            var step = (res.body.steps || [])[0];
            msg.textContent = 'Not sent' + (step ? ': ' + step.name + (step.detail ? ' (' + step.detail + ')' : '') : '.');
            return;
          }
          input.value = '';
          msg.textContent = 'Sent to ' + (res.body.session || 'the CEO') + '.';
          form.dispatchEvent(new CustomEvent('wb:sent', { bubbles: true }));
          setTimeout(function () { location.reload(); }, 4000);
        })
        .catch(function (err) { msg.textContent = 'Not sent: ' + err; });
    });
  });

  // Refresh once a minute while the page is in view, unless a details panel
  // is open.
  setInterval(function () {
    var typing = document.activeElement && document.activeElement.tagName === 'INPUT' && document.activeElement.value;
    if (document.visibilityState === 'visible' && !document.querySelector('details[open], [data-hold-open]:not([hidden])') && !typing) location.reload();
  }, 60000);

  // Live refresh, as the classic pages do: a project page polls the
  // project's /hash every 4 s and reloads when an issue, its status or an
  // agent session changed. It holds off while you type, a dialog is open or
  // a fold marked data-hold or a shown [data-hold-open] form is open, or for 10 s after a click (so a
  // button's result line can be read), and reloads once that ends.
  var proj = location.pathname.match(/^\/p\/[^/]+/);
  // The CEO chat polls its own pane and a station's Edit form holds a
  // draft: neither reloads on its own.
  if (proj && window.fetch && !/\/(lead|edit)$/.test(location.pathname)) {
    var last = null, pending = false, acted = 0;
    ['click', 'submit', 'keydown'].forEach(function (ev) { document.addEventListener(ev, function () { acted = Date.now(); }, true); });
    var held = function () {
      if (Date.now() - acted < 10000) return true;
      var a = document.activeElement;
      if (a && (/^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName) || a.isContentEditable)) return true;
      return !!document.querySelector('dialog[open], details[data-hold][open], [data-hold-refresh], [data-hold-open]:not([hidden])');
    };
    setInterval(function () {
      if (document.visibilityState !== 'visible') return;
      if (pending) { if (!held()) location.reload(); return; }
      fetch(proj[0] + '/hash', { cache: 'no-store' })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (d) {
          if (!d || !d.hash) return;
          if (last !== null && d.hash !== last) {
            pending = true;
            if (!held()) location.reload();
          }
          last = d.hash;
        })
        .catch(function () {});
    }, 4000);
  }
})();

// Decisions log (L2): copy a decision's ref, and "Question it" starts a
// message to the CEO in the box on the right for the owner to finish.
(function () {
  var toast = document.querySelector('.wb-toast');
  function say(text) {
    if (!toast) return;
    toast.textContent = text;
    toast.classList.add('wb-toast--on');
    setTimeout(function () { toast.classList.remove('wb-toast--on'); }, 2200);
  }
  document.querySelectorAll('.wb-copyref').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var ref = btn.getAttribute('data-ref');
      if (!navigator.clipboard) { say(ref); return; }
      navigator.clipboard.writeText(ref).then(function () { say('Copied ' + ref); }, function () { say(ref); });
    });
  });
  // Confirm records the owner's words on the decision itself (no CEO).
  document.querySelectorAll('form[data-confirm]').forEach(function (form) {
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var input = form.querySelector('input[name=text]');
      var msg = form.querySelector('.wb-site-msg');
      var btn = form.querySelector('button[type=submit]');
      btn.disabled = true;
      msg.textContent = 'Confirming…';
      fetch(form.getAttribute('data-confirm'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text: input.value.trim() }) })
        .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, body: j }; }); })
        .then(function (res) {
          if (!res.ok) { msg.textContent = 'Not confirmed: ' + (res.body.error || 'error'); btn.disabled = false; return; }
          msg.textContent = 'Confirmed ' + res.body.ref + '.';
          say('Confirmed ' + res.body.ref);
          setTimeout(function () { location.reload(); }, 600);
        })
        .catch(function (err) { msg.textContent = 'Not confirmed: ' + err; btn.disabled = false; });
    });
  });
  document.querySelectorAll('[data-question]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var input = document.getElementById('wb-msg-input');
      if (!input || input.disabled) return;
      input.value = btn.getAttribute('data-question');
      input.scrollIntoView({ behavior: 'smooth', block: 'center' });
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    });
  });
  // A room's connectors stay unfolded when the URL names them (after Check now).
  document.querySelectorAll('details.wb-connectors').forEach(function (box) {
    if (location.hash === '#' + box.id) box.open = true;
  });
  // After Check now, the room polls its connectors until every check has
  // a result from after the click, then reloads (30 s at most).
  document.querySelectorAll('[data-checking]').forEach(function (box) {
    var since = Number(box.getAttribute('data-checking')) * 1000;
    var tries = 0;
    var poll = function () {
      if (++tries > 15) { location.replace(location.pathname + location.hash); return; }
      fetch(box.getAttribute('data-state'), { headers: { Accept: 'application/json' } })
        .then(function (res) { return res.json(); })
        .then(function (list) {
          var done = list.every(function (c) { return !c.has_check || (c.last_check && Date.parse(c.last_check.date) >= since); });
          if (done) { location.replace(location.pathname + location.hash); } else { setTimeout(poll, 2000); }
        })
        .catch(function () { setTimeout(poll, 2000); });
    };
    setTimeout(poll, 1000);
  });
})();
