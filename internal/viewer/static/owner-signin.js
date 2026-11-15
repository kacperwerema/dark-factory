// Owner sign-in in place (docs/API/security.md, "Owner sign-in").
//
// An owner decision the viewer refuses because this browser isn't signed in
// comes back 403 with the X-Owner-Sign-In header (owner.go refuseNonOwner).
// Instead of printing that refusal, every page's fetch opens a small sign-in
// dialog here: Send me a code (POST /owner/code: the viewer shows the code in
// a dialog on the Mac or its terminal, never in the answer), type it, Sign in
// (POST /owner/pair sets the HttpOnly cookie), and the refused request is sent
// again; its answer goes to the page as if nothing happened. Cancel hands the
// page the original refusal, which it shows as before. Agents and issue-cli
// never run this script: they keep the refusal's text.
(function () {
    'use strict';
    if (!window.fetch || window.__ownerSignIn) { return; }
    var plainFetch = window.fetch.bind(window);
    var pending = null; // one dialog for every request refused at once

    function el(tag, attrs, text) {
        var n = document.createElement(tag);
        Object.keys(attrs || {}).forEach(function (k) {
            if (k === 'style') { Object.assign(n.style, attrs[k]); } else { n.setAttribute(k, attrs[k]); }
        });
        if (text) { n.textContent = text; }
        return n;
    }

    // ask shows the dialog; it resolves true once this browser is signed in,
    // false when the owner cancels.
    function ask() {
        if (pending) { return pending; }
        pending = new Promise(function (resolve) {
            var dlg = el('dialog', { 'data-testid': 'owner_signin.dialog', 'aria-labelledby': 'owner-signin-title', style: {
                maxWidth: '26rem', width: 'calc(100% - 32px)', padding: '20px', borderRadius: '10px',
                border: '1px solid #8886', background: 'Canvas', color: 'CanvasText', font: '14px/1.45 system-ui, sans-serif'
            } });
            var title = el('h2', { id: 'owner-signin-title', style: { margin: '0 0 8px', fontSize: '17px' } }, 'Sign in as the owner');
            var why = el('p', { style: { margin: '0 0 12px' } }, 'Only the owner records this, and this browser isn’t signed in (a viewer restart before this fix signed it out). Ask for a one-time code: the viewer shows it on this Mac, in a dialog or its terminal.');
            var send = el('button', { type: 'button', 'data-testid': 'owner_signin.send' }, 'Send me a code');
            var form = el('form', { style: { display: 'flex', gap: '8px', margin: '12px 0 0', flexWrap: 'wrap' } });
            var code = el('input', { type: 'text', inputmode: 'numeric', autocomplete: 'one-time-code', placeholder: '1234-5678', 'aria-label': 'Code', 'data-testid': 'owner_signin.code', style: { flex: '1 1 8rem', font: 'inherit', padding: '4px 8px' } });
            var go = el('button', { type: 'submit', 'data-testid': 'owner_signin.submit' }, 'Sign in');
            var cancel = el('button', { type: 'button', 'data-testid': 'owner_signin.cancel' }, 'Cancel');
            var msg = el('p', { role: 'status', 'data-testid': 'owner_signin.msg', style: { margin: '10px 0 0', minHeight: '1.4em' } });
            form.append(code, go, cancel);
            dlg.append(title, why, send, form, msg);
            document.body.appendChild(dlg);

            var done = function (ok) {
                dlg.close();
                dlg.remove();
                pending = null;
                resolve(ok);
            };
            var say = function (text) { msg.textContent = text; };

            send.addEventListener('click', function () {
                send.disabled = true;
                plainFetch('/owner/code', { method: 'POST', credentials: 'same-origin' }).then(function (res) {
                    var q = new URL(res.url, location.href).searchParams;
                    if (q.get('sent')) { say('Code shown in ' + q.get('sent') + '. Type it here within 5 minutes.'); }
                    else if (q.get('wait')) { say('A code was sent less than 30 seconds ago: use that one.'); }
                    else { say('The viewer could not show a code. Check the viewer’s terminal.'); }
                    code.focus();
                }).catch(function () { say('The viewer did not answer.'); }).finally(function () {
                    setTimeout(function () { send.disabled = false; }, 30000);
                });
            });
            form.addEventListener('submit', function (ev) {
                ev.preventDefault();
                go.disabled = true;
                var body = new URLSearchParams();
                body.set('code', code.value);
                plainFetch('/owner/pair', { method: 'POST', body: body, credentials: 'same-origin', headers: { 'Accept': 'application/json' } }).then(function (res) {
                    go.disabled = false;
                    if (res.ok) { done(true); return; }
                    say('That code is wrong, expired or used. Ask for a new one.');
                    code.select();
                }).catch(function () { go.disabled = false; say('The viewer did not answer.'); });
            });
            cancel.addEventListener('click', function () { done(false); });
            dlg.addEventListener('cancel', function (ev) { ev.preventDefault(); done(false); });
            dlg.showModal();
            send.focus();
        });
        return pending;
    }

    window.fetch = function (input, init) {
        // A Request's body can be read once: keep a copy to send again.
        var again = (typeof Request !== 'undefined' && input instanceof Request) ? input.clone() : input;
        return plainFetch(input, init).then(function (res) {
            if (res.status !== 403 || !res.headers.get('X-Owner-Sign-In')) { return res; }
            return ask().then(function (ok) { return ok ? plainFetch(again, init) : res; });
        });
    };
    window.__ownerSignIn = ask;
}());
