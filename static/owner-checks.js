// The Owner checks page: the epic select reloads the page with ?epic=, and
// a box's tick posts {slug, id, text} to …/owner-checks/tick. Only the
// owner's browser passes (the server refuses curl and agents); a refused
// tick says why in the row and nothing changes.
(function () {
    'use strict';
    var root = document.querySelector('[data-tick-url]');
    var select = document.querySelector('[data-oc-epic]');
    if (select) {
        select.addEventListener('change', function () { select.form.submit(); });
    }
    if (!root) { return; }
    function say(box, text, ok) {
        var msg = box.querySelector('[data-oc-msg]');
        msg.textContent = text;
        msg.className = 'wb-ny-msg ' + (ok ? 'wb-ny-msg--ok' : 'wb-ny-msg--err');
    }
    root.addEventListener('click', function (ev) {
        var btn = ev.target.closest('[data-oc-tick]');
        if (!btn || btn.disabled) { return; }
        var box = btn.closest('[data-oc-box]');
        var issue = btn.closest('[data-oc-issue]');
        btn.disabled = true;
        say(box, 'Ticking…', true);
        fetch(root.dataset.tickUrl, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ slug: issue.dataset.slug, id: box.dataset.id, text: box.dataset.text })
        }).then(function (r) {
            return r.json().catch(function () { return { error: r.statusText }; }).then(function (j) { return { ok: r.ok, j: j }; });
        }).then(function (res) {
            if (!res.ok || !res.j.ok) {
                btn.disabled = false;
                say(box, 'Not ticked: ' + (res.j.error || 'the server refused it') + '.', false);
                return;
            }
            box.classList.add('wb-oc-box--done');
            btn.textContent = '✓';
            say(box, 'Ticked by you ✓' + (res.j.comment_error ? ' (the comment was not saved: ' + res.j.comment_error + ')' : ' · comment added to the issue'), true);
        }).catch(function (err) {
            btn.disabled = false;
            say(box, 'Not ticked: ' + err.message, false);
        });
    });
}());
