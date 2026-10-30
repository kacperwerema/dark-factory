// A station's Edit form (templates/hall_edit.html): add, move and remove
// action rows, show the fields of each row's type, and keep Confirm from
// saving a form that changed since its preview. Without JS the form still
// posts every field; only the row tools need it.
(function () {
  var form = document.querySelector('[data-edit-form]');
  if (!form) return;

  // showFields hides the fields the row's type doesn't read.
  function showFields(row) {
    var type = row.querySelector('[data-edit-type]').value;
    row.querySelectorAll('[data-for]').forEach(function (label) {
      label.hidden = label.getAttribute('data-for').split(' ').indexOf(type) < 0;
    });
  }
  form.querySelectorAll('[data-edit-row]').forEach(showFields);

  form.addEventListener('change', function (e) {
    if (e.target.matches('[data-edit-type]')) showFields(e.target.closest('[data-edit-row]'));
  });

  form.addEventListener('click', function (e) {
    var btn = e.target.closest('button');
    if (!btn) return;
    var row = btn.closest('[data-edit-row]');
    if (btn.hasAttribute('data-edit-add')) {
      var edge = btn.closest('[data-edit-edge]');
      var blank = edge.querySelector('template[data-edit-blank]').content.firstElementChild.cloneNode(true);
      edge.querySelector('[data-edit-rows]').appendChild(blank);
      showFields(blank);
      blank.querySelector('[data-edit-type]').focus();
    } else if (btn.hasAttribute('data-edit-remove')) {
      row.remove();
    } else if (btn.hasAttribute('data-edit-up') && row.previousElementSibling) {
      row.parentNode.insertBefore(row, row.previousElementSibling);
      btn.focus();
    } else if (btn.hasAttribute('data-edit-down') && row.nextElementSibling) {
      row.parentNode.insertBefore(row.nextElementSibling, row);
      btn.focus();
    } else {
      return;
    }
    stale();
  });

  // Any change after the preview: Confirm would save something else than
  // the diff shows, so it waits for a new preview. The reason is not part
  // of the diff and may change.
  var confirm = form.querySelector('[data-edit-confirm]');
  function stale() {
    if (!confirm) return;
    confirm.disabled = true;
    var note = form.querySelector('[data-edit-stale]');
    if (note) note.hidden = false;
  }
  form.addEventListener('input', function (e) {
    if (e.target.name !== 'reason') stale();
  });
})();
