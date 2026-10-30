package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"gopkg.in/yaml.v3"
)

// The README's screenshots: a named list of pages (docs/images/shots.yaml)
// shot at a fixed width in the light theme, each into its own file. Unlike
// ux-test there are no checks and no run folders; scripts/readme-shots.sh
// stages the demo board and points this at it.

// Shot is one screenshot in the list.
type Shot struct {
	// File is the image's name in the output folder (.png).
	File string `yaml:"file"`
	// Path is the page, from the board's root (/p/demo/hall).
	Path string `yaml:"path"`
	// Wait is a selector that must be visible before the shot; without one
	// the shot waits for the document to be ready.
	Wait string `yaml:"wait,omitempty"`
	// Click is a selector clicked once the page is ready, then Wait again.
	Click string `yaml:"click,omitempty"`
	// Height is the viewport's height in CSS pixels (default 800).
	Height int `yaml:"height,omitempty"`
	// Settle is how long to wait after loading, for polled parts (ms).
	Settle int `yaml:"settle,omitempty"`
}

// shotWidth and shotScale are the README's: 1280 CSS pixels at 2x.
const (
	shotWidth  = 1280
	shotScale  = 2
	shotHeight = 800
)

// lightThemeJS picks the light theme before theme.js reads it.
const lightThemeJS = `try { localStorage.setItem('theme', 'light'); } catch (e) {}`

// LoadShots reads and checks a shot list.
func LoadShots(path string) ([]Shot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var shots []Shot
	if err := yaml.Unmarshal(b, &shots); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if len(shots) == 0 {
		return nil, fmt.Errorf("%s: no shots", path)
	}
	seen := map[string]bool{}
	for i, s := range shots {
		switch {
		case s.File == "" || filepath.Base(s.File) != s.File || !strings.HasSuffix(s.File, ".png"):
			return nil, fmt.Errorf("%s: shot %d: file %q must be a plain name ending in .png", path, i+1, s.File)
		case seen[s.File]:
			return nil, fmt.Errorf("%s: shot %d: file %q is listed twice", path, i+1, s.File)
		case !strings.HasPrefix(s.Path, "/"):
			return nil, fmt.Errorf("%s: %s: path %q must start with /", path, s.File, s.Path)
		}
		seen[s.File] = true
	}
	return shots, nil
}

func cmdShots(o opts, out, errw io.Writer) int {
	if o.list == "" || o.base == "" || o.out == "" {
		fmt.Fprintln(errw, "playtest shots: -list, -base and -out are required (make readme-shots sets them)")
		return 2
	}
	shots, err := LoadShots(o.list)
	if err != nil {
		fmt.Fprintln(errw, "playtest shots:", err)
		return 1
	}
	only := stepFilter(o.only)
	b, err := NewBrowser(false, shotWidth, shotHeight)
	if err != nil {
		fmt.Fprintln(errw, "playtest shots:", err)
		return 1
	}
	defer b.Close()
	failed := 0
	for _, s := range shots {
		if only != nil && !only[s.File] {
			continue
		}
		if err := shoot(b, strings.TrimRight(o.base, "/"), o.out, s); err != nil {
			fmt.Fprintf(errw, "✗ %s: %v\n", s.File, err)
			failed++
			continue
		}
		fmt.Fprintf(out, "✓ %s  %s\n", s.File, s.Path)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// shoot opens one shot's page in a fresh context and saves it.
func shoot(b *Browser, base, dir string, s Shot) error {
	p, err := b.NewPage(nil, lightThemeJS, noMotionJS)
	if err != nil {
		return err
	}
	defer p.Close()
	h := s.Height
	if h == 0 {
		h = shotHeight
	}
	wait := chromedp.WaitReady("body")
	if s.Wait != "" {
		wait = chromedp.WaitVisible(s.Wait)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	steps := []chromedp.Action[chromedp.Void]{
		chromedp.EmulateViewport(shotWidth, int64(h), chromedp.EmulateScale(shotScale)),
		chromedp.Navigate(base + s.Path),
		wait,
	}
	if s.Click != "" {
		steps = append(steps, chromedp.Click(s.Click), wait)
	}
	steps = append(steps, chromedp.Sleep(time.Duration(300+s.Settle)*time.Millisecond))
	if err := p.Do(ctx, steps...); err != nil {
		return err
	}
	png, err := p.Screenshot(ctx, false)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, s.File), png, 0o644)
}
