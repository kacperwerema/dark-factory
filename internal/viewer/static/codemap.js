// The code map page: clicking a package chip paints what it depends on
// (data-mark="dep") and what reaches it through reverse imports
// (data-mark="hit"), marks the context boxes those packages sit in, and
// fills the Selection panel. The graph comes from the page's JSON
// (codemap_view.go CodemapGraph); text goes in with textContent only.
(function () {
    'use strict';
    var data = document.getElementById('codemap-graph');
    if (!data) { return; }
    var G = JSON.parse(data.textContent);
    var P = G.packages, C = G.contexts;
    var rev = {};
    Object.keys(P).forEach(function (p) { rev[p] = []; });
    Object.keys(P).forEach(function (p) { (P[p].d || []).forEach(function (d) { if (rev[d]) { rev[d].push(p); } }); });
    function closure(start, next) {
        var seen = {}, stack = [start];
        while (stack.length) {
            var x = stack.pop();
            (next(x) || []).forEach(function (y) { if (!seen[y]) { seen[y] = true; stack.push(y); } });
        }
        delete seen[start];
        return Object.keys(seen).sort();
    }
    var chips = Array.prototype.slice.call(document.querySelectorAll('[data-p]'));
    var boxes = Array.prototype.slice.call(document.querySelectorAll('[data-ctx]'));
    var initial = chips.map(function (c) { return c.getAttribute('data-mark'); });
    var initialBox = boxes.map(function (b) { return b.getAttribute('data-mark'); });
    var head = document.querySelector('[data-cm-detail-h]');
    var body = document.querySelector('[data-cm-detail]');
    function setMark(el, m) { if (m) { el.setAttribute('data-mark', m); } else { el.removeAttribute('data-mark'); } }
    function line(label, text) {
        var p = document.createElement('p');
        p.className = 'wb-panel-sub';
        var b = document.createElement('b');
        b.textContent = label + ' ';
        p.appendChild(b);
        p.appendChild(document.createTextNode(text));
        body.appendChild(p);
    }
    function byContext(list) {
        var g = {};
        list.forEach(function (p) { var c = P[p].c || ''; (g[c] = g[c] || []).push(p.replace(/^internal\//, '')); });
        return g;
    }
    function select(p) {
        var hit = closure(p, function (x) { return rev[x]; });
        var dep = closure(p, function (x) { return P[x] ? P[x].d : []; });
        var H = {}, D = {};
        hit.forEach(function (x) { H[x] = true; });
        dep.forEach(function (x) { D[x] = true; });
        chips.forEach(function (c) {
            var x = c.dataset.p;
            setMark(c, x === p ? 'sel' : H[x] ? 'hit' : D[x] ? 'dep' : '');
        });
        boxes.forEach(function (b) {
            var k = b.dataset.ctx, h = false, d = false;
            hit.forEach(function (x) { h = h || P[x].c === k; });
            dep.forEach(function (x) { d = d || P[x].c === k; });
            setMark(b, h ? 'reaches' : d ? 'dep' : '');
        });
        head.textContent = p;
        body.textContent = '';
        line('Context:', (C[P[p].c] || 'unmapped') + ' · ' + P[p].l + ' lines');
        line('A change reaches:', hit.length + ' packages · ' + (rev[p] || []).length + ' import it directly');
        line('It depends on:', dep.length + ' packages');
        if (P[p].u && P[p].u.length) { line('Imports up a layer:', P[p].u.join(', ')); }
        var g = byContext(hit);
        Object.keys(g).forEach(function (k) { line((C[k] || 'unmapped') + ':', g[k].join(', ')); });
    }
    document.addEventListener('click', function (ev) {
        var chip = ev.target.closest('[data-p]');
        if (chip) { select(chip.dataset.p); return; }
        if (ev.target.closest('[data-cm-clear]')) {
            chips.forEach(function (c, i) { setMark(c, initial[i]); });
            boxes.forEach(function (b, i) { setMark(b, initialBox[i]); });
            head.textContent = 'Selection';
            body.textContent = '';
            line('', 'Nothing selected. Click a package.');
        }
    });
}());
