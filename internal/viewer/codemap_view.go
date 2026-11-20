package viewer

import (
	"net/http"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/codemap"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// CodemapData is /p/<project>/codemap: the code map's lanes (layers, top
// first), their contexts and packages, the imports between contexts, the
// upward ones and the unmapped packages (docs/workbench-files.md#code-map).
// ?files= paints a footprint as issue-cli affected computes it.
type CodemapData struct {
	Header   WbHeader
	Title    string
	Empty    bool // the workflow has no workbench.contexts
	Error    string
	Main     string
	Packages int
	HasGo    bool
	Lanes    []CodemapLane
	Keys     []CodemapKey // the matrix's columns
	Matrix   []CodemapRow
	Upward   []codemap.Edge
	Unmapped []string
	// Graph is the JSON codemap.js reads: package → context, imports.
	Graph CodemapGraph
	// Footprint is ?files= on the map, nil without it.
	Footprint *CodemapFootprint
}

// CodemapLane is one layer.
type CodemapLane struct {
	Name     string
	Level    int
	Shared   bool
	Contexts []CodemapContext
}

// CodemapContext is one context box.
type CodemapContext struct {
	Key, Name, Summary string
	Lines              int
	Packages           []CodemapChip
	Surface            int // surface files that work in it
	Mark               string
}

// CodemapChip is one package chip.
type CodemapChip struct {
	Dir, Label string
	ID         string // the dir with dashes for slashes, for its test id
	Lines      int
	Big, Up    bool
	Mark       string // changed, hit (reached) for a footprint
}

// CodemapKey is a matrix column.
type CodemapKey struct{ Key, Name string }

// CodemapRow is one matrix row: counts by column, Up where it points up.
type CodemapRow struct {
	Name  string
	Cells []CodemapCell
}

// CodemapCell is one count.
type CodemapCell struct {
	N        int
	Up, Self bool
}

// CodemapGraph is what the page's script reads.
type CodemapGraph struct {
	Contexts map[string]string           `json:"contexts"` // key → name
	Packages map[string]CodemapGraphNode `json:"packages"`
}

// CodemapGraphNode is one package for the script.
type CodemapGraphNode struct {
	Context string   `json:"c"`
	Lines   int      `json:"l"`
	Imports []string `json:"d"`
	Up      []string `json:"u,omitempty"`
}

// CodemapFootprint is ?files= on the page.
type CodemapFootprint struct {
	Files                  int
	WorksIn, Reaches       []string
	Binaries, Watch, Other []string
	Changed, Reached       int
}

func (s *Server) handleCodemap(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	d := s.buildCodemap(proj, prefix, r.URL.Query().Get("files"), time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "codemap.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildCodemap(p *tracker.Project, prefix, files string, now time.Time) CodemapData {
	color := s.projectColor(p)
	lot, rows := s.buildLot(p, color, now)
	d := CodemapData{Title: p.Name + " · Code map"}
	d.Header = WbHeader{Crumbs: wbCrumbs(1, p.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, OwnerWait: lot.You, DoneWeek: lot.DoneWeek, Waiting: len(rows), WaitingURL: prefix + "/needs-you"}
	d.Header.Crumbs[1].Color, d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = color, false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Code map", Current: true}
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)

	m, sha, err := codemap.ForProject(p, p.LoadWorkflow())
	if err != nil {
		d.Error = err.Error()
		return d
	}
	if m == nil {
		d.Empty = true
		return d
	}
	if sha != "" {
		d.Main = gitdir.ShortSHA(sha)
	}
	d.Packages, d.HasGo = len(m.Packages), m.HasGo
	changed, hit, works, reach := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	if fs := strings.FieldsFunc(files, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }); len(fs) > 0 {
		fp := m.Affected(fs)
		names := func(keys []string) []string {
			out := make([]string, len(keys))
			for i, k := range keys {
				out[i] = m.ContextName(k)
			}
			return out
		}
		d.Footprint = &CodemapFootprint{Files: len(fp.Files), WorksIn: names(fp.WorksIn), Reaches: names(fp.Reaches), Binaries: fp.Binaries, Watch: fp.Watch, Other: fp.Other, Changed: len(fp.Changed), Reached: len(fp.Reached)}
		for _, c := range fp.Changed {
			changed[c] = true
		}
		for _, c := range fp.Reached {
			hit[c] = true
		}
		for _, k := range fp.WorksIn {
			works[k] = true
		}
		for _, k := range fp.Reaches {
			reach[k] = true
		}
	}
	d.Graph = CodemapGraph{Contexts: map[string]string{}, Packages: map[string]CodemapGraphNode{}}
	for _, c := range m.Contexts {
		d.Graph.Contexts[c.Key] = c.Name
	}
	for dir, pk := range m.Packages {
		d.Graph.Packages[dir] = CodemapGraphNode{Context: pk.Context, Lines: pk.Lines, Imports: pk.Imports, Up: pk.Up}
	}
	for i := len(m.Layers) - 1; i >= 0; i-- {
		l := m.Layers[i]
		lane := CodemapLane{Name: l.Name, Level: l.Level, Shared: l.Shared}
		for _, c := range m.Contexts {
			if c.Level != l.Level {
				continue
			}
			box := CodemapContext{Key: c.Key, Name: c.Name, Summary: c.Summary, Lines: c.Lines}
			if l.Level != m.Top() && !c.Shared {
				box.Surface = len(m.SurfaceFiles(c.Key))
			}
			switch {
			case works[c.Key]:
				box.Mark = "works-in"
			case reach[c.Key]:
				box.Mark = "reaches"
			}
			for _, dir := range c.Packages {
				pk := m.Packages[dir]
				chip := CodemapChip{Dir: dir, ID: strings.ReplaceAll(dir, "/", "-"), Label: strings.TrimPrefix(dir, "internal/"), Lines: pk.Lines, Big: pk.Lines > 3000, Up: len(pk.Up) > 0}
				switch {
				case changed[dir]:
					chip.Mark = "changed"
				case hit[dir]:
					chip.Mark = "hit"
				}
				box.Packages = append(box.Packages, chip)
			}
			lane.Contexts = append(lane.Contexts, box)
		}
		d.Lanes = append(d.Lanes, lane)
	}
	counts := map[[2]string]codemap.Count{}
	for _, c := range m.Between() {
		counts[[2]string{c.From, c.To}] = c
	}
	for _, c := range m.Contexts {
		d.Keys = append(d.Keys, CodemapKey{Key: c.Key, Name: c.Name})
	}
	for _, a := range m.Contexts {
		row := CodemapRow{Name: a.Name}
		for _, b := range m.Contexts {
			n := counts[[2]string{a.Key, b.Key}]
			row.Cells = append(row.Cells, CodemapCell{N: n.N, Up: n.Up, Self: a.Key == b.Key})
		}
		d.Matrix = append(d.Matrix, row)
	}
	d.Upward = m.Upward()
	d.Unmapped = m.Unmapped()
	return d
}
