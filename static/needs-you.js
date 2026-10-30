// The Needs you list: pick ready gates and approve them in one go (one
// confirm listing each from → to), or one at a time; ▶ Run checks opens the
// issue's check scripts in tmux. Each project's gates go to its own
// …/needs-you/approve; a failed row says why and the others still go through.
(function () {
    'use strict';
    var dialog = document.querySelector('[data-ny-dialog]');
    var approveBtn = document.querySelector('[data-ny-approve]');
    var allBox = document.querySelector('[data-ny-all]');
    var pending = [];

    function picks() {
        return Array.prototype.slice.call(document.querySelectorAll('[data-ny-pick]'));
    }
    function rowOf(el) { return el.closest('[data-testid="needs_you.row"]'); }
    function say(row, text, ok) {
        var msg = row.querySelector('[data-ny-msg]');
        msg.textContent = text;
        msg.className = 'wb-ny-msg ' + (ok ? 'wb-ny-msg--ok' : 'wb-ny-msg--err');
    }
    function refresh() {
        var live = picks().filter(function (b) { return !b.disabled; });
        var n = live.filter(function (b) { return b.checked; }).length;
        if (approveBtn) {
            approveBtn.disabled = n === 0;
            approveBtn.querySelector('[data-ny-n]').textContent = n;
        }
        if (allBox) {
            allBox.checked = n > 0 && n === live.length;
            allBox.indeterminate = n > 0 && n < live.length;
        }
    }

    function confirmRows(rows) {
        if (!rows.length || !dialog) { return; }
        pending = rows;
        var list = dialog.querySelector('[data-ny-list]');
        list.textContent = '';
        rows.forEach(function (row) {
            var li = document.createElement('li');
            var b = document.createElement('b');
            b.textContent = row.dataset.slug;
            li.appendChild(b);
            li.appendChild(document.createTextNode(': ' + row.dataset.from + ' → ' + row.dataset.status));
            list.appendChild(li);
        });
        dialog.querySelector('[data-ny-dn]').textContent = rows.length === 1 ? '1 gate' : rows.length + ' gates';
        dialog.showModal();
    }

    function post(url, items, words) {
        return fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ items: items, words: words })
        }).then(function (r) {
            if (!r.ok) { return r.text().then(function (t) { throw new Error(t || r.statusText); }); }
            return r.json();
        });
    }

    function approve(rows, words) {
        // One request per project: the cross-project page posts to each.
        var byURL = {};
        rows.forEach(function (row) {
            var url = row.closest('[data-approve-url]').dataset.approveUrl;
            (byURL[url] = byURL[url] || []).push(row);
        });
        Object.keys(byURL).forEach(function (url) {
            var group = byURL[url];
            group.forEach(function (row) { say(row, 'Approving…', true); });
            var items = group.map(function (row) { return { slug: row.dataset.slug, status: row.dataset.status }; });
            post(url, items, words).then(function (res) {
                (res.results || []).forEach(function (r, i) {
                    var row = group[i];
                    if (!row) { return; }
                    if (r.ok) {
                        say(row, 'Approved for ' + r.status + ' ✓' + (r.words_error ? ' (your words were not saved: ' + r.words_error + ')' : ''), true);
                        row.classList.add('wb-ny-row--done');
                        row.dataset.done = '1';
                        var box = row.querySelector('[data-ny-pick]');
                        if (box) { box.checked = false; box.disabled = true; }
                        var one = row.querySelector('[data-ny-one]');
                        if (one) { one.disabled = true; }
                    } else {
                        say(row, 'Not approved: ' + r.error, false);
                    }
                });
                refresh();
            }).catch(function (err) {
                group.forEach(function (row) { say(row, 'Not approved: ' + err.message, false); });
            });
        });
    }

    document.addEventListener('change', function (e) {
        if (e.target.matches('[data-ny-all]')) {
            picks().forEach(function (b) { if (!b.disabled) { b.checked = e.target.checked; } });
        }
        if (e.target.matches('[data-ny-pick], [data-ny-all]')) { refresh(); }
    });

    document.addEventListener('click', function (e) {
        var t = e.target;
        if (t.closest('[data-ny-approve]')) {
            confirmRows(picks().filter(function (b) { return b.checked && !b.disabled; }).map(rowOf));
        } else if (t.closest('[data-ny-one]')) {
            confirmRows([rowOf(t)]);
        } else if (t.closest('[data-ny-cancel]')) {
            dialog.close();
        } else if (t.closest('[data-ny-confirm]')) {
            var words = dialog.querySelector('[data-ny-words]').value.trim();
            dialog.close();
            approve(pending, words);
            pending = [];
        } else if (t.closest('[data-ny-run]')) {
            var btn = t.closest('[data-ny-run]');
            var row = rowOf(btn);
            btn.disabled = true;
            say(row, 'Opening the check scripts…', true);
            fetch(btn.dataset.nyRun, { method: 'POST' }).then(function (r) { return r.json(); }).then(function (res) {
                var ok = res.status !== 'no-scripts' && res.status !== 'error';
                var text = ok ? 'Running in tmux window ' + res.session + (res.attach_cmd ? ': ' + res.attach_cmd : '') : ((res.steps && res.steps[0] && res.steps[0].name) || res.status);
                say(row, text, ok);
            }).catch(function (err) { say(row, err.message, false); }).then(function () { btn.disabled = false; });
        }
    });
    refresh();
})();
