package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"

	"github.com/michal-franc/issue-viewer/internal/playtestruns"
)

// testChrome is the browser the playtest runner would drive, or "".
func testChrome() string {
	if p := strings.TrimSpace(os.Getenv("PLAYTEST_CHROME")); p != "" {
		return p
	}
	for _, c := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return playtestruns.PlaywrightChrome(home)
	}
	return ""
}

// Without JavaScript the floor still opens one room: the one the server
// marks open (default Product, ?dept=, ?added=), or the one the hash
// targets (#room-<box>, or #dept-<key> inside an inspector), never two.
func TestCompanyFloorWithoutJSOpensOneRoom(t *testing.T) {
	if testing.Short() {
		t.Skip("starts Chrome")
	}
	chrome := testChrome()
	if chrome == "" {
		t.Skip("no Chrome or Chromium (set PLAYTEST_CHROME)")
	}
	url, _, done := floorServer(t, "connectors")
	defer done()

	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome), chromedp.WindowSize(1440, 900))
	actx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()
	ctx, cancel := chromedp.NewContext(actx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := chromedp.Do(ctx, chromedp.Func(func(ctx context.Context, tg *chromedp.Target) error {
		_, err := cdp.Call(ctx, tg, emulation.SetScriptExecutionDisabled, emulation.SetScriptExecutionDisabledParams{Value: true})
		return err
	})); err != nil {
		t.Fatal(err)
	}
	const shown = `Array.from(document.querySelectorAll('.wb-fl-insp')).filter(e => e.getClientRects().length > 0).map(e => e.dataset.room).join(',')`
	for _, tc := range []struct{ path, want string }{
		{"/p/acme/company", "product"},
		{"/p/acme/company#room-hq", "hq"},
		{"/p/acme/company#room-marketing", "marketing"},
		// The hash wins over the server's pick: still one room.
		{"/p/acme/company?dept=marketing#room-hq", "hq"},
		// An id inside an inspector (Check now's #dept-<key>) opens its room.
		{"/p/acme/company#dept-marketing", "marketing"},
		{"/p/acme/company?dept=marketing", "marketing"},
	} {
		if err := chromedp.Do(ctx, chromedp.Navigate("about:blank"), chromedp.Navigate(url+tc.path), chromedp.WaitReady("body")); err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		got, err := chromedp.Run(ctx, chromedp.Evaluate[string](shown))
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		// Proof the page's own script did not run.
		js, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`document.querySelector('.wb-fl-app').classList.contains('wb-fl--js')`))
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if js {
			t.Fatalf("%s: the floor's script ran; this test needs JavaScript off", tc.path)
		}
		if got != tc.want {
			t.Errorf("%s: open inspectors %q, want %q", tc.path, got, tc.want)
		}
	}
}
