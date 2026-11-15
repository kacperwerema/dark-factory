package critique

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/memory"
)

// Run is one critique run, one line of <issues>/.critique/runs.jsonl.
type Run struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	From string `json:"from"`
	// To is the gate's target; "" for a run by hand from a status with no
	// gate out of it (ByHand).
	To string    `json:"to"`
	At time.Time `json:"at"`
	// Agent is the critic's CLI; "" under critique: command:, which may
	// run anything.
	Agent string `json:"agent,omitempty"`
	// Worker is the agent that wrote the work, from the dispatch record;
	// "" without one.
	Worker string `json:"worker,omitempty"`
	// Why says how Agent was picked: "not the worker's claude", "codex
	// resting until 17:00; on the worker's claude", "--agent".
	Why string `json:"why,omitempty"`
	// SameAgent: the critic is the agent that wrote the work, Why says why.
	SameAgent bool `json:"same_agent,omitempty"`
	// Head is the branch tip the critic read ("" without a branch).
	Head   string `json:"head,omitempty"`
	Report Report `json:"report"`
	// Raw is the critic's stdout as kept on disk, relative to the issues
	// dir (.critique/raw/<id>.txt), secrets redacted; "" for runs from
	// before it was kept. issue-cli critique --reparse parses it again.
	Raw string `json:"raw,omitempty"`
	// RawRedacted names the kinds of secret redacted from the raw file.
	RawRedacted []string `json:"raw_redacted,omitempty"`
	// Failed: the critic exited non-zero or timed out; Error says how.
	Failed bool   `json:"failed,omitempty"`
	Error  string `json:"error,omitempty"`
	// Fallback says the first agent hit its usage limit and the critic
	// ran again on another ("codex hit its usage limit (…); ran on claude").
	Fallback string `json:"fallback,omitempty"`
	// Override: the owner let the gate pass past this run's blocks.
	Override *Override `json:"override,omitempty"`
}

// Override is the owner's word that a run's blocks don't hold the gate.
type Override struct {
	At    time.Time `json:"at"`
	Quote string    `json:"quote"`
	By    string    `json:"by,omitempty"`
}

// event is a line of the log: a run, or an override of an earlier run.
type event struct {
	Run      *Run      `json:"run,omitempty"`
	Override *Override `json:"override,omitempty"`
	RunID    string    `json:"run_id,omitempty"`
}

// Dir is the critique folder under the issues dir.
func Dir(issuesDir string) string { return filepath.Join(issuesDir, ".critique") }

// LogPath is the run log.
func LogPath(issuesDir string) string { return filepath.Join(Dir(issuesDir), "runs.jsonl") }

// Holds reports whether the run keeps the gate shut: it failed, has a
// blocks finding (malformed included) or an output problem, and no
// override.
func (r *Run) Holds() bool {
	if r == nil {
		return true
	}
	if r.Override != nil {
		return false
	}
	return r.Failed || r.Report.Count(Blocks) > 0 || len(r.Report.Problems) > 0
}

// ByHand reports a run from a status with no gate out of it: it is
// recorded and commented, and no gate or red round reads it.
func (r *Run) ByHand() bool { return r != nil && r.To == "" }

// Edge is where the run read the work, in words: "in design → backlog", or
// "in progress, by hand (no gate)".
func (r *Run) Edge() string {
	if r.ByHand() {
		return r.From + ", by hand (no gate)"
	}
	return r.From + " → " + r.To
}

// Verdict is the run in a word: "clear", "holds", "overridden", "failed",
// or "findings" for a run by hand with blocks findings, which holds nothing.
func (r *Run) Verdict() string {
	switch {
	case r == nil:
		return "none"
	case r.Override != nil:
		return "overridden"
	case r.Failed:
		return "failed"
	case r.Holds() && r.ByHand():
		return "findings"
	case r.Holds():
		return "holds"
	default:
		return "clear"
	}
}

