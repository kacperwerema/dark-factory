// The review after YOLO (/p/<project>/yolo/review): one entry per issue.
// The issue's Agree (all its gates) and Back to post {slug, verdict, words}
// to …/yolo/review; an owner box's Tick posts to …/owner-checks/tick and
// Agree without it to the station's …/issue/<slug>/yolo-review. Only the
// owner's browser passes; a refused or failed click says why and nothing
// changes. Back moves the issue, so the page reloads after it.
(function () {
    'use strict';
    var root = document.querySelector('[data-issue-url]');
    if (!root) { return; }

    function say(el, text, ok) {
        el.textContent = text;
        el.className = 'wb-ny-msg ' + (ok ? 'wb-ny-msg--ok' : 'wb-ny-msg--err');
    }

    function post(url, body) {
        return fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        }).then(function (r) {
            return r.json().catch(function () { return { error: r.statusText }; })
                .then(function (j) { return { ok: r.ok, j: j }; });
        }).catch(function (err) {
            return { ok: false, j: { error: err.message } };
        });
    }

    // done folds the issue's card once nothing in it waits.
    function done(group) {
        var gatesLeft = group.querySelectorAll('[data-yr-gate]:not(.wb-yr-item--done)').length;
        var checksLeft = group.querySelectorAll('[data-yr-check]:not(.wb-yr-item--done)').length;
        if (gatesLeft === 0) {
            var agree = group.querySelector('[data-yr-issue="agree"]');
            if (agree) { agree.hidden = true; }
        }
        if (gatesLeft === 0 && checksLeft === 0) {
            group.classList.add('wb-yr-group--done');
            say(group.querySelector('.wb-yr-foot [data-yr-msg]'), 'All reviewed ✓', true);
        }
    }

    root.addEventListener('click', function (ev) {
        var btn = ev.target.closest('[data-yr-issue], [data-yr-check-act]');
        if (!btn || btn.disabled) { return; }
        var group = btn.closest('[data-yr-group]');
        var words = group.querySelector('[data-yr-words]');
        var text = words.value.trim();

        if (btn.hasAttribute('data-yr-issue')) {
            var verdict = btn.dataset.yrIssue;
            var msg = group.querySelector('.wb-yr-foot [data-yr-msg]');
            if (verdict === 'back' && !text) {
                say(msg, 'Say why it goes back in Your words: they reach the agent.', false);
                words.focus();
                return;
            }
            btn.disabled = true;
            post(root.dataset.issueUrl, { slug: group.dataset.slug, verdict: verdict, words: text }).then(function (res) {
                btn.disabled = false;
                if (!res.ok) {
                    say(msg, 'Not recorded: ' + (res.j.error || 'the server refused it') + '.', false);
                    return;
                }
                if (verdict === 'back') {
                    say(msg, 'Sent back to ' + res.j.to + ' ✓', true);
                    location.reload();
                    return;
                }
                group.querySelectorAll('[data-yr-gate]').forEach(function (g) { g.classList.add('wb-yr-item--done'); });
                say(msg, 'Agreed ✓' + (res.j.checks_left ? ' · ' + res.j.checks_left + ' owner check(s) still yours' : ''), true);
                done(group);
            });
            return;
        }

        var item = btn.closest('[data-yr-check]');
        var itemMsg = item.querySelector('[data-yr-msg]');
        var act = btn.dataset.yrCheckAct;
        var req = act === 'tick'
            ? post(root.dataset.tickUrl, { slug: group.dataset.slug, id: item.dataset.key, text: item.dataset.text })
            : post(group.dataset.review, { status: item.dataset.key, verdict: 'agree', words: text });
        btn.disabled = true;
        req.then(function (res) {
            btn.disabled = false;
            if (!res.ok) {
                say(itemMsg, 'Not recorded: ' + (res.j.error || 'the server refused it') + '.', false);
                return;
            }
            item.classList.add('wb-yr-item--done');
            say(itemMsg, act === 'tick' ? 'Ticked by you ✓' : 'Agreed without it ✓', true);
            done(group);
        });
    });
}());
