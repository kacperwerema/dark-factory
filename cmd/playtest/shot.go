package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	// Scroll is a selector for an inner scroller to unroll (implies Full).
	Scroll string
	// HTML also saves the rendered DOM beside the PNG (htmlOut).
	HTML bool
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
	head, base, err := resolveShotURL(repo, o.url, o.scenario, o.baseRef)
	if err != nil {
		fmt.Fprintln(errw, "playtest shot:", err)
		return 2
	}
	s := oneShot{URL: head, W: o.width, H: o.height, Out: o.out, Full: o.full || o.scroll != "", Scroll: o.scroll, HTML: o.html}.withDefaults(repo)
	shots := []oneShot{s}
	if base != "" {
		b := s
		b.URL, b.Out = base, baseOut(s.Out)
		shots = append(shots, b)
	}
	if err := os.MkdirAll(filepath.Dir(s.Out), 0o755); err != nil {
		fmt.Fprintln(errw, "playtest shot:", err)
		return 1
	}
	br, err := NewBrowser(false, s.W, s.H)
	if err != nil {
		fmt.Fprintln(errw, "playtest shot:", err)
		return 1
	}
	defer br.Close()
	for _, sh := range shots {
		if err := takeOneShot(br, sh); err != nil {
			fmt.Fprintf(errw, "playtest shot: %s: %v\n", sh.URL, err)
			return 1
		}
		fmt.Fprintf(out, "✓ %s  %s (%d px)\n", sh.Out, sh.URL, sh.W)
		if sh.HTML {
			fmt.Fprintf(out, "✓ %s\n", htmlOut(sh.Out))
		}
	}
	if len(shots) == 2 && s.HTML {
		fmt.Fprintf(out, "diff: diff -u %s %s\n", htmlOut(shots[1].Out), htmlOut(s.Out))
	}
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
	// The DOM before any unroll, which edits inline styles.
	if s.HTML {
		var html string
		if err := p.Eval(ctx, "JSON.stringify(document.documentElement.outerHTML)", &html); err != nil {
			return err
		}
		if err := os.WriteFile(htmlOut(s.Out), []byte("<!doctype html>\n"+html+"\n"), 0o644); err != nil {
			return err
		}
	}
	if s.Full {
		var msg string
		if err := p.Eval(ctx, "JSON.stringify("+unrollJS+"("+jsString(s.Scroll)+"))", &msg); err != nil {
			return err
		}
		if strings.HasPrefix(msg, "!") {
			return fmt.Errorf("%s", msg[1:])
		}
		if msg != "" {
			// The page's own layout code (roads, lines) redraws on resize.
			if err := p.Do(ctx, chromedp.Sleep(300*time.Millisecond)); err != nil {
				return err
			}
		}
	}
	png, err := p.Screenshot(ctx, s.Full)
	if err != nil {
		return err
	}
	return os.WriteFile(s.Out, png, 0o644)
}

// jsString quotes s as a JavaScript string literal.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// unrollJS lets a full-page shot reach an inner scroller. Workshop pages are
// one screen tall and scroll inside (.wb-map, .wb-scroll), so a plain full
// shot is the viewport. Given a selector it unrolls that element; given ""
// and a page that doesn't scroll itself, the scroller hiding the most. The
// element and every ancestor drop their height cap and clip, so the
// document grows to the content and the full shot takes it all. It returns
// "" when nothing was unrolled, the element's description when one was,
// and "!message" when the selector matches nothing.
const unrollJS = `(function (sel) {
  var el = null;
  if (sel) {
    el = document.querySelector(sel);
    if (!el) return '!-scroll ' + sel + ' matches no element';
  } else {
    var doc = document.scrollingElement || document.documentElement;
    if (doc.scrollHeight > window.innerHeight + 1) return '';
    var most = 0;
    document.querySelectorAll('body *').forEach(function (e) {
      var cs = getComputedStyle(e);
      if (!/(auto|scroll)/.test(cs.overflowY + ' ' + cs.overflowX)) return;
      var hidden = Math.max(0, e.scrollHeight - e.clientHeight) * e.clientWidth +
        Math.max(0, e.scrollWidth - e.clientWidth) * e.clientHeight;
      if (hidden > most) { most = hidden; el = e; }
    });
    if (!el) return '';
  }
  var h = el.scrollHeight;
  for (var e = el; e; e = e.parentElement) {
    e.style.setProperty('overflow', 'visible', 'important');
    e.style.setProperty('max-height', 'none', 'important');
    e.style.setProperty('height', e === el ? h + 'px' : 'auto', 'important');
  }
  window.dispatchEvent(new Event('resize'));
  return el.tagName.toLowerCase() + (el.className ? '.' + String(el.className).trim().split(/\s+/).join('.') : '');
})`
