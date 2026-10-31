package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// pageViolations opens html at w×h with checks.js and returns the layout
// rule's violations as "<id> <rule>".
func pageViolations(t *testing.T, html string, w, h int, rules ...string) []string {
	t.Helper()
	if _, err := ChromePath(); err != nil {
		t.Skip(err)
	}
	b, err := NewBrowser(false, w, h)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(b.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { fmt.Fprint(rw, html) }))
	t.Cleanup(srv.Close)
	pg, err := b.NewPage(&Viewport{w, h}, checksJS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pg.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	if err := pg.Do(ctx, chromedp.Navigate(srv.URL)); err != nil {
		t.Fatal(err)
	}
	var v []LayoutViolation
	if err := pg.Call(ctx, &v, "layout", rules, []string{}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range v {
		got = append(got, l.ID+" "+l.Rule)
	}
	sort.Strings(got)
	return got
}

func TestNoPageScrollRule(t *testing.T) {
	const head = `<!doctype html><html><head><style>body{margin:0} .app{height:100vh;display:flex;flex-direction:column} .box{height:200px;flex:none}</style></head><body>`
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"fits", `<div class="app"><div class="box" data-testid="a">a</div></div>`, nil},
		{"page scrolls", `<div style="height:1400px" data-testid="tall">tall</div>`, []string{"(page) no_page_scroll", "tall no_page_scroll"}},
		// The page hides its overflow: it doesn't scroll, but "cut" ends
		// below the viewport, out of reach.
		{"hidden overflow cuts off", `<div class="app" style="overflow:hidden"><div class="box" style="height:700px">x</div><div class="box" data-testid="cut">cut</div></div>`, []string{"cut no_page_scroll"}},
		// The same block in a panel that scrolls is reachable.
		{"in a scrolling panel", `<div class="app"><div style="flex:1;min-height:0;overflow:auto"><div class="box" style="height:700px">x</div><div class="box" data-testid="reach">reach</div></div></div>`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pageViolations(t, head+tc.body+`</body></html>`, 800, 600, "no_page_scroll")
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("violations %q, want %q", got, tc.want)
			}
		})
	}
}
