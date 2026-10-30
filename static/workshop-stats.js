// Hall stats (templates/stats_wb.html, stats_overview_wb.html). The page
// works without this file: every option is a link and every chart's table
// is a <details>. This adds the Copy buttons and remembers the last options
// per project, shared with the classic page (stats.js), in localStorage,
// which may throw.
(function () {
    document.querySelectorAll('[data-copy]').forEach(function (btn) {
        btn.addEventListener('click', function () {
            var text = btn.getAttribute('data-copy');
            var done = function () { btn.textContent = 'Copied'; setTimeout(function () { btn.textContent = 'Copy'; }, 1500); };
            if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, function () { btn.textContent = 'Select and copy'; });
        });
    });

    var page = document.querySelector('.wb-stats[data-project]');
    if (!page) return;
    var key = 'stats:' + page.getAttribute('data-project');
    var params = new URLSearchParams(location.search);
    params.delete('drill');
    params.delete('classic');
    try {
        if (!location.search) {
            var saved = localStorage.getItem(key);
            if (saved) location.replace(location.pathname + '?' + saved + location.hash);
        } else if (!params.has('days')) {
            localStorage.setItem(key, params.toString());
        }
    } catch (e) {
        // No storage (private window, blocked site data): options stay in
        // the URL only.
    }
})();
