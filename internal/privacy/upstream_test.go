package privacy

import (
	"os/exec"
	"strings"
	"testing"
)

func TestScanUpstreamNameFlagsEveryFileButLicense(t *testing.T) {
	name := "Mich" + "al"
	dir := leakRepo(t, map[string]string{
		"LICENSE.md":      "Required Notice: Copyright " + name + " Example\n",
		"README.md":       "clean line\nfork of github.com/" + strings.ToLower(name) + "-example/x\n",
		"go.mod":          "module github.com/" + strings.ToUpper(name) + "-x/y\n",
		"docs/LICENSE.md": "a nested copy is not the exempt file: " + name + "\n",
		"clean.go":        "package x\n",
	})
	hits, err := ScanUpstreamName(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, h := range hits {
		got[h.Path] = h.Line
		if !strings.Contains(h.String(), h.Path+":") || strings.Contains(strings.ToLower(h.What), strings.ToLower(name)) {
			t.Errorf("a hit names file and line without echoing the name: %q", h)
		}
	}
	want := map[string]int{"README.md": 2, "go.mod": 1, "docs/LICENSE.md": 1}
	if len(got) != len(want) {
		t.Errorf("hits %v, want %v", got, want)
	}
	for p, line := range want {
		if got[p] != line {
			t.Errorf("%s: line %d, want %d (all hits %v)", p, got[p], line, got)
		}
	}
}

func TestScanUpstreamNamePassesWhenOnlyLicenseHasIt(t *testing.T) {
	dir := leakRepo(t, map[string]string{
		"LICENSE.md": "Required Notice: Copyright " + "Mich" + "al Example\n",
		"README.md":  "github.com/kacperwerema/dark-factory\n",
	})
	hits, err := ScanUpstreamName(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("only LICENSE.md has the name, got %v", hits)
	}
	if hits, _ := ScanUpstreamName(dir, "HEAD"); len(hits) != 0 {
		t.Errorf("at HEAD, got %v", hits)
	}
}

// TestTrackedFilesNameUpstreamOnlyInLicense is the guard over this
// repository (decision D-4 of the public-repo cleanup epic): the upstream
// author's name, in any case, only in LICENSE.md. Each hit is a file:line.
func TestTrackedFilesNameUpstreamOnlyInLicense(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not a git checkout")
	}
	hits, err := ScanUpstreamName(strings.TrimSpace(string(root)), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		t.Errorf("%s", h)
	}
}
