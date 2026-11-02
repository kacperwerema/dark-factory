package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// A scenario board on demand (make serve SCENARIO=…): the scenario staged
// as ux-test stages it (own HOME, XDG dirs, tmux socket, free port), but the
// board keeps running after serve exits. With -base REF a second board,
// built from that ref, serves the same fixtures beside it, for before/after
// shots (make shot URL=/p/… BASE=REF). Each serve leaves a record under
// .playtest/serve/<scenario>.json; make serve-stop reads it, kills only the
// boards it names and removes only folders carrying the scenario marker.

// serveMarker marks a serve's parent folder (it holds head/ and base/).
const serveMarker = ".playtest-serve"

// ServeRecord is .playtest/serve/<scenario>.json.
type ServeRecord struct {
	Scenario string        `json:"scenario"`
	Project  string        `json:"project"` // the scenario's project slug (/p/<project>/…)
	Dir      string        `json:"dir"`     // the parent folder (serveMarker)
	Started  time.Time     `json:"started"`
	Boards   []ServedBoard `json:"boards"`
}

// ServedBoard is one running board of a serve: "head" (this checkout) or
// "base" (built from Ref).
type ServedBoard struct {
	Label  string `json:"label"`
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit,omitempty"`
	URL    string `json:"url"`
	PID    int    `json:"pid"`
	Root   string `json:"root"`   // the scenario root (markerName)
	Config string `json:"config"` // its projects.yaml: the board's command line names it
	Log    string `json:"log"`
}

// Board returns the record's board with this label, or nil.
func (r *ServeRecord) Board(label string) *ServedBoard {
	for i := range r.Boards {
		if r.Boards[i].Label == label {
			return &r.Boards[i]
		}
	}
	return nil
}

// serveDir is where the records live.
func serveDir(repo string) string { return filepath.Join(repo, ".playtest", "serve") }

func recordPath(repo, scenario string) string {
	return filepath.Join(serveDir(repo), scenario+".json")
}

