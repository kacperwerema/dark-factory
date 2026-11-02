// Event bindings without inline handlers (F-12, docs/API/security.md).
//
// The page CSP allows only /static scripts and nonce'd inline blocks, so no
// template uses onclick= and friends. Instead an element carries:
//
//   data-click / data-change / data-input / data-keydown / data-blur
//       a JSON call, written by the onCall template func:
//       ["fnName", arg, …]. fnName is a global function of the page; an arg
//       of "$this" is the element, "$event" the event, "$value" its value.
//   data-stop        click stops propagation first (the old
//                    onclick="event.stopPropagation(); …").
//   data-autosubmit  change submits the element's form.
//
// Each element gets its own listener (not one on document), so the order of
// handlers and stopPropagation work as they did inline. Elements added later
// (innerHTML, templates cloned by scripts) are bound by a MutationObserver.
(function () {
  'use strict';
  var EVENTS = ['click', 'change', 'input', 'keydown', 'blur'];
  var SELECTOR = EVENTS.map(function (t) { return '[data-' + t + ']'; })
    .concat(['[data-stop]', '[data-autosubmit]']).join(',');
  var bound = new WeakMap();

  function arg(a, el, ev) {
    if (a === '$this') return el;
    if (a === '$event') return ev;
    if (a === '$value') return el.value;
    return a;
  }

  function run(el, type, ev) {
    if (type === 'click' && el.hasAttribute('data-stop')) ev.stopPropagation();
    if (type === 'change' && el.hasAttribute('data-autosubmit') && el.form) {
      el.form.submit();
      return;
    }
    var raw = el.getAttribute('data-' + type);
    if (!raw) return;
    var call;
    try { call = JSON.parse(raw); } catch (e) { return; }
    if (!Array.isArray(call) || typeof call[0] !== 'string') return;
    var fn = window[call[0]];
    if (typeof fn !== 'function') {
      if (window.console) console.error('events.js: no function ' + call[0]);
      return;
    }
    fn.apply(el, call.slice(1).map(function (a) { return arg(a, el, ev); }));
  }

  function bind(el) {
    var done = bound.get(el);
    if (!done) { done = {}; bound.set(el, done); }
    EVENTS.forEach(function (type) {
      if (done[type]) return;
      var wants = el.hasAttribute('data-' + type) ||
        (type === 'click' && el.hasAttribute('data-stop')) ||
        (type === 'change' && el.hasAttribute('data-autosubmit'));
      if (!wants) return;
      done[type] = true;
      el.addEventListener(type, function (ev) { run(el, type, ev); });
    });
  }

  function scan(node) {
    if (!node || node.nodeType !== 1) return;
    if (node.matches(SELECTOR)) bind(node);
    node.querySelectorAll(SELECTOR).forEach(bind);
  }

  // Small helpers for what used to be a statement in an inline handler.
  window.hideById = function (id) {
    var el = document.getElementById(id);
    if (el) el.style.display = 'none';
  };
  window.toggleOpenWithNext = function (el) {
    el.classList.toggle('open');
    if (el.nextElementSibling) el.nextElementSibling.classList.toggle('open');
  };

  // dataCall is for HTML a script builds as a string: the attribute that
  // calls call (["fnName", arg, …]) on type, HTML-escaped.
  window.dataCall = function (type, call) {
    return 'data-' + type + '="' + JSON.stringify(call).replace(/&/g, '&amp;')
      .replace(/"/g, '&quot;').replace(/</g, '&lt;') + '"';
  };

  scan(document.documentElement);
  new MutationObserver(function (records) {
    records.forEach(function (r) {
      if (r.type === 'attributes') { scan(r.target); return; }
      r.addedNodes.forEach(scan);
    });
  }).observe(document.documentElement, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: EVENTS.map(function (t) { return 'data-' + t; }).concat(['data-stop', 'data-autosubmit']),
  });
})();
