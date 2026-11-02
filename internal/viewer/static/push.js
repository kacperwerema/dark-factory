// Phone notifications on /push: register the service worker, subscribe this
// browser with the viewer's key, unsubscribe, send a test.
(function () {
  'use strict';

  function supported() {
    return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
  }

  function register() {
    if (!('serviceWorker' in navigator)) return Promise.resolve(null);
    return navigator.serviceWorker.register('/sw.js', { scope: '/' });
  }
  window.pushRegister = register;

  // deviceId is the viewer's id for an endpoint: base64url(sha256)[:16].
  function deviceId(endpoint) {
    var data = new TextEncoder().encode(endpoint);
    return crypto.subtle.digest('SHA-256', data).then(function (buf) {
      var bytes = new Uint8Array(buf);
      var s = '';
      for (var i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
      return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '').slice(0, 16);
    });
  }

  function say(text, bad) {
    var el = document.querySelector('[data-testid="push.status"]');
    if (!el) return;
    el.textContent = text;
    el.classList.toggle('wb-owner-note--err', !!bad);
  }

  function post(path, body) {
    return fetch(path, {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify(body)
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (!r.ok) throw new Error(j.error || ('the viewer answered ' + r.status));
        return j;
      });
    });
  }

  function current() {
    return register().then(function (reg) {
      if (!reg) return null;
      return navigator.serviceWorker.ready.then(function (r) { return r.pushManager.getSubscription(); });
    });
  }

  function refresh() {
    var sub = document.querySelector('[data-testid="push.subscribe"]');
    var unsub = document.querySelector('[data-testid="push.unsubscribe"]');
    var test = document.querySelector('[data-testid="push.test"]');
    if (!supported()) {
      if (sub) sub.disabled = true;
      say(/iPhone|iPad/.test(navigator.userAgent)
        ? 'On iPhone and iPad, add this page to the Home Screen (Share → Add to Home Screen), open it from there, and subscribe in that app.'
        : 'This browser has no Web Push.', true);
      return;
    }
    current().then(function (s) {
      var on = !!s;
      if (sub) sub.hidden = on;
      if (unsub) unsub.hidden = !on;
      if (test) test.hidden = !on;
      if (!on) { say('This browser is not subscribed.'); return; }
      return deviceId(s.endpoint).then(function (id) {
        var row = document.querySelector('[data-device="' + id + '"]');
        if (row) {
          row.classList.add('wb-push-device--this');
          say('This browser is subscribed (' + row.getAttribute('data-label') + ').');
        } else {
          say('This browser has a subscription the viewer doesn\'t know: subscribe again.', true);
          if (sub) sub.hidden = false;
        }
      });
    }).catch(function (e) { say(e.message, true); });
  }

  window.pushSubscribe = function () {
    var key = document.body.getAttribute('data-push-key');
    if (!key) { say('This viewer can\'t send notifications now: see above.', true); return; }
    say('Asking this browser…');
    Notification.requestPermission().then(function (perm) {
      if (perm !== 'granted') throw new Error('Notifications are blocked for this site in the browser settings.');
      return register().then(function () { return navigator.serviceWorker.ready; });
    }).then(function (reg) {
      return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
    }).then(function (s) {
      return post('/push/subscribe', s.toJSON());
    }).then(function () {
      location.reload();
    }).catch(function (e) { say(e.message, true); });
  };

  window.pushUnsubscribe = function () {
    current().then(function (s) {
      if (!s) return;
      return post('/push/unsubscribe', { endpoint: s.endpoint }).then(function () { return s.unsubscribe(); });
    }).then(function () { location.reload(); }).catch(function (e) { say(e.message, true); });
  };

  window.pushTest = function () {
    current().then(function (s) {
      if (!s) throw new Error('This browser is not subscribed.');
      return post('/push/test', { endpoint: s.endpoint });
    }).then(function (j) {
      say(j.outcome === 'sent' ? 'Test sent: it should show in a few seconds.' : 'The push service said ' + j.outcome + (j.why ? ': ' + j.why : '') + '.', j.outcome !== 'sent');
    }).catch(function (e) { say(e.message, true); });
  };

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', refresh);
  else refresh();
})();
