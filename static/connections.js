// Roads on the company page: one road per pair of rooms that pass work
// (WbConnections.Roads, roads_view.go), routed like streets through the
// gaps between the rooms, with the open traffic moving along it as parcels:
// ● request, ▲ handoff, ■ output, ◆ dependency, in the sending room's
// colour. With prefers-reduced-motion the parcels stand still and the road
// shows its counts. A declared road with no traffic is quiet (thin, no
// parcels); traffic off the declared routes gets a red "!" sign.
// Hover, keyboard focus or a tap on a room lights its roads; a road (or a
// row of the list) lights itself, the caption says what it carries, and the
// road links to the connections page.
// Roads that share a street merge into it: the router prefers cells an
// earlier road took. Animations pause while the floor is off screen or the
// tab hidden.
// Without this script the block's rows still list every connection.
(function () {
  var floor = document.querySelector('[data-connections]');
  if (!floor) return;
  var svg = floor.querySelector('.wb-conn-lines');
  var dataEl = document.querySelector('script[data-roads]');
  if (!svg || !dataEl) return;
  var roads;
  try { roads = JSON.parse(dataEl.textContent) || []; } catch (e) { return; }
  var NS = 'http://www.w3.org/2000/svg';
  var dense = floor.hasAttribute('data-dense');
  var still = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)');
  var caption = document.querySelector('[data-testid="company.connections.focus"]');
  var idle = caption ? caption.textContent : '';
  var rows = Array.prototype.slice.call(document.querySelectorAll('.wb-conn-row'));
  var glyph = { request: '●', handoff: '▲', output: '■', dependency: '◆' };
  var kinds = ['request', 'handoff', 'output', 'dependency'];

  // part clones one of the page's road parts (<template data-road-parts>),
  // which carry the class and data-testid; el makes any other element.
  var parts = document.querySelector('template[data-road-parts]');
  function part(name, attrs) {
    var p = parts && parts.content.querySelector('[data-part="' + name + '"]');
    var e = p ? p.cloneNode(false) : document.createElementNS(NS, name === 'bed' ? 'path' : name === 'link' ? 'a' : 'g');
    e.removeAttribute('data-part');
    for (var k in attrs) {
      if (k === 'class') e.setAttribute('class', ((e.getAttribute('class') || '') + ' ' + attrs[k]).trim());
      else e.setAttribute(k, attrs[k]);
    }
    return e;
  }
  function el(name, attrs) {
    var e = document.createElementNS(NS, name);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    return e;
  }
  function rect(e, base) {
    var b = e.getBoundingClientRect();
    return { l: b.left - base.left, r: b.right - base.left, t: b.top - base.top, b: b.bottom - base.top };
  }

  // The grid: cells of CELL px; each costs FREE to cross, an obstacle
  // (a room's content) WALL, a cell an earlier road took SHARED.
  var CELL = 8, FREE = 1, SHARED = 0.55, WALL = 40, TURN = 3;
  var cols, nrows, cost, used;

  // walls are the floor's solid parts: a room without rooms inside, and
  // the parts of a group around its rooms (its head, its lead's column,
  // its foot). The corridor between HQ and Product is a street.
  function walls(base) {
    var out = [];
    function walk(e) {
      if (e === svg || e.classList.contains('wb-corridor')) return;
      if (e.querySelector('[data-box]')) {
        Array.prototype.forEach.call(e.children, walk);
        return;
      }
      var r = rect(e, base);
      if (r.r > r.l && r.b > r.t) out.push(r);
    }
    floor.querySelectorAll('.wb-company-row').forEach(function (row) {
      Array.prototype.forEach.call(row.children, walk);
    });
    return out;
  }
  function fill(r, c, pad) {
    var c0 = Math.max(0, Math.floor((r.l - pad) / CELL)), c1 = Math.min(cols - 1, Math.floor((r.r + pad) / CELL));
    var r0 = Math.max(0, Math.floor((r.t - pad) / CELL)), r1 = Math.min(nrows - 1, Math.floor((r.b + pad) / CELL));
    for (var y = r0; y <= r1; y++) for (var x = c0; x <= c1; x++) cost[y * cols + x] = c;
  }
  // doors are the free cells just outside a room's box.
  function doors(r) {
    var out = [], seen = {};
    function add(x, y) {
      if (x < 0 || y < 0 || x >= cols || y >= nrows) return;
      var c = y * cols + x;
      if (cost[c] < WALL && !seen[c]) { seen[c] = true; out.push(c); }
    }
    // Two rings: the first can sit under the room's shadow.
    for (var k = 1; k <= 2; k++) {
      var c0 = Math.floor(r.l / CELL) - k, c1 = Math.floor(r.r / CELL) + k;
      var r0 = Math.floor(r.t / CELL) - k, r1 = Math.floor(r.b / CELL) + k;
      for (var x = c0; x <= c1; x++) { add(x, r0); add(x, r1); }
      for (var y = r0 + 1; y < r1; y++) { add(c0, y); add(c1, y); }
    }
    return out;
  }

  // A small binary heap of [priority, state].
  function Heap() { this.p = []; this.s = []; }
  Heap.prototype.push = function (p, s) {
    var a = this.p, b = this.s, i = a.length;
    a.push(p); b.push(s);
    while (i > 0) {
      var j = (i - 1) >> 1;
      if (a[j] <= p) break;
      a[i] = a[j]; b[i] = b[j]; i = j;
    }
    a[i] = p; b[i] = s;
  };
  Heap.prototype.pop = function () {
    var a = this.p, b = this.s, top = b[0], lp = a.pop(), ls = b.pop(), n = a.length;
    if (n) {
      var i = 0;
      for (;;) {
        var l = 2 * i + 1, r = l + 1, m = i, mp = lp;
        if (l < n && a[l] < mp) { m = l; mp = a[l]; }
        if (r < n && a[r] < mp) { m = r; }
        if (m === i) break;
        a[i] = a[m]; b[i] = b[m]; i = m;
      }
      a[i] = lp; b[i] = ls;
    }
    return top;
  };

  // route is the cheapest street from any door of a to any door of b:
  // A* over (cell, heading), a turn costing TURN, toward b's box.
  var DX = [1, 0, -1, 0], DY = [0, 1, 0, -1];
  function route(ra, rb) {
    var from = doors(ra), to = doors(rb);
    if (!from.length || !to.length) return null;
    var goal = {};
    to.forEach(function (c) { goal[c] = true; });
    var n = cols * nrows * 4;
    var dist = new Float32Array(n).fill(Infinity), prev = new Int32Array(n).fill(-1);
    var gx = (rb.l + rb.r) / 2 / CELL, gy = (rb.t + rb.b) / 2 / CELL;
    var hw = (rb.r - rb.l) / 2 / CELL, hh = (rb.b - rb.t) / 2 / CELL;
    function h(c) {
      var x = c % cols, y = (c - x) / cols;
      return (Math.max(0, Math.abs(x - gx) - hw) + Math.max(0, Math.abs(y - gy) - hh)) * SHARED;
    }
    var heap = new Heap();
    from.forEach(function (c) {
      for (var d = 0; d < 4; d++) { dist[c * 4 + d] = 0; heap.push(h(c), c * 4 + d); }
    });
    var end = -1, guard = n * 2;
    while (heap.p.length && guard-- > 0) {
      var s = heap.pop(), c = s >> 2, d = s & 3, g = dist[s];
      if (goal[c]) { end = s; break; }
      var x = c % cols, y = (c - x) / cols;
      for (var nd = 0; nd < 4; nd++) {
        if (nd === (d + 2) % 4) continue;
        var nx = x + DX[nd], ny = y + DY[nd];
        if (nx < 0 || ny < 0 || nx >= cols || ny >= nrows) continue;
        var nc = ny * cols + nx, ns = nc * 4 + nd;
        var step = used[nc] ? Math.min(SHARED, cost[nc]) : cost[nc];
        var ng = g + step + (nd === d ? 0 : TURN);
        if (ng < dist[ns]) { dist[ns] = ng; prev[ns] = s; heap.push(ng + h(nc), ns); }
      }
    }
    if (end < 0) return null;
    var cells = [];
    for (var t = end; t >= 0; t = prev[t]) cells.push(t >> 2);
    cells.reverse();
    // Consecutive duplicates come from the start's four headings.
    return cells.filter(function (c, i) { return i === 0 || c !== cells[i - 1]; });
  }

  // points turns the cells into corners, with a stub onto each room's edge.
  function centre(c) {
    var x = c % cols, y = (c - x) / cols;
    return { x: x * CELL + CELL / 2, y: y * CELL + CELL / 2 };
  }
  function onto(p, r) {
    if (p.x < r.l) return { x: r.l, y: p.y };
    if (p.x > r.r) return { x: r.r, y: p.y };
    if (p.y < r.t) return { x: p.x, y: r.t };
    return { x: p.x, y: r.b };
  }
  function points(cells, ra, rb) {
    var pts = cells.map(centre);
    pts.unshift(onto(pts[0], ra));
    pts.push(onto(pts[pts.length - 1], rb));
    var out = [pts[0]];
    for (var i = 1; i < pts.length - 1; i++) {
      var a = out[out.length - 1], b = pts[i], c = pts[i + 1];
      if ((a.x === b.x && b.x === c.x) || (a.y === b.y && b.y === c.y)) continue;
      out.push(b);
    }
    out.push(pts[pts.length - 1]);
    return out;
  }
  // pathD rounds every corner.
  function pathD(p) {
    var d = 'M' + p[0].x + ' ' + p[0].y;
    for (var i = 1; i < p.length - 1; i++) {
      var a = p[i - 1], b = p[i], c = p[i + 1];
      var r = Math.min(10, Math.hypot(b.x - a.x, b.y - a.y) / 2, Math.hypot(c.x - b.x, c.y - b.y) / 2);
      var ux = Math.sign(b.x - a.x), uy = Math.sign(b.y - a.y), vx = Math.sign(c.x - b.x), vy = Math.sign(c.y - b.y);
      d += ' L' + (b.x - ux * r) + ' ' + (b.y - uy * r) + ' Q' + b.x + ' ' + b.y + ' ' + (b.x + vx * r) + ' ' + (b.y + vy * r);
    }
    var z = p[p.length - 1];
    return d + ' L' + z.x + ' ' + z.y;
  }
  function markUsed(cells) { cells.forEach(function (c) { used[c] = 1; }); }

  // parcel is one moving (or, reduced, standing) piece of traffic.
  function parcel(kind, color) {
    var g = part('parcel', { 'data-kind': kind });
    var shape;
    if (kind === 'output') shape = el('rect', { x: -5, y: -5, width: 10, height: 10, rx: 1.5 });
    else if (kind === 'handoff') shape = el('path', { d: 'M0 -6 L6 5 L-6 5 Z' });
    else if (kind === 'dependency') shape = el('path', { d: 'M0 -6 L6 0 L0 6 L-6 0 Z' });
    else shape = el('circle', { r: 5 });
    shape.setAttribute('fill', color);
    g.appendChild(shape);
    return g;
  }
  function tally(road) {
    var n = {};
    road.traffic.forEach(function (f) { n[f.kind] = (n[f.kind] || 0) + f.n; });
    return kinds.filter(function (k) { return n[k]; }).map(function (k) { return glyph[k] + n[k]; }).join(' ');
  }
  function tag(e, i, road) {
    e.setAttribute('data-road', i);
    e.setAttribute('data-boxes', ' ' + road.a + ' ' + road.b + ' ');
    return e;
  }

  // draw reads every box first, routes, then writes the roads in one go.
  var MAX_PARCELS = 60;
  function draw() {
    var base = floor.getBoundingClientRect();
    var W = Math.ceil(base.width), H = Math.ceil(base.height);
    cols = Math.ceil(W / CELL); nrows = Math.ceil(H / CELL);
    cost = new Float32Array(cols * nrows).fill(FREE);
    used = new Uint8Array(cols * nrows);
    var rects = {};
    floor.querySelectorAll('[data-box]').forEach(function (e) { rects[e.getAttribute('data-box')] = rect(e, base); });
    walls(base).forEach(function (r) { fill(r, WALL, 4); });

    var casing = el('g', { 'class': 'wb-road-casing' }), paint = el('g', { 'class': 'wb-road-paint' }), top = el('g', { 'class': 'wb-road-top' });
    var budget = MAX_PARCELS, moving = !(still && still.matches);
    // Busy roads route first, so quieter ones join their streets.
    var order = roads.map(function (r, i) { return i; }).sort(function (a, b) { return roads[b].open - roads[a].open; });
    order.forEach(function (i) {
      var road = roads[i], ra = rects[road.a], rb = rects[road.b];
      if (!ra || !rb) return;
      var cells = route(ra, rb);
      if (!cells) return;
      markUsed(cells);
      var pts = points(cells, ra, rb), d = pathD(pts);
      var quiet = road.route === 'quiet', off = road.route === 'undeclared';
      var cls = quiet ? 'wb-road--quiet' : off ? 'wb-road--undeclared' : '';
      casing.appendChild(tag(part('bed', { 'class': cls, d: d, id: 'wb-road-' + i }), i, road));
      if (!quiet) paint.appendChild(tag(el('path', { 'class': 'wb-road-line', d: d }), i, road));

      var a = tag(part('link', { 'class': cls, href: road.url, 'aria-label': road.label }), i, road);
      var title = el('title', {}); title.textContent = road.label; a.appendChild(title);
      a.appendChild(el('path', { 'class': 'wb-road-hit', d: d }));
      // A door where the road meets each room.
      [pts[0], pts[pts.length - 1]].forEach(function (q) {
        a.appendChild(el('rect', { 'class': 'wb-road-door', x: q.x - 4, y: q.y - 4, width: 8, height: 8, rx: 2 }));
      });
      var probe = casing.lastChild, len = probe.getTotalLength();
      var mid = probe.getPointAtLength(len / 2);
      if (off) {
        var at = probe.getPointAtLength(len / 3);
        var sign = part('sign', { transform: 'translate(' + Math.max(at.x, 11) + ' ' + at.y + ')' });
        sign.appendChild(el('path', { d: 'M0 -9 L9 0 L0 9 L-9 0 Z' }));
        var bang = el('text', { 'text-anchor': 'middle', y: 4 }); bang.textContent = '!';
        sign.appendChild(bang);
        a.appendChild(sign);
      }
      if (!quiet && road.traffic && road.traffic.length) {
        if (moving) {
          var dur = Math.max(5, len / 40);
          road.traffic.forEach(function (f) {
            var k = Math.min(f.n, dense ? 1 : 3, budget);
            for (var j = 0; j < k; j++) {
              budget--;
              var p = parcel(f.kind, f.color);
              var m = el('animateMotion', { dur: dur.toFixed(1) + 's', repeatCount: 'indefinite', begin: (-(dur * (j + (f.dir === 'ba' ? 0.5 : 0)) / k)).toFixed(2) + 's', calcMode: 'linear' });
              if (f.dir === 'ba') { m.setAttribute('keyPoints', '1;0'); m.setAttribute('keyTimes', '0;1'); }
              var mp = el('mpath', {}); mp.setAttribute('href', '#wb-road-' + i);
              m.appendChild(mp); p.appendChild(m);
              a.appendChild(p);
            }
          });
        } else {
          // Reduced motion: the counts stand on the road's middle.
          var t = tally(road);
          var w = 10 + t.length * 7.5;
          var cx = Math.min(Math.max(mid.x, w / 2 + 2), W - w / 2 - 2);
          var chip = part('tally', { transform: 'translate(' + cx + ' ' + mid.y + ')' });
          chip.appendChild(el('rect', { x: -w / 2, y: -10, width: w, height: 20, rx: 10 }));
          var tt = el('text', { 'text-anchor': 'middle', y: 4 }); tt.textContent = t;
          chip.appendChild(tt);
          a.appendChild(chip);
        }
      }
      top.appendChild(a);
    });
    while (svg.firstChild) svg.removeChild(svg.firstChild);
    svg.setAttribute('width', W); svg.setAttribute('height', H);
    svg.appendChild(casing); svg.appendChild(paint); svg.appendChild(top);
    light(lit);
    pauseIfAway();
  }

  // lit is what is lighted now: {box}, {road} or {row}; pinned what a tap fixed.
  var lit = null, pinned = null;
  function same(a, b) { return a === b || (a && b && a.box === b.box && a.road === b.road && a.row === b.row); }
  function roomName(box) {
    var e = floor.querySelector('[data-box="' + box + '"] .wb-room-title');
    return e ? e.textContent.trim() : box;
  }
  function roadOfRow(row) {
    for (var i = 0; i < roads.length; i++) if ((roads[i].rows || []).indexOf(row) >= 0) return i;
    return -1;
  }
  function light(sel) {
    lit = sel;
    floor.querySelectorAll('.wb-room--lit').forEach(function (e) { e.classList.remove('wb-room--lit'); });
    svg.querySelectorAll('.is-on').forEach(function (e) { e.classList.remove('is-on'); });
    rows.forEach(function (r) { r.classList.remove('is-on'); });
    svg.classList.toggle('is-focus', !!sel);
    if (!sel) {
      if (caption) { caption.textContent = idle; caption.hidden = !dense; }
      return;
    }
    var on = [];
    if (sel.box != null) {
      // A group lights its own roads and those of the rooms inside it.
      var room0 = floor.querySelector('[data-box="' + sel.box + '"]'), keys = [sel.box];
      if (room0) room0.querySelectorAll('[data-box]').forEach(function (e) { keys.push(e.getAttribute('data-box')); });
      on = svg.querySelectorAll(keys.map(function (k) { return '[data-boxes~="' + k + '"]'; }).join(','));
    }
    else {
      var i = sel.road != null ? sel.road : roadOfRow(sel.row);
      if (i >= 0) on = svg.querySelectorAll('[data-road="' + i + '"]');
    }
    var seen = {}, n = 0;
    on.forEach(function (e) {
      e.classList.add('is-on');
      var i = e.getAttribute('data-road');
      if (!seen[i]) {
        seen[i] = true; n++;
        (roads[i].rows || []).forEach(function (r) { if (rows[r]) rows[r].classList.add('is-on'); });
      }
    });
    if (sel.box != null) {
      var room = floor.querySelector('[data-box="' + sel.box + '"]');
      if (room) room.classList.add('wb-room--lit');
    }
    if (caption) {
      caption.hidden = false;
      if (sel.box != null) caption.textContent = roomName(sel.box) + ': ' + (n === 1 ? '1 road' : n + ' roads') + (pinned ? ' · tap the room again to let go' : '');
      else if (sel.road != null) caption.textContent = roads[sel.road].label + ' · open it for the list';
      else if (rows[sel.row]) caption.textContent = rows[sel.row].textContent.replace(/\s+/g, ' ').trim();
    }
  }
  function want(sel) { if (!same(sel, lit)) light(sel); }
  function selAt(target) {
    var r = target && target.closest ? target.closest('.wb-road-link') : null;
    if (r) return { road: +r.getAttribute('data-road') };
    var e = target && target.closest ? target.closest('[data-box]') : null;
    return e && floor.contains(e) ? { box: e.getAttribute('data-box') } : null;
  }

  floor.addEventListener('mouseover', function (ev) { want(selAt(ev.target) || pinned); });
  floor.addEventListener('mouseleave', function () { want(pinned); });
  floor.addEventListener('focusin', function (ev) { want(selAt(ev.target) || pinned); });
  floor.addEventListener('focusout', function (ev) {
    if (!floor.contains(ev.relatedTarget)) want(pinned);
  });
  // A tap on a room (not on its links or buttons) pins its roads, for touch.
  floor.addEventListener('click', function (ev) {
    if (ev.target.closest('a, button, input, textarea, select, summary')) return;
    var sel = selAt(ev.target);
    pinned = sel && !same(sel, pinned) ? sel : null;
    light(pinned);
  });
  document.addEventListener('keydown', function (ev) {
    if (ev.key !== 'Escape' || !pinned) return;
    pinned = null;
    light(null);
  });
  rows.forEach(function (row, i) {
    row.addEventListener('mouseenter', function () { want({ row: i }); });
    row.addEventListener('focus', function () { want({ row: i }); });
    row.addEventListener('mouseleave', function () { want(pinned); });
    row.addEventListener('blur', function () { want(pinned); });
  });

  // The parcels stop while nobody can see them: tab hidden or floor off
  // screen.
  var onScreen = true;
  function pauseIfAway() {
    if (!svg.pauseAnimations) return;
    if (document.hidden || !onScreen) svg.pauseAnimations();
    else svg.unpauseAnimations();
  }
  document.addEventListener('visibilitychange', pauseIfAway);
  if (window.IntersectionObserver) {
    new IntersectionObserver(function (es) { onScreen = es[0].isIntersecting; pauseIfAway(); }).observe(floor);
  }
  if (still && still.addEventListener) still.addEventListener('change', redraw);

  // One redraw per frame however often the floor resizes; the floor's own
  // size is what counts (ResizeObserver also fires on its first look).
  var queued = false, lastW = -1, lastH = -1;
  function redraw() {
    if (queued) return;
    queued = true;
    requestAnimationFrame(function () { queued = false; draw(); });
  }
  draw();
  if (window.ResizeObserver) {
    new ResizeObserver(function () {
      var b = floor.getBoundingClientRect();
      if (Math.round(b.width) === lastW && Math.round(b.height) === lastH) return;
      lastW = Math.round(b.width); lastH = Math.round(b.height);
      redraw();
    }).observe(floor);
  } else window.addEventListener('resize', redraw);
})();