// Append writes one event line. A single O_APPEND write keeps two runs at
// once from interleaving.
func appendEvent(issuesDir string, e event) error {
	if err := os.MkdirAll(Dir(issuesDir), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(LogPath(issuesDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// RawDir is where the critics' raw outputs are kept.
func RawDir(issuesDir string) string { return filepath.Join(Dir(issuesDir), "raw") }

// Record appends a run, giving it an ID when it has none.
func Record(issuesDir string, r *Run) error {
	if r.ID == "" {
		r.ID = r.At.UTC().Format("20060102T150405.000000000Z")
	}
	return appendEvent(issuesDir, event{Run: r})
}

// RecordWithRaw keeps the critic's stdout beside the run, then records
// the run naming the file: .critique/raw/<id>.txt, scrubbed by the
// memory's secret scan (memory.RedactSecrets). A raw file that can't be
// written leaves Raw empty; the run is recorded all the same.
func RecordWithRaw(issuesDir string, r *Run, raw string) error {
	if r.ID == "" {
		r.ID = r.At.UTC().Format("20060102T150405.000000000Z")
	}
	if err := writeRaw(issuesDir, r, raw); err != nil {
		r.Raw = ""
	}
	return Record(issuesDir, r)
}

func writeRaw(issuesDir string, r *Run, raw string) error {
	if err := os.MkdirAll(RawDir(issuesDir), 0o755); err != nil {
		return err
	}
	clean, kinds := memory.RedactSecrets(raw)
	name := filepath.Join(RawDir(issuesDir), r.ID+".txt")
	if err := gitfile.WriteAtomic(name, []byte(clean), 0o644); err != nil {
		return err
	}
	r.Raw = filepath.ToSlash(filepath.Join(".critique", "raw", r.ID+".txt"))
	r.RawRedacted = kinds
	return nil
}

// ReadRaw is the kept raw output of run r.
func ReadRaw(issuesDir string, r *Run) (string, error) {
	if r.Raw == "" {
		return "", fmt.Errorf("run %s has no raw output kept (it is from before raw outputs were kept)", r.ID)
	}
	// Only a file in the raw folder: the record names it, never a path elsewhere.
	name := filepath.Join(RawDir(issuesDir), filepath.Base(filepath.FromSlash(r.Raw)))
	data, err := os.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("run %s's raw output: %w", r.ID, err)
	}
	return string(data), nil
}

// Find is the run with id, nil when there is none.
func Find(runs []*Run, id string) *Run {
	for _, r := range runs {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// RecordOverride marks run id as overridden by the owner.
func RecordOverride(issuesDir, runID string, o Override) error {
	if strings.TrimSpace(o.Quote) == "" {
		return fmt.Errorf("an override needs the owner's words (--quote)")
	}
	return appendEvent(issuesDir, event{RunID: runID, Override: &o})
}

// Load reads every run for slug ("" for all), oldest first, with overrides
// folded in. A missing log is no runs; a bad line is skipped.
func Load(issuesDir, slug string) ([]*Run, error) {
	f, err := os.Open(LogPath(issuesDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var runs []*Run
	byID := map[string]*Run{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch {
		case e.Run != nil:
			if slug != "" && e.Run.Slug != slug {
				continue
			}
			runs = append(runs, e.Run)
			byID[e.Run.ID] = e.Run
		case e.Override != nil:
			if r := byID[e.RunID]; r != nil {
				r.Override = e.Override
			}
		}
	}
	return runs, sc.Err()
}

// Latest is the newest run for slug from status from ("" for any), nil
// when there is none.
func Latest(runs []*Run, slug, from string) *Run {
	var best *Run
	for _, r := range runs {
		if r.Slug != slug || (from != "" && !strings.EqualFold(r.From, from)) {
			continue
		}
		if best == nil || !r.At.Before(best.At) {
			best = r
		}
	}
	return best
}
