package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Candidate names the files a candidate lint run reads instead of the live
// ones (issue-cli health lint --workflow/--projects): a workflow.yaml for
// Product, a projects.yaml the project is taken from, or both. An empty
// field keeps the live file.
type Candidate struct {
	Workflow string
	Projects string
}

// LoadCandidate is the Env a candidate lint run reads: live's project with
// the candidate's projects.yaml entry (same slug) and workflow file, the
// same issues. When a candidate file sits in another git checkout than the
// live root (a worker's worktree), the project's root moves to that
// checkout, so a job brief, role prompt or department workflow file the
// branch adds counts as there before the merge. A candidate file that
// doesn't load is an error naming it.
func LoadCandidate(live *Env, c Candidate) (*Env, error) {
	proj := *live.Project
	if c.Projects != "" {
		cfg, err := tracker.LoadProjectsConfig(c.Projects)
		if err != nil {
			return nil, err
		}
		var found *tracker.Project
		for i := range cfg.Projects {
			if cfg.Projects[i].Slug == live.Project.Slug {
				found = &cfg.Projects[i]
				break
			}
		}
		if found == nil {
			return nil, fmt.Errorf("%s has no project %q", c.Projects, live.Project.Slug)
		}
		cand := *found
		if cand.IssueDir == "" {
			cand.IssueDir = proj.IssueDir
		}
		if cand.WorkflowFile == "" {
			cand.WorkflowFile = proj.WorkflowFile
		}
		proj = cand
	}
	if c.Workflow != "" {
		abs, err := filepath.Abs(c.Workflow)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("candidate workflow: %w", err)
		}
		if _, err := tracker.LoadWorkflow(abs); err != nil {
			return nil, fmt.Errorf("candidate workflow %s: %w", abs, err)
		}
		proj.WorkflowFile = abs
	}
	for _, f := range []string{c.Workflow, c.Projects} {
		if f == "" {
			continue
		}
		if root := candidateRoot(live.Project.Root(), f); root != "" {
			proj.WorkDir = root
		}
		break
	}
	return NewEnv(&proj, live.Issues, live.Now), nil
}

// candidateRoot is the live root's place in the checkout that holds file,
// when that is another checkout than the live root's; "" when it is the
// same one or file is in none.
func candidateRoot(liveRoot, file string) string {
	abs, err := filepath.Abs(file)
	if err != nil {
		return ""
	}
	candTop := checkoutTop(filepath.Dir(abs))
	liveTop := checkoutTop(liveRoot)
	if candTop == "" || liveTop == "" || realPath(candTop) == realPath(liveTop) {
		return ""
	}
	rel, err := filepath.Rel(realPath(liveTop), realPath(liveRoot))
	if err != nil || strings.HasPrefix(rel, "..") {
		return candTop
	}
	return filepath.Join(candTop, rel)
}

// checkoutTop is the nearest folder at or above dir holding a .git (a
// folder in a clone, a file in a worktree); "" when there is none.
func checkoutTop(dir string) string {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return ""
		}
		d = up
	}
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Delta is what a candidate run adds and removes against the live run.
type Delta struct {
	Project string `json:"project"`
	// Workflow and Projects are the candidate files ("" for the live one).
	Workflow          string    `json:"workflow,omitempty"`
	Projects          string    `json:"projects,omitempty"`
	Checks            []string  `json:"checks"`
	Added             []Finding `json:"added"`
	Removed           []Finding `json:"removed"`
	LiveWarnings      int       `json:"live_warnings"`
	CandidateWarnings int       `json:"candidate_warnings"`
}

// Diff is the warnings cand has that live hasn't (Added) and the other way
// round (Removed). Findings match on check and message, with the
// candidate's root and workflow path read as the live ones, so a line that
// moved is no change; equal findings count as many times as they occur.
func Diff(live, cand *Env, liveRep, candRep *Report) *Delta {
	d := &Delta{Project: liveRep.Project, Checks: liveRep.Checks, Added: []Finding{}, Removed: []Finding{},
		LiveWarnings: liveRep.Warnings, CandidateWarnings: candRep.Warnings}
	norm := strings.NewReplacer()
	var pairs []string
	if cr, lr := cand.Project.Root(), live.Project.Root(); cr != lr {
		pairs = append(pairs, cr, lr)
	}
	if cand.File != "" && live.File != "" && cand.File != live.File {
		pairs = append(pairs, cand.File, live.File)
	}
	if len(pairs) > 0 {
		norm = strings.NewReplacer(pairs...)
	}
	key := func(f Finding, n *strings.Replacer) string { return f.Check + "\x00" + n.Replace(f.Message) }
	plain := strings.NewReplacer()
	left := map[string]int{}
	for _, f := range liveRep.Findings {
		left[key(f, plain)]++
	}
	for _, f := range candRep.Findings {
		k := key(f, norm)
		if left[k] > 0 {
			left[k]--
			continue
		}
		d.Added = append(d.Added, f)
	}
	for _, f := range liveRep.Findings {
		k := key(f, plain)
		if left[k] > 0 {
			left[k]--
			d.Removed = append(d.Removed, f)
		}
	}
	return d
}

// RunCandidate lints the live files and the candidate's with the named
// checks and returns the difference.
func RunCandidate(live *Env, c Candidate, names ...string) (*Delta, error) {
	cand, err := LoadCandidate(live, c)
	if err != nil {
		return nil, err
	}
	liveRep, err := Run(live, names...)
	if err != nil {
		return nil, err
	}
	candRep, err := Run(cand, names...)
	if err != nil {
		return nil, err
	}
	d := Diff(live, cand, liveRep, candRep)
	d.Workflow, d.Projects = c.Workflow, c.Projects
	return d, nil
}
