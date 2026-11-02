// Stub: a fake SpeechRecognition (and webkitSpeechRecognition). start()
// hears "status" (interim), then "status please" (final); stop() or abort()
// delivers one more final result, "late words", 200 ms later, then onend,
// as a real engine may. It also counts the page's non-GET fetches to a
// /lead/ route in a hidden element, data-testid="playtest.lead_posts".
(function () {
  function result(text, final) {
    var r = [{ transcript: text, confidence: 0.9 }];
    r.isFinal = final;
    return r;
  }
  function Fake() { this.lang = ''; this.continuous = false; this.interimResults = false; this.live = false; }
  Fake.prototype.start = function () {
    var self = this;
    if (self.live) throw new Error('already started');
    self.live = true;
    window.__speechLang = self.lang;
    setTimeout(function () { if (self.live && self.onresult) self.onresult({ resultIndex: 0, results: [result('status', false)] }); }, 100);
    setTimeout(function () { if (self.live && self.onresult) self.onresult({ resultIndex: 0, results: [result('status please', true)] }); }, 300);
  };
  Fake.prototype.finish = function () {
    var self = this;
    if (!self.live) return;
    self.live = false;
    setTimeout(function () {
      if (self.onresult) self.onresult({ resultIndex: 1, results: [result('status please', true), result('late words', true)] });
      setTimeout(function () { if (self.onend) self.onend(); }, 50);
    }, 200);
  };
  Fake.prototype.stop = function () { this.finish(); };
  Fake.prototype.abort = function () { this.finish(); };
  window.SpeechRecognition = Fake;
  window.webkitSpeechRecognition = Fake;

  var posts = 0, el = null;
  function show() { if (el) el.textContent = String(posts); }
  var realFetch = window.fetch;
  window.fetch = function (input, init) {
    var url = typeof input === 'string' ? input : (input && input.url) || '';
    var method = (init && init.method) || (input && input.method) || 'GET';
    if (method.toUpperCase() !== 'GET' && url.indexOf('/lead/') >= 0) { posts++; show(); }
    return realFetch.apply(this, arguments);
  };
  document.addEventListener('DOMContentLoaded', function () {
    el = document.createElement('span');
    el.setAttribute('data-testid', 'playtest.lead_posts');
    el.hidden = true;
    document.body.appendChild(el);
    show();
  });
})();
