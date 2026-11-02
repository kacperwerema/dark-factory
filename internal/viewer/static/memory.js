// The Memory page (/p/<project>/memory): ask before a Delete is sent.
document.addEventListener('click', function (e) {
  var b = e.target.closest('[data-mem-delete]');
  if (!b) return;
  var row = b.closest('[id]');
  if (!window.confirm('Delete memory ' + (row ? row.id : '') + '? Agents will no longer see it.')) e.preventDefault();
});
