// YOLO mode in the sidebar: Extend / Turn off now. The after-the-fact queue
// is reviewed on /p/<project>/yolo/review (static/yolo-review.js).
(function () {
  const box = document.querySelector('[data-testid="nav.yolo"]');
  if (!box || box.dataset.wired) return;
  box.dataset.wired = '1';
  const prefix = box.dataset.prefix;
  async function post(path, body) {
    const res = await fetch(prefix + path, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body || {}),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      alert(data.error || res.statusText);
      return;
    }
    location.reload();
  }
  box.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-yolo]');
    if (!btn) return;
    const kind = btn.dataset.yolo;
    if (kind === 'off') {
      post('/yolo/off');
    } else if (kind === 'extend') {
      const until = prompt('Extend YOLO until (08:00, 4h, or off = until turned off):', '4h');
      if (until) post('/yolo/extend', {until});
    }
  });
})();
