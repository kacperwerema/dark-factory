// The post-mortem queue: each group's buttons act on its ticked rows (a
// group of copies starts with every copy ticked: one answer covers it). The
// first click asks, the second posts {act, items: [{file, id}], note, sha}
// to …/postmortems/mark, or {items} to …/postmortems/undo for Marked lately's
// Undo. Only the owner's browser passes (the server refuses curl and
// agents). Each row shows its own outcome; the page reloads when nothing
// failed.
(function () {
    'use strict';
    var root = document.querySelector('[data-pmq-root]');
    if (!root) { return; }
    var words = { agree: 'Agree', refuse: 'Refuse', overtaken: 'Overtaken', undo: 'Undo' };
    var doneWords = { agree: 'Applied', refuse: 'Refuse', overtaken: 'Overtaken', undo: 'Reopened' };
    function ticked(group) {
        return Array.prototype.filter.call(group.querySelectorAll('[data-pmq-row]'), function (row) {
            var box = row.querySelector('[data-pmq-tick]');
            return box.checked && !box.disabled;
        });
    }
    // refresh puts the ticked count back on the group's buttons, which ends
    // a waiting confirm: what it asked about has changed.
    function refresh(group) {
        var n = ticked(group).length;
        group.querySelectorAll('[data-pmq-act]').forEach(function (b) {
            delete b.dataset.armed;
            b.textContent = words[b.dataset.pmqAct] + ' ' + n;
            b.disabled = n === 0;
        });
    }
    function show(row, text, ok) {
        var msg = row.querySelector('[data-pmq-msg]');
        msg.textContent = text;
        msg.className = 'wb-ny-msg wb-pmq-msg ' + (ok ? 'wb-ny-msg--ok' : 'wb-ny-msg--err');
    }
    function post(group, btn) {
        var rows = ticked(group);
        var act = btn.dataset.pmqAct;
        var undo = act === 'undo';
        var sha = group.querySelector('[data-pmq-sha]');
        var note = group.querySelector('[data-pmq-note]');
        var failed = undo ? 'Not undone: ' : 'Not marked: ';
        btn.disabled = true;
        fetch(undo ? root.dataset.undoUrl : root.dataset.markUrl, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
            body: JSON.stringify({
                act: act, note: note ? note.value : '', sha: sha ? sha.value : '',
                items: rows.map(function (r) { return { file: r.dataset.file, id: r.dataset.id }; })
            })
        }).then(function (r) {
            return r.json().catch(function () { return { error: r.statusText }; }).then(function (j) { return { ok: r.ok, j: j }; });
        }).then(function (res) {
            if (!res.ok) {
                rows.forEach(function (r) { show(r, failed + (res.j.error || 'the server refused it') + '.', false); });
                refresh(group);
                return;
            }
            (res.j.outcomes || []).forEach(function (o, i) {
                var row = rows[i];
                if (o.error) { show(row, failed + o.error + '.', false); return; }
                show(row, doneWords[act] + ': ' + o.line, true);
                row.classList.add('wb-pmq-row--done');
                row.querySelector('[data-pmq-tick]').disabled = true;
            });
            refresh(group);
            if (res.j.ok) { setTimeout(function () { window.location.reload(); }, 1200); }
        }).catch(function (err) {
            rows.forEach(function (r) { show(r, failed + err + '.', false); });
            refresh(group);
        });
    }
    root.addEventListener('click', function (ev) {
        var group = ev.target.closest('[data-pmq-group]');
        if (!group) { return; }
        var btn = ev.target.closest('[data-pmq-act]');
        if (!btn || btn.disabled) { return; }
        if (btn.dataset.armed) { post(group, btn); return; }
        refresh(group);
        btn.dataset.armed = '1';
        btn.textContent = 'Confirm: ' + words[btn.dataset.pmqAct].toLowerCase() + ' ' + ticked(group).length;
    });
    root.addEventListener('change', function (ev) {
        var group = ev.target.closest('[data-pmq-group]');
        if (!group) { return; }
        if (ev.target.matches('[data-pmq-all]')) {
            group.querySelectorAll('[data-pmq-tick]').forEach(function (b) { if (!b.disabled) { b.checked = ev.target.checked; } });
            refresh(group);
            return;
        }
        if (ev.target.matches('[data-pmq-tick]')) { refresh(group); }
    });
    root.addEventListener('input', function (ev) {
        if (ev.target.matches('[data-pmq-note], [data-pmq-sha]')) { refresh(ev.target.closest('[data-pmq-group]')); }
    });
    root.querySelectorAll('[data-pmq-group]').forEach(refresh);
})();
