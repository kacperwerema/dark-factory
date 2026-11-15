package bench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A golden task is solved again from its base commit, so the sandbox may
// hold only what a worker had at that date: the shared memory as it stood
// then, and no git ref past base. A memory written after the golden issue
// was done (or by its own run) can hold the answer. docs/benchmark.md
// "What is isolated".

// MemoryCut is what the sandbox's memory holds: counts only, never a
// memory's text.
type MemoryCut struct {
	// Cut is the base commit's committer time: events after it are left out.
	Cut time.Time `json:"cut"`
	// Project and Machine are the events copied into the sandbox's stores.
	Project int `json:"project"`
	Machine int `json:"machine"`
	// DroppedAfter are events after Cut; DroppedOwn events of a memory the
	// task's own issue or agent wrote (any date).
	DroppedAfter int `json:"dropped_after"`
	DroppedOwn   int `json:"dropped_own"`
}

// Line is the cut in words for bench report.
func (m *MemoryCut) Line() string {
	if m == nil {
		return ""
	}
	s := fmt.Sprintf("memory as of %s: %d project, %d machine", m.Cut.UTC().Format("2006-01-02 15:04"), m.Project, m.Machine)
	if m.DroppedAfter+m.DroppedOwn > 0 {
		s += fmt.Sprintf(" (%d later and %d of its own left out)", m.DroppedAfter, m.DroppedOwn)
	}
	return s
}

// xdgData is the sandbox's XDG_DATA_HOME: the machine memory store lives
// under it (memory.MachinePath), so the owner's is never read.
func (sb *sandbox) xdgData() string { return filepath.Join(sb.dir, "data") }

// cutMemory copies the project's and the machine's memory logs into the
// sandbox, as of the base commit: events at or before its committer time,
// none of a memory whose source is the task's issue or whose author is the
// task's agent. machineLog is the owner's machine store's path.
func (sb *sandbox) cutMemory(p *tracker.Project, t Task, machineLog string) (*MemoryCut, error) {
	out, err := git(sb.repo, "show", "-s", "--format=%ct", t.Base)
	if err != nil {
		return nil, err
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("base commit time: %w", err)
	}
	cut := &MemoryCut{Cut: time.Unix(sec, 0).UTC()}
	agent := tracker.AgentSessionName(t.Slug)
	kept, after, own, err := cutLog(memory.ProjectPath(p.IssueDir), memory.ProjectPath(filepath.Join(sb.tracker, "issues")), cut.Cut, t.Slug, agent)
	if err != nil {
		return nil, err
	}
	cut.Project, cut.DroppedAfter, cut.DroppedOwn = kept, after, own
	if machineLog != "" {
		dst := filepath.Join(sb.xdgData(), "ai-workflow-engine", "memory", "events.jsonl")
		kept, after, own, err := cutLog(machineLog, dst, cut.Cut, t.Slug, agent)
		if err != nil {
			return nil, err
		}
		cut.Machine = kept
		cut.DroppedAfter += after
		cut.DroppedOwn += own
	}
	return cut, nil
}

// cutLog writes src's events that pass the cut to dst, byte for byte, and
// counts them. A missing src writes nothing. Lines that don't parse, or
// whose time doesn't, are dropped as later (they can't be dated).
func cutLog(src, dst string, cut time.Time, slug, agent string) (kept, after, own int, err error) {
	data, err := os.ReadFile(src)
	if os.IsNotExist(err) {
		return 0, 0, 0, nil
	}
	if err != nil {
		return 0, 0, 0, err
	}
	type line struct {
		raw []byte
		ev  memory.Event
		ok  bool
	}
	var lines []line
	ownIDs := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var ev memory.Event
		ok := json.Unmarshal(raw, &ev) == nil
		lines = append(lines, line{raw: append([]byte(nil), raw...), ev: ev, ok: ok})
		if ok && ev.Memory != nil && (ev.Memory.Source.Issue == slug || (agent != "" && ev.By == agent)) {
			ownIDs[ev.ID] = true
			ownIDs[ev.Memory.ID] = true
		}
	}
	if err := sc.Err(); err != nil {
		return 0, 0, 0, err
	}
	var buf bytes.Buffer
	for _, l := range lines {
		if !l.ok {
			after++
			continue
		}
		at, perr := time.Parse(time.RFC3339, l.ev.At)
		switch {
		case ownIDs[l.ev.ID] || ownIDs[l.ev.Target] || (agent != "" && l.ev.By == agent):
			own++
		case perr != nil || at.After(cut):
			after++
		default:
			kept++
			buf.Write(l.raw)
			buf.WriteByte('\n')
		}
	}
	if kept == 0 {
		return kept, after, own, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, 0, 0, err
	}
	return kept, after, own, os.WriteFile(dst, buf.Bytes(), 0o644)
}

// cutRefs leaves the clone no ref past base: every ref (branches, tags) and
// the reflogs go, then each branch the clone had is set at base, so `git log
// --all` and `git reflog` reach nothing later. Objects stay reachable by
// hash through the alternates file; nothing names the merged commit.
func cutRefs(repo, base string) error {
	out, err := git(repo, "for-each-ref", "--format=%(refname)")
	if err != nil {
		return err
	}
	var del strings.Builder
	var heads []string
	for _, ref := range strings.Fields(out) {
		del.WriteString("delete " + ref + "\n")
		if strings.HasPrefix(ref, "refs/heads/") {
			heads = append(heads, ref)
		}
	}
	if del.Len() > 0 {
		if _, err := gitIn(repo, del.String(), "update-ref", "--stdin"); err != nil {
			return err
		}
	}
	if len(heads) == 0 {
		heads = []string{"refs/heads/main"}
	}
	for _, ref := range heads {
		if _, err := git(repo, "update-ref", "--no-deref", ref, base); err != nil {
			return err
		}
	}
	gitDir, err := git(repo, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(strings.TrimSpace(gitDir), "logs"))
}
