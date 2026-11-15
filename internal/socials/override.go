package socials

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// varRe is what an env var name may be: it is written into YAML and a
// compose interpolation as is.
var varRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// OverridePath is the tool's compose override in the state dir, shared by
// every project on the machine (one tool per machine, like its lease).
func OverridePath(tool string) string {
	base := viewerstate.Dir()
	if base == "" || tool == "" {
		return ""
	}
	return filepath.Join(base, "socials", tool, "compose.override.yaml")
}

// WriteOverride writes a's override for vars at path, aside and renamed.
func WriteOverride(a Adapter, path string, vars []string) error {
	if path == "" {
		return fmt.Errorf("no state dir for the compose override")
	}
	vs := append([]string{}, vars...)
	sort.Strings(vs)
	for _, v := range vs {
		if !varRe.MatchString(v) {
			return fmt.Errorf("env var %q is not a plain NAME", v)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return gitfile.WriteAtomic(path, a.Override(vs), 0o600)
}
