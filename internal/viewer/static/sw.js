// The viewer's service worker (push_view.go serves it at /sw.js, scope /).
// It shows Web Push notifications and opens their link on a tap. It caches
// nothing: every page comes from the viewer, never a stale copy.
'use strict';

// sameOriginPath keeps a link only when it is a path on this viewer.
function sameOriginPath(url) {
  if (typeof url !== 'string' || url.charAt(0) !== '/' || url.charAt(1) === '/' || url.indexOf('\\') >= 0) {
    return '/needs-you';
  }
  try {
    var u = new URL(url, self.location.origin);
    if (u.origin !== self.location.origin) return '/needs-you';
    return u.pathname + u.search + u.hash;
  } catch (e) {
    return '/needs-you';
  }
}

self.addEventListener('install', function () { self.skipWaiting(); });
self.addEventListener('activate', function (event) { event.waitUntil(self.clients.claim()); });

self.addEventListener('push', function (event) {
  var data = {};
  try { data = event.data ? event.data.json() : {}; } catch (e) { data = {}; }
  var title = data.title || 'Something waits on you';
  var opts = {
    body: data.project || '',
    tag: data.tag || undefined,
    icon: '/static/icons/icon-192.png',
    badge: '/static/icons/icon-192.png',
    data: { url: sameOriginPath(data.url) }
  };
  event.waitUntil(self.registration.showNotification(title, opts));
});

self.addEventListener('notificationclick', function (event) {
  event.notification.close();
  var path = sameOriginPath(event.notification.data && event.notification.data.url);
  var target = new URL(path, self.location.origin).href;
  event.waitUntil(self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function (list) {
    for (var i = 0; i < list.length; i++) {
      if (list[i].url === target && 'focus' in list[i]) return list[i].focus();
    }
    return self.clients.openWindow(path);
  }));
});

// The browser rotated the subscription: subscribe again with the same key
// and tell the viewer, which keeps the device's history (owner-only route;
// it fails when the owner's session has ended, and /push shows the device
// as removed once the push service drops the old one).
self.addEventListener('pushsubscriptionchange', function (event) {
  var old = event.oldSubscription;
  event.waitUntil(fetch('/push/key', { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (k) {
    if (!k.key) return;
    return self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: k.key }).then(function (sub) {
      var body = sub.toJSON();
      if (old) body.old_endpoint = old.endpoint;
      return fetch('/push/change', {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
    });
  }).catch(function () {}));
});
