#!/usr/bin/env bash
# issue: cli/page-check-skips-sitemap-imageloc-urls-instead-of-fetching-them-so-a-big-sitemap-costs-one-get-per-page
source "${CHECKS_LIB:?run me with: issue-cli check-script <slug>}"
checks_intro "page-check reads only each <url>'s <loc> from a sitemap, never image:loc or video:loc. About 1 minute."

ROOT="$(git rev-parse --show-toplevel)"
SITE="$(mktemp -d)"
trap 'kill "${SRV_PID:-0}" 2>/dev/null; rm -rf "${SITE:?}"' EXIT
PORT="$(python3 -I -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')"
B="http://127.0.0.1:$PORT"
for p in a b c; do printf '<!doctype html><html><head><meta charset="utf-8"><title>Page %s</title></head></html>' "$p" > "$SITE/$p.html"; done
cat > "$SITE/sitemap.xml" <<XML
<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:image="http://www.google.com/schemas/sitemap-image/1.1" xmlns:video="http://www.google.com/schemas/sitemap-video/1.1">
<url><loc>$B/a.html</loc><image:image><image:loc>$B/one.jpg</image:loc></image:image><image:image><image:loc>$B/two.jpg</image:loc></image:image></url>
<url><loc>$B/b.html</loc><video:video><video:loc>$B/v.mp4</video:loc><video:content_loc>$B/c.mp4</video:content_loc></video:video></url>
<url><loc>$B/c.html</loc></url>
</urlset>
XML
python3 -I -m http.server "$PORT" --bind 127.0.0.1 --directory "$SITE" >"$SITE/access.log" 2>&1 &
SRV_PID=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do curl -sf "$B/c.html" >/dev/null && break; sleep 0.3; done
: > "$SITE/access.log"

step PC1 "A sitemap with image and video entries costs one GET per page" --box TP2 \
  --what "page-check on a local sitemap.xml: 3 pages, 2 image:loc under the first, a video:loc and video:content_loc under the second" \
  --why "page-check read every element named loc, so it fetched each image and video too: double the GETs and --max used up on media" \
  --look "the sitemap line says 3 URL(s); only a.html, b.html, c.html are listed; the server log has 4 GETs, none for .jpg or .mp4" \
  --expect "4 results, no .jpg or .mp4 among them or in the server log"
run "${ISSUE_CLI:-issue-cli}" page-check "$B/sitemap.xml" --json
expect 'grep -q "sitemap: 3 URL(s)" <<<"$out"' "the sitemap expands to 3 URLs"
expect '! grep -qE "\.(jpg|mp4)" <<<"$out"' "no image or video URL is checked"
expect '[ "$(grep -c "\"GET " "$SITE/access.log")" = 4 ] && ! grep -qE "\.(jpg|mp4)" "$SITE/access.log"' "the server saw 4 GETs: the sitemap and one per page"

step PC2 "--max counts pages only" --box TP2 \
  --what "the same sitemap with --max 3" \
  --why "media entries used to eat into the cap" \
  --look "3 results (the sitemap and two pages), 1 skipped: c.html, not media" \
  --expect "\"skipped\": 1 in the JSON"
run "${ISSUE_CLI:-issue-cli}" page-check "$B/sitemap.xml" --max 3 --json
expect 'grep -qE "\"skipped\": *1" <<<"$out"' "one page left out by the cap"

step PC3 "The unit test" --box TP1 \
  --what "TestSitemapSkipsExtensionLocs in internal/pagecheck" \
  --why "it pins the GET count and the cap on a stand-in site" \
  --look "PASS" --expect "ok"
run go test -C "$ROOT" ./internal/pagecheck -run '^TestSitemapSkipsExtensionLocs$' -count=1 -v
expect '[ $code = 0 ] && grep -q -- "--- PASS: TestSitemapSkipsExtensionLocs" <<<"$out"' "the test ran and passed"

summary
