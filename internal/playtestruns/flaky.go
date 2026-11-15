package playtestruns

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// Flaky steps: a ux-test step that failed and passed on the same build is
// flaky, since nothing in the code changed between the two runs. The health
// score's flaky component reads it (internal/ci plugs it into
// internal/health). See docs/workbench-files.md, "The tests family".

// FlakyCount is what the ux-test runs in a window say about flaky steps.
type FlakyCount struct {
	// Runs is how many ux-test runs started in the window.
	Runs int
	// Steps is how many distinct <session>/<step> passed or failed in them.
	Steps int
	// Names are the flaky <session>/<step>, sorted; len(Names) is the count.
	Names []string
}

// flakyResults is the part of results.yaml Flaky reads.
type flakyResults struct {
	Session string `yaml:"session"`
	Mode    string `yaml:"mode"`
	Build   string `yaml:"build"`
	Started string `yaml:"started"`
	Steps   []struct {
		ID     string `yaml:"id"`
		Status string `yaml:"status"`
		Cached string `yaml:"cached"`
	} `yaml:"steps"`
}

// Flaky reads every <dir>/<session>/<run>/results.yaml under dirs and counts
// the ux-test (auto) runs started in [from, to]. A step ran when it passed or
// failed (a cached, skipped or pending step did not); it is flaky when one
// build both failed and passed it. A run with no build, or build "unknown",
// can't be paired and counts for nothing. A missing dir is no runs; an
// unreadable results.yaml is skipped.
func Flaky(dirs []string, from, to time.Time) FlakyCount {
	type key struct{ step, build string }
	seen := map[key]map[string]bool{}
	steps := map[string]bool{}
	var c FlakyCount
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*", "*", "results.yaml"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var r flakyResults
			if yaml.Unmarshal(b, &r) != nil || Mode(r.Mode) != "auto" || r.Build == "" || r.Build == "unknown" {
				continue
			}
			at, err := time.Parse(time.RFC3339, r.Started)
			if err != nil || at.Before(from) || at.After(to) {
				continue
			}
			c.Runs++
			session := r.Session
			if session == "" {
				session = filepath.Base(filepath.Dir(filepath.Dir(f)))
			}
			for _, st := range r.Steps {
				if st.Cached != "" || st.ID == "" || (st.Status != "pass" && st.Status != "fail") {
					continue
				}
				name := session + "/" + st.ID
				steps[name] = true
				k := key{name, r.Build}
				if seen[k] == nil {
					seen[k] = map[string]bool{}
				}
				seen[k][st.Status] = true
			}
		}
	}
	flaky := map[string]bool{}
	for k, st := range seen {
		if st["pass"] && st["fail"] {
			flaky[k.step] = true
		}
	}
	c.Steps = len(steps)
	for n := range flaky {
		c.Names = append(c.Names, n)
	}
	sort.Strings(c.Names)
	return c
}