func writeRecord(repo string, r *ServeRecord) error {
	if err := os.MkdirAll(serveDir(repo), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	p := recordPath(repo, r.Scenario)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// loadRecords reads every record, sorted by scenario name.
func loadRecords(repo string) ([]*ServeRecord, error) {
	paths, err := filepath.Glob(filepath.Join(serveDir(repo), "*.json"))
	if err != nil {
		return nil, err
	}
	var out []*ServeRecord
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var r ServeRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		out = append(out, &r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scenario < out[j].Scenario })
	return out, nil
}

// boardAlive: the board's process is still there and its command line names
// the board's projects.yaml, so a pid the system reused for something else
// never counts (and is never killed).
func boardAlive(b ServedBoard) bool {
	if b.PID <= 0 || syscall.Kill(b.PID, 0) != nil {
		return false
	}
	return cmdlineOf(b.PID, b.Config)
}

// cmdlineOf reports whether pid's command line contains want. A stand-in
// board in a test (want "") is the test process itself.
var cmdlineOf = func(pid int, want string) bool {
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", fmt.Sprint(pid)).Output()
	return err == nil && want != "" && strings.Contains(string(out), want)
}

// live reports whether every board of the record is alive.
func (r *ServeRecord) live() bool {
	if len(r.Boards) == 0 {
		return false
	}
	for _, b := range r.Boards {
		if !boardAlive(b) {
			return false
		}
	}
	return true
}

// serveOpts is what `playtest serve` takes.
type serveOpts struct {
	Scenario    string
	ScenarioDir string
	Base        string // a git ref to serve beside the checkout ("" none)
	Port        int    // the head board's port when free
	Dir         string // the parent folder; "" is a fresh pt-serve-* temp dir
}

// Serve stages the scenario (and, with Base, the same scenario on a board
// built from that ref), records it and leaves the boards running.
func Serve(repo string, o serveOpts, bins *Binaries, log io.Writer) (*ServeRecord, []string, error) {
	if o.Scenario == "" {
		return nil, nil, errors.New("-scenario is required (make serve SCENARIO=<name>; playtest/scenarios/ lists them)")
	}
	spec, err := LoadScenarioSpec(filepath.Join(o.ScenarioDir, o.Scenario))
	if err != nil {
		return nil, nil, err
	}
	if old, err := readRecord(repo, o.Scenario); err == nil {
		if old.live() {
			return nil, nil, fmt.Errorf("scenario %s is already served at %s; stop it first: make serve-stop SCENARIO=%s", o.Scenario, old.Boards[0].URL, o.Scenario)
		}
		stopRecord(repo, old, false, io.Discard)
	}
	dir := o.Dir
	if dir == "" {
		// Short: the scenario's tmux socket lives under it.
		if dir, err = os.MkdirTemp("", "pt-serve-"); err != nil {
			return nil, nil, err
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, serveMarker), []byte(o.Scenario+"\n"), 0o644); err != nil {
		return nil, nil, err
	}
	rec := &ServeRecord{Scenario: o.Scenario, Project: spec.Project.Slug, Dir: dir, Started: time.Now()}
	var notes []string
	var started []*Scenario
	fail := func(err error) (*ServeRecord, []string, error) {
		for _, sc := range started {
			sc.Stop(false)
		}
		removeMarked(dir, serveMarker)
		return nil, nil, err
	}
	start := func(label string, b *Binaries, port int) (*Scenario, error) {
		sc, err := StartScenarioWith(o.ScenarioDir, o.Scenario, b, log, StartOpts{Root: filepath.Join(dir, label), Port: port, Detached: true})
		if err != nil {
			return nil, fmt.Errorf("%s board: %v", label, err)
		}
		started = append(started, sc)
		if sc.PortTaken != 0 {
			notes = append(notes, fmt.Sprintf("port %d is taken; the %s board is on %d", sc.PortTaken, label, sc.Port))
		}
		return sc, nil
	}
	head, err := start("head", bins, o.Port)
	if err != nil {
		return fail(err)
	}
	rec.Boards = append(rec.Boards, boardOf("head", head))
	if o.Base != "" {
		commit, baseBins, err := buildRef(repo, o.Base, filepath.Join(dir, "base-bin"), log)
		if err != nil {
			return fail(err)
		}
		sc, err := start("base", baseBins, 0)
		if err != nil {
			return fail(err)
		}
		b := boardOf("base", sc)
		b.Ref, b.Commit = o.Base, commit
		rec.Boards = append(rec.Boards, b)
	}
	if err := writeRecord(repo, rec); err != nil {
		return fail(err)
	}
	// The boards are on their own now: serve returns, they keep running.
	for _, sc := range started {
		_ = sc.board.Process.Release()
		sc.board = nil
	}
	return rec, notes, nil
}

func boardOf(label string, sc *Scenario) ServedBoard {
	return ServedBoard{Label: label, URL: sc.Base, PID: sc.board.Process.Pid, Root: sc.Root, Config: sc.ConfigPath(), Log: sc.LogPath()}
}

func readRecord(repo, scenario string) (*ServeRecord, error) {
	data, err := os.ReadFile(recordPath(repo, scenario))
	if err != nil {
		return nil, err
	}
	var r ServeRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// buildRef builds issue-viewer and issue-cli from ref (git archive into a
// temp folder, no worktree to clean up) into binDir, and returns the
// commit it built.
func buildRef(repo, ref, binDir string, log io.Writer) (string, *Binaries, error) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--short", ref+"^{commit}").Output()
	if err != nil {
		return "", nil, fmt.Errorf("BASE %s: not a commit in this repo", ref)
	}
	commit := strings.TrimSpace(string(out))
	src, err := os.MkdirTemp("", "pt-src-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(src)
	archive := exec.Command("git", "-C", repo, "archive", "--format=tar", commit)
	untar := exec.Command("tar", "-x", "-C", src)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	untar.Stdin = pipe
	untar.Stderr, archive.Stderr = log, log
	if err := untar.Start(); err != nil {
		return "", nil, err
	}
	if err := archive.Run(); err != nil {
		return "", nil, fmt.Errorf("git archive %s: %v", ref, err)
	}
	if err := untar.Wait(); err != nil {
		return "", nil, fmt.Errorf("unpack %s: %v", ref, err)
	}
	bins, err := BuildBinaries(src, binDir, log)
	if err != nil {
		return "", nil, fmt.Errorf("BASE %s (%s): %v", ref, commit, err)
	}
	return commit, bins, nil
}

// stopRecord kills the record's boards (only those still alive as
// themselves), removes their folders (only marked ones, unless keep) and
// the record.
func stopRecord(repo string, r *ServeRecord, keep bool, out io.Writer) {
	for _, b := range r.Boards {
		if boardAlive(b) {
			_ = syscall.Kill(-b.PID, syscall.SIGTERM)
			for deadline := time.Now().Add(5 * time.Second); boardAlive(b) && time.Now().Before(deadline); {
				time.Sleep(50 * time.Millisecond)
			}
			if boardAlive(b) {
				_ = syscall.Kill(-b.PID, syscall.SIGKILL)
			}
			fmt.Fprintf(out, "✓ stopped %s board %s (pid %d)\n", b.Label, b.URL, b.PID)
		} else {
			fmt.Fprintf(out, "· %s board %s was not running\n", b.Label, b.URL)
		}
		// The scenario's private tmux server, if anything started one.
		(&Scenario{Root: b.Root}).Stop(true)
	}
	if !keep && r.Dir != "" {
		for _, b := range r.Boards {
			removeMarked(b.Root, markerName)
		}
		removeMarked(r.Dir, serveMarker)
	}
	_ = os.Remove(recordPath(repo, r.Scenario))
}

// removeMarked removes dir only when it carries marker.
func removeMarked(dir, marker string) {
	if dir == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
		_ = os.RemoveAll(dir)
	}
}

func cmdServe(repo string, o opts, out, errw io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintln(errw, "playtest serve:", err)
		return 1
	}
	if err := mustBeQuiet(); err != nil {
		return fail(err)
	}
	// Refuse a second serve of the same scenario before the minute-long build.
	if old, err := readRecord(repo, o.scenario); err == nil && old.live() {
		return fail(fmt.Errorf("scenario %s is already served at %s; stop it first: make serve-stop SCENARIO=%s", o.scenario, old.Boards[0].URL, o.scenario))
	}
	dir := o.dir
	if dir != "" {
		// stop removes the whole folder: never one that holds anything else.
		if ents, err := os.ReadDir(dir); err == nil && len(ents) > 0 {
			return fail(fmt.Errorf("-dir %s is not empty; give a new or empty folder (stop removes it)", dir))
		}
	} else {
		var err error
		// Short: the scenario's tmux socket lives under it.
		if dir, err = os.MkdirTemp("", "pt-serve-"); err != nil {
			return fail(err)
		}
	}
	// The boards run these binaries and put their folder on PATH, so they
	// live with the serve and go when it is stopped.
	binDir := filepath.Join(dir, "head-bin")
	bins, err := BuildBinaries(repo, binDir, errw)
	if err != nil {
		os.RemoveAll(binDir)
		return fail(err)
	}
	so := serveOpts{Scenario: o.scenario, ScenarioDir: o.scenarios, Base: o.baseRef, Port: o.port, Dir: dir}
	rec, notes, err := Serve(repo, so, bins, errw)
	if err != nil {
		os.RemoveAll(binDir)
		return fail(err)
	}
	printServe(out, rec, notes)
	return 0
}

