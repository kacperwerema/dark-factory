package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/chromedp"
)

// One screenshot of one page (make shot URL=…): no session, no scenario, no
// checks. It waits for the load event and the body, then a short settle; it
// never waits for the network to go idle, because Workshop pages poll.

// oneShot is what `playtest shot` takes.
type oneShot struct {
	URL  string
	W, H int
	Out  string
	Full bool
}

// withDefaults fills the width, height and output file.
func (s oneShot) withDefaults(repo string) oneShot {
	if s.W <= 0 {
		s.W = shotWidth
	}
	if s.H <= 0 {
		s.H = shotHeight
	}
	if s.Out == "" {
		s.Out = filepath.Join(repo, ".playtest", "shot.png")
	}
	return s
}

// checkShotURL refuses anything but an http(s) URL with a host.
func checkShotURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("-url is required (make shot URL=http://127.0.0.1:8080/…)")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("-url %q must be an http(s) URL with a host", raw)
	}
	return nil
}

func cmdShot(repo string, o opts, out, errw io.Writer) int {
	if err := checkShotURL(o.url); err != nil {
		fmt.Fprintln(errw, "playtest shot:", err)
		return 2
	}
	s := oneShot{URL: o.url, W: o.width, H: o.height, Out: o.out, Full: o.full}.withDefaults(repo)
	if err := os.MkdirAll(filepath.Dir(s.Out), 0o755); err != nil {
		fmt.Fprintln(errw, "playtest shot:", err)
		return 1
	}
	b, err := NewBrowser(false, s.W, s.H)
	if err != nil {
		fmt.Fprintln(errw, "playtest shot:", err)
		return 1
	}
	defer b.Close()
	if err := takeOneShot(b, s); err != nil {
		fmt.Fprintf(errw, "playtest shot: %s: %v\n", s.URL, err)
		return 1
	}
	fmt.Fprintf(out, "✓ %s  %s (%d px)\n", s.Out, s.URL, s.W)
	return 0
}

// takeOneShot opens the page at the shot's viewport and saves it.
func takeOneShot(b *Browser, s oneShot) error {
	p, err := b.NewPage(&Viewport{W: s.W, H: s.H}, noMotionJS)
	if err != nil {
		return err
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.Do(ctx, chromedp.Navigate(s.URL), chromedp.WaitReady("body"), chromedp.Sleep(300*time.Millisecond)); err != nil {
		return err
	}
	png, err := p.Screenshot(ctx, s.Full)
	if err != nil {
		return err
	}
	return os.WriteFile(s.Out, png, 0o644)
}
