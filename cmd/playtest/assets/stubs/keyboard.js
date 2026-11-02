// Stub: a phone's on-screen keyboard. While a textarea or text input has
// focus, window.visualViewport is 340 px shorter than the window (the
// layout viewport keeps its size, as on iOS), its resize event fires, and a
// fixed 340 px block, data-testid="playtest.keyboard", covers the bottom.
(function () {
  var height = 340, up = false, block = null;
  var events = new EventTarget();
  var fake = {
    get height() { return window.innerHeight - (up ? height : 0); },
    get width() { return window.innerWidth; },
    offsetTop: 0, offsetLeft: 0, pageLeft: 0, scale: 1,
    get pageTop() { return window.scrollY; },
    addEventListener: events.addEventListener.bind(events),
    removeEventListener: events.removeEventListener.bind(events),
    dispatchEvent: events.dispatchEvent.bind(events)
  };
  Object.defineProperty(window, 'visualViewport', { get: function () { return fake; }, configurable: true });
  function typing(el) { return el && (el.tagName === 'TEXTAREA' || (el.tagName === 'INPUT' && /^(text|search|email|url)?$/.test(el.type))); }
  function show(on) {
    if (on === up) return;
    up = on;
    if (!block) {
      block = document.createElement('div');
      block.setAttribute('data-testid', 'playtest.keyboard');
      block.style.cssText = 'position:fixed;left:0;right:0;bottom:0;height:' + height + 'px;background:#CDD2DA;z-index:2147483647;pointer-events:none';
    }
    if (on) document.body.appendChild(block); else block.remove();
    events.dispatchEvent(new Event('resize'));
  }
  document.addEventListener('focusin', function (e) { if (typing(e.target)) show(true); });
  document.addEventListener('focusout', function (e) { if (typing(e.target)) show(false); });
})();
