// The Needs you list: pick ready gates and approve them in one go (one
// confirm listing each from → to), or one at a time in one tap (no
// confirm); ▶ Play and ▶ Run checks open the issue's playtest or check
// scripts in tmux. Each project's gates go to its own …/needs-you/approve;
// a failed row says why and the others still go through. A clarify option
// answers its question (…/issue/<slug>/clarify, the Station's route), Reply
// posts the owner's comment under an open question, and Agree settles a
// YOLO entry (…/yolo/review). Each route keeps its own owner check.
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

    function postJSON(url, body) {
        return fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        }).then(function (r) {
            return r.text().then(function (t) {
                var data = null;
                try { data = t ? JSON.parse(t) : {}; } catch (e) { data = null; }
                if (!r.ok) {
                    var why = (data && (data.error || (data.results && data.results[0] && data.results[0].error))) || t || r.statusText;
                    throw new Error(why);
                }
                return data || {};
            });
        });
    }
    function post(url, items, words) {
        return postJSON(url, { items: items, words: words });
    }
    function afterRowOf(el) { return el.closest('[data-testid="needs_you.after.row"]'); }
    function sayIn(el, text, ok) {
        el.textContent = text;
        el.className = 'wb-ny-msg ' + (ok ? 'wb-ny-msg--ok' : 'wb-ny-msg--err');
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
                        var again = row.querySelector('[data-ny-one]');
                        if (again) { again.disabled = false; }
                    }
                });
                refresh();
            }).catch(function (err) {
                group.forEach(function (row) {
                    say(row, 'Not approved: ' + err.message, false);
                    var again = row.querySelector('[data-ny-one]');
                    if (again) { again.disabled = false; }
                });
            });
        });
    }

    // A clarify option answers its question with the option the owner
    // tapped; the words are its label marked as a tap, so the decision
    // never reads as words the owner typed.
    function answerClarify(btn) {
        var q = btn.closest('[data-qid]');
        var url = btn.closest('[data-clarify-url]').dataset.clarifyUrl;
        var msg = q.querySelector('[data-ny-qmsg]');
        var opts = q.querySelectorAll('[data-ny-opt]');
        opts.forEach(function (b) { b.disabled = true; });
        sayIn(msg, 'Answering…', true);
        var answer = { id: q.dataset.qid, option: parseInt(btn.dataset.nyOpt, 10), text: q.dataset.qtext, words: btn.dataset.label + ' (tapped on Needs you)' };
        postJSON(url, { answers: [answer], from: 'needs-you' }).then(function (res) {
            var r = (res.results || [])[0] || {};
            if (r.error) { throw new Error(r.error); }
            sayIn(msg, 'Answered: ' + btn.dataset.label + (r.decision ? ' (' + r.decision + ')' : '') + ' ✓', true);
            btn.classList.add('wb-ny-opt--chosen');
            q.dataset.done = '1';
            var row = rowOf(q);
            if (row.querySelectorAll('[data-qid]').length === row.querySelectorAll('[data-qid][data-done]').length) {
                row.classList.add('wb-ny-row--done');
            }
        }).catch(function (err) {
            sayIn(msg, 'Not answered: ' + err.message, false);
            opts.forEach(function (b) { b.disabled = false; });
        });
    }

    // Agree on a YOLO entry: every passed gate and assumed answer of the
    // issue; its owner checks stay for their own page.
    function agree(btn) {
        var row = afterRowOf(btn);
        var url = btn.closest('[data-yolo-url]').dataset.yoloUrl;
        var msg = row.querySelector('[data-ny-msg]');
        btn.disabled = true;
        sayIn(msg, 'Agreeing…', true);
        postJSON(url, { slug: row.dataset.slug, verdict: 'agree' }).then(function (res) {
            sayIn(msg, 'Agreed: ' + res.settled + ' settled' + (res.checks_left ? ', ' + res.checks_left + ' owner check' + (res.checks_left === 1 ? '' : 's') + ' left' : '') + ' ✓', true);
            row.classList.add('wb-ny-row--done');
        }).catch(function (err) {
            sayIn(msg, 'Not agreed: ' + err.message, false);
            btn.disabled = false;
        });
    }

    // An expired hard decision: Renew (with its date) or Let go, the
    // owner's words optional.
    document.addEventListener('submit', function (e) {
        var form = e.target.closest('[data-ny-expiry]');
        if (!form) { return; }
        e.preventDefault();
        var row = rowOf(form);
        var act = (e.submitter && e.submitter.value) || 'renew';
        var body = { text: form.querySelector('[data-ny-expiry-text]').value.trim() };
        if (act === 'renew') {
            body.expires = form.querySelector('[data-ny-expires]').value;
            if (!body.expires) { say(row, 'Pick the new last day first.', false); return; }
        }
        var buttons = form.querySelectorAll('button');
        buttons.forEach(function (b) { b.disabled = true; });
        say(row, act === 'renew' ? 'Renewing…' : 'Letting go…', true);
        postJSON(form.dataset.nyExpiry + '/' + act, body).then(function (res) {
            say(row, act === 'renew' ? 'Renewed until ' + res.expires + ' ✓' : 'Let go ✓', true);
            form.hidden = true;
            row.classList.add('wb-ny-row--done');
        }).catch(function (err) {
            say(row, (act === 'renew' ? 'Not renewed: ' : 'Not let go: ') + err.message, false);
            buttons.forEach(function (b) { b.disabled = false; });
        });
    });

    document.addEventListener('submit', function (e) {
        var form = e.target.closest('[data-ny-reply-form]');
        if (!form) { return; }
        e.preventDefault();
        var row = rowOf(form);
        var box = form.querySelector('[data-ny-reply-text]');
        var text = box.value.trim();
        if (!text) { say(row, 'Write your reply first.', false); return; }
        var send = form.querySelector('[type=submit]');
        send.disabled = true;
        say(row, 'Sending…', true);
        postJSON(form.dataset.nyReplyForm, { text: text }).then(function () {
            say(row, 'Replied ✓', true);
            form.hidden = true;
            row.classList.add('wb-ny-row--done');
            var rb = row.querySelector('[data-ny-reply]');
            if (rb) { rb.disabled = true; }
        }).catch(function (err) {
            say(row, 'Not sent: ' + err.message, false);
            send.disabled = false;
        });
    });

    // Keep it on a challenge row: the owner keeps the decision, closing its
    // open challenges (…/decisions/challenge/resolve, owner-only).
    document.addEventListener('submit', function (e) {
        var form = e.target.closest('[data-ny-resolve-form]');
        if (!form) { return; }
        e.preventDefault();
        var row = rowOf(form);
        var text = form.querySelector('[data-ny-resolve-text]').value.trim();
        var send = form.querySelector('[type=submit]');
        send.disabled = true;
        say(row, 'Keeping…', true);
        postJSON(form.dataset.nyResolveForm, { ref: form.dataset.ref, issue: row.dataset.slug, text: text }).then(function (res) {
            say(row, 'Kept ' + res.ref + ': ' + res.resolved + ' challenge' + (res.resolved === 1 ? '' : 's') + ' resolved ✓', true);
            form.hidden = true;
            row.classList.add('wb-ny-row--done');
            var rb = row.querySelector('[data-ny-resolve]');
            if (rb) { rb.disabled = true; }
        }).catch(function (err) {
            say(row, 'Not kept: ' + err.message, false);
            send.disabled = false;
        });
    });

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
            // One gate, one tap: the row says what happened.
            var oneBtn = t.closest('[data-ny-one]');
            oneBtn.disabled = true;
            approve([rowOf(t)], '');
        } else if (t.closest('[data-ny-opt]')) {
            answerClarify(t.closest('[data-ny-opt]'));
        } else if (t.closest('[data-ny-resolve]')) {
            var kb = t.closest('[data-ny-resolve]');
            var kf = rowOf(kb).querySelector('[data-ny-resolve-form]');
            kf.hidden = !kf.hidden;
            kb.setAttribute('aria-expanded', kf.hidden ? 'false' : 'true');
            if (!kf.hidden) { kf.querySelector('[data-ny-resolve-text]').focus(); }
        } else if (t.closest('[data-ny-reply]')) {
            var rb = t.closest('[data-ny-reply]');
            var form = rowOf(rb).querySelector('[data-ny-reply-form]');
            form.hidden = !form.hidden;
            rb.setAttribute('aria-expanded', String(!form.hidden));
            if (!form.hidden) { form.querySelector('[data-ny-reply-text]').focus(); }
        } else if (t.closest('[data-ny-agree]')) {
            agree(t.closest('[data-ny-agree]'));
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
            say(row, 'Opening it in tmux…', true);
            var ok = false;
            fetch(btn.dataset.nyRun, { method: 'POST' }).then(function (r) { ok = r.ok; return r.json(); }).then(function (res) {
                var text = ok ? 'Running in tmux window ' + res.session + (res.attach_cmd ? ': ' + res.attach_cmd : '') : ((res.steps && res.steps[0] && res.steps[0].name) || res.status);
                say(row, text, ok);
            }).catch(function (err) { say(row, err.message, false); }).then(function () { btn.disabled = false; });
        }
    });
    refresh();
})();
