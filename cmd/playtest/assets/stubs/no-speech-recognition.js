// Stub: a browser without speech recognition (Firefox): both constructors
// are gone.
(function () {
  ['SpeechRecognition', 'webkitSpeechRecognition'].forEach(function (name) {
    try { Object.defineProperty(window, name, { value: undefined, configurable: true, writable: true }); } catch (e) {}
  });
})();
