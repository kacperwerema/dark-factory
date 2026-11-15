package viewer

import (
	"net/url"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/codemap"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// WbStationAffected is the station's Affected block: the work branch's
// footprint on the code map (docs/workbench-files.md#code-map), at a
// verify or owner-test status so the owner sees what the change reaches
// at review.
type WbStationAffected struct {
	Files    int
	Changed  int
	Reached  int
	WorksIn  []string // context names
	Reaches  []string
	Binaries []string
	Watch    []string
	Groups   []WbAffectedGroup // reached packages by context
	Surface  []WbAffectedFile
	Other    int
	Main     string // short sha of main the map was built from
	Warn     string // no diff: why
	MapURL   string
}

// WbAffectedGroup is one context's reached packages.
type WbAffectedGroup struct {
	Name     string
	Packages []string
}

// WbAffectedFile is a changed viewer or CLI file and the contexts it imports.
type WbAffectedFile struct {
	File, Contexts string
}

// stationAffected is the block for issue: nil unless its status has role
// verify or owner-test and the project has workbench.contexts.
func stationAffected(p *tracker.Project, wf *tracker.WorkflowConfig, prefix string, issue *tracker.Issue) *WbStationAffected {
	if wf == nil || !codemap.Configured(wf.Workbench) {
		return nil
	}
	if !wf.HasRole(issue.Status, tracker.StatusRoleVerify) && !wf.HasRole(issue.Status, tracker.StatusRoleOwnerTest) {
		return nil
	}
	f, err := codemap.ForIssue(p, wf, issue)
	if err != nil {
		return &WbStationAffected{Warn: err.Error(), MapURL: prefix + "/codemap"}
	}
	if f == nil {
		return nil
	}
	m := f.Map
	names := func(keys []string) []string {
		out := make([]string, len(keys))
		for i, k := range keys {
			out[i] = m.ContextName(k)
		}
		return out
	}
	a := &WbStationAffected{Files: len(f.Files), Changed: len(f.Changed), Reached: len(f.Reached), WorksIn: names(f.WorksIn), Reaches: names(f.Reaches),
		Binaries: f.Binaries, Watch: f.Watch, Other: len(f.Other), Warn: f.Warn, MapURL: prefix + "/codemap"}
	if f.Main != "" {
		a.Main = gitdir.ShortSHA(f.Main)
	}
	by := map[string][]string{}
	for _, d := range f.Reached {
		by[m.Packages[d].Context] = append(by[m.Packages[d].Context], d)
	}
	for _, c := range m.Contexts {
		if ds := by[c.Key]; len(ds) > 0 {
			a.Groups = append(a.Groups, WbAffectedGroup{Name: c.Name, Packages: ds})
			delete(by, c.Key)
		}
	}
	for _, s := range f.Surface {
		a.Surface = append(a.Surface, WbAffectedFile{File: s.File, Contexts: strings.Join(names(s.Contexts), ", ")})
	}
	if len(f.Files) > 0 {
		a.MapURL += "?files=" + url.QueryEscape(strings.Join(f.Files, ","))
	}
	return a
}
