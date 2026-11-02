// The post-mortem page: one round. Submit posts every open action's answer
// ({answers: [{id, agree, note}]}) to …/postmortem/<slug>/decide. Only the
// owner's browser passes (the server refuses curl and agents); the page
// reloads to show what was applied and refused.
(function () {
    'use strict';
    var root = document.querySelector('[data-pm-root]');
    var submit = document.querySelector('[data-pm-submit]');
    var status = document.querySelector('[data-pm-status]');
    if (!root || !submit) { return; }
    function say(text, ok) {
        status.textContent = text;
        status.className = 'wb-ny-msg ' + (ok ? 'wb-ny-msg--ok' : 'wb-ny-msg--err');
    }
    submit.addEventListener('click', function () {
        var answers = [];
        var missing = [];
        root.querySelectorAll('[data-pm-action]').forEach(function (row) {
            var agree = row.querySelector('[data-pm-agree]');
            var refuse = row.querySelector('[data-pm-refuse]');
            if (!agree) { return; }
            if (!agree.checked && !refuse.checked) { missing.push(row.dataset.id); return; }
            var note = row.querySelector('[data-pm-note]');
            answers.push({ id: row.dataset.id, agree: agree.checked, note: note ? note.value : '' });
        });
        if (missing.length) {
            say('One round answers every action: agree or refuse ' + missing.join(', ') + ' too.', false);
            return;
        }
        submit.disabled = true;
        say('Applying…', true);
        fetch(root.dataset.decideUrl, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
            body: JSON.stringify({ answers: answers })
        }).then(function (r) {
            return r.json().catch(function () { return { error: r.statusText }; }).then(function (j) { return { ok: r.ok, j: j }; });
        }).then(function (res) {
            if (!res.ok) {
                submit.disabled = false;
                say('Nothing applied: ' + (res.j.error || 'the server refused it') + '.', false);
                return;
            }
            (res.j.outcomes || []).forEach(function (o) {
                var row = root.querySelector('[data-pm-action][data-id="' + o.id + '"]');
                var msg = row && row.querySelector('[data-pm-msg]');
                if (!msg) { return; }
                msg.textContent = o.error ? 'Not applied: ' + o.error : (o.agreed ? 'Applied: ' : 'Refused: ') + o.line;
                msg.className = 'wb-ny-msg ' + (o.error ? 'wb-ny-msg--err' : 'wb-ny-msg--ok');
            });
            say(res.j.ok ? 'Done ✓ · the retro links each outcome' : 'Partly done: ' + (res.j.error || ''), !!res.j.ok);
            setTimeout(function () { window.location.reload(); }, 1500);
        }).catch(function (err) {
            submit.disabled = false;
            say('Nothing applied: ' + err + '.', false);
        });
    });
})();
