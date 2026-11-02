package buildversion

import (
	"runtime/debug"
	"testing"
)

func withBuildInfo(t *testing.T, version string, ok bool) {
	t.Helper()
	old := readBuildInfo
	t.Cleanup(func() { readBuildInfo = old })
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		if !ok {
			return nil, false
		}
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/kacperwerema/dark-factory", Version: version}}, true
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name, stamped, module string
		ok                    bool
		want                  string
	}{
		{"ldflags stamp wins", "v0.59.0-3-gabc1234", "v0.60.0", true, "v0.59.0-3-gabc1234"},
		{"go install @version", "dev", "v0.60.0", true, "v0.60.0"},
		{"empty stamp reads module", "", "v0.60.1", true, "v0.60.1"},
		{"pseudo-version from a checkout", "dev", "v0.60.1-0.20261102120000-abcdef123456+dirty", true, "v0.60.1-0.20261102120000-abcdef123456+dirty"},
		{"devel is not a version", "dev", "(devel)", true, "dev"},
		{"no module version", "dev", "", true, "dev"},
		{"no build info", "dev", "", false, "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withBuildInfo(t, c.module, c.ok)
			if got := Resolve(c.stamped); got != c.want {
				t.Fatalf("Resolve(%q) with module %q = %q, want %q", c.stamped, c.module, got, c.want)
			}
		})
	}
}