// mustBeQuiet runs mem-guard check when it is installed; a blocked machine
// starts no board.
func mustBeQuiet() error {
	bin, err := exec.LookPath("mem-guard")
	if err != nil {
		return nil
	}
	if out, err := exec.Command(bin, "check").CombinedOutput(); err != nil {
		return fmt.Errorf("mem-guard check: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func printServe(out io.Writer, r *ServeRecord, notes []string) {
	for _, n := range notes {
		fmt.Fprintln(out, "note:", n)
	}
	fmt.Fprintf(out, "✓ serving scenario %s (project %s)\n", r.Scenario, r.Project)
	for _, b := range r.Boards {
		what := "this checkout"
		if b.Ref != "" {
			what = b.Ref + " @ " + b.Commit
		}
		fmt.Fprintf(out, "  %-4s  %s   (%s)   log %s\n", b.Label, b.URL, what, b.Log)
	}
	page := "/p/" + r.Project + "/company"
	shot := "make shot URL=" + page
	if base := r.Board("base"); base != nil {
		shot += " BASE=" + base.Ref
	}
	fmt.Fprintf(out, "  owner sign-in: open <url>/owner; the code is printed in the board's log\n")
	fmt.Fprintf(out, "  shoot: %s   (HTML=1 also saves the rendered HTML of each)\n", shot)
	fmt.Fprintf(out, "  stop:  make serve-stop SCENARIO=%s\n", r.Scenario)
}

func cmdStop(repo string, o opts, out, errw io.Writer) int {
	recs, err := loadRecords(repo)
	if err != nil {
		fmt.Fprintln(errw, "playtest stop:", err)
		return 1
	}
	n := 0
	for _, r := range recs {
		if o.scenario != "" && r.Scenario != o.scenario {
			continue
		}
		stopRecord(repo, r, o.keep, out)
		n++
	}
	if n == 0 {
		if o.scenario != "" {
			fmt.Fprintf(errw, "playtest stop: no serve of %s recorded in %s\n", o.scenario, serveDir(repo))
			return 1
		}
		fmt.Fprintln(out, "nothing served")
	}
	return 0
}

// resolveShotURL turns a shot's -url into the head page's URL and, with
// baseRef, the base board's: a full URL is used as is (and cannot have a
// base: which board would it be?); a path is joined to the live serve's
// boards, the one for scenario when given.
func resolveShotURL(repo, raw, scenario, baseRef string) (head, base string, err error) {
	if raw == "" {
		return "", "", fmt.Errorf("-url is required (make shot URL=http://127.0.0.1:8080/… or URL=/p/<project>/… with make serve running)")
	}
	if !strings.HasPrefix(raw, "/") {
		if baseRef != "" {
			return "", "", fmt.Errorf("BASE=%s needs a path URL (URL=/p/…) on a served scenario: make serve SCENARIO=<name> BASE=%s", baseRef, baseRef)
		}
		return raw, "", checkShotURL(raw)
	}
	recs, err := loadRecords(repo)
	if err != nil {
		return "", "", err
	}
	var live []*ServeRecord
	for _, r := range recs {
		if (scenario == "" || r.Scenario == scenario) && r.live() {
			live = append(live, r)
		}
	}
	hint := "make serve SCENARIO=<name>"
	if scenario != "" {
		hint = "make serve SCENARIO=" + scenario
	}
	if baseRef != "" {
		hint += " BASE=" + baseRef
	}
	if baseRef != "" {
		var withBase []*ServeRecord
		for _, r := range live {
			if b := r.Board("base"); b != nil && b.Ref == baseRef {
				withBase = append(withBase, r)
			}
		}
		live = withBase
	}
	switch len(live) {
	case 0:
		what := "no served scenario board"
		if baseRef != "" {
			what = "no served scenario board with BASE=" + baseRef
		}
		return "", "", fmt.Errorf("%s for %s: run %s first", what, raw, hint)
	case 1:
	default:
		var names []string
		for _, r := range live {
			names = append(names, r.Scenario)
		}
		return "", "", fmt.Errorf("%d served scenarios (%s): pick one with SCENARIO=", len(live), strings.Join(names, ", "))
	}
	r := live[0]
	join := func(b *ServedBoard) string {
		u, _ := url.Parse(b.URL)
		p, _ := url.Parse(raw)
		return u.ResolveReference(p).String()
	}
	head = join(r.Board("head"))
	if baseRef != "" {
		base = join(r.Board("base"))
	}
	return head, base, nil
}

// baseOut is the base board's file beside out: shot.png → shot.base.png.
func baseOut(out string) string {
	ext := filepath.Ext(out)
	return strings.TrimSuffix(out, ext) + ".base" + ext
}

// htmlOut is a shot's rendered HTML beside its PNG: shot.png → shot.html.
func htmlOut(png string) string {
	return strings.TrimSuffix(png, filepath.Ext(png)) + ".html"
}
