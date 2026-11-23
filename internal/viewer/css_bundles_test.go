package viewer

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

// cssPart is one file of a CSS bundle: its path under the embed and its text.
type cssPart struct{ name, text string }

// cssBundleParts reads a bundle's parts from the embedded static/ in order.
func cssBundleParts(t *testing.T, bundle string) []cssPart {
	t.Helper()
	var parts []cssPart
	for _, p := range cssBundles[bundle] {
		data, err := fs.ReadFile(staticFS, "static/"+p)
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, cssPart{"static/" + p, string(data)})
	}
	if len(parts) == 0 {
		t.Fatalf("no CSS bundle %q", bundle)
	}
	return parts
}

// cssBundleText is a bundle's parts joined in order: the stylesheet a page
// gets, as one text.
func cssBundleText(t *testing.T, bundle string) string {
	t.Helper()
	var b strings.Builder
	for _, p := range cssBundleParts(t, bundle) {
		b.WriteString(p.text)
	}
	return b.String()
}

// Every CSS file under static/ is a part of one bundle, so a page loads it,
// and none is over 1000 lines, so branches that change different views edit
// different files (factory/split-workshop-css-by-view-so-factory-branches-stop-editing-one-file).
func TestCSSPartsListedAndShort(t *testing.T) {
	listed := map[string]string{}
	for bundle, parts := range cssBundles {
		if len(parts) == 0 || parts[0] != bundle+".css" {
			t.Errorf("bundle %s: its first part is %v, want %s.css (the tokens every later part reads)", bundle, parts, bundle)
		}
		for _, p := range parts {
			if other, dup := listed[p]; dup {
				t.Errorf("%s is listed in bundle %s and %s", p, other, bundle)
			}
			listed[p] = bundle
		}
	}
	files, err := fs.Glob(staticFS, "static/*.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := path.Base(f)
		if listed[name] == "" {
			t.Errorf("%s is in no bundle in css_bundles.go: no page loads it", f)
		}
		delete(listed, name)
		data, _ := fs.ReadFile(staticFS, f)
		if n := strings.Count(string(data), "\n"); n > 1000 {
			t.Errorf("%s has %d lines: split it by view (over 1000)", f, n)
		}
	}
	for name := range listed {
		t.Errorf("css_bundles.go lists %s, which is not under static/", name)
	}
}

func TestStylesheetsLinksEveryPartInOrder(t *testing.T) {
	for bundle, parts := range cssBundles {
		got := string(stylesheets(bundle))
		at := 0
		for _, p := range parts {
			link := `<link rel="stylesheet" href="/static/` + p + `">`
			i := strings.Index(got[at:], link)
			if i < 0 {
				t.Fatalf("stylesheets(%q) has no %s after offset %d:\n%s", bundle, link, at, got)
			}
			at += i + len(link)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("stylesheets of an unknown bundle did not panic")
		}
	}()
	stylesheets("nope")
}
