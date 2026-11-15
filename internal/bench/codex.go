package bench

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The agent CLIs a benchmark names (routing.KnownAgents).
const (
	AgentClaude = "claude"
	AgentCodex  = "codex"
)

// CodexSessions is where a Codex worker's rollout files are under home.
func CodexSessions(home string) string { return filepath.Join(home, ".codex", "sessions") }

// codexRollout is one line of a Codex rollout file, only the numbers and
// names read here: session_meta's cwd and CLI version, turn_context's model,
// token_count's running total. A message's text is never decoded.
type codexRollout struct {
	Type    string `json:"type"`
	Payload struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Cwd        string `json:"cwd"`
		CLIVersion string `json:"cli_version"`
		Model      string `json:"model"`
		Info       *struct {
			Total struct {
				Input      int64 `json:"input_tokens"`
				Cached     int64 `json:"cached_input_tokens"`
				CacheWrite int64 `json:"cache_write_input_tokens"`
				Output     int64 `json:"output_tokens"`
			} `json:"total_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// ReadCodexSessions sums the rollout files under dir of sessions run in one
// of roots, last changed at or after since (zero: any): each session's last
// running total, input without its cached part, which counts as cache read.
// Codex reports no cost, so USD stays unknown until Cost prices the model. A
// missing dir is a Transcript with Usage.Known false.
func ReadCodexSessions(dir string, since time.Time, roots ...string) Transcript {
	var tr Transcript
	models := map[string]bool{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if info, err := d.Info(); err != nil || info.ModTime().Before(since) {
			return nil
		}
		readCodexRollout(path, roots, &tr, models)
		return nil
	})
	var ms []string
	for m := range models {
		ms = append(ms, m)
	}
	sort.Strings(ms)
	tr.Usage.Model = strings.Join(ms, ",")
	if tr.Usage.Known {
		tr.Usage.Source = "rollout"
	}
	return tr
}

func readCodexRollout(path string, roots []string, tr *Transcript, models map[string]bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64<<20)
	var last *codexRollout
	ours := false
	id, version := "", ""
	turns := 0
	fileModels := map[string]bool{}
	for sc.Scan() {
		line := sc.Bytes()
		head := string(line[:min(len(line), 200)])
		if !ours && !strings.Contains(head, `"session_meta"`) {
			continue
		}
		// Only the three kinds of line read here are decoded.
		if !strings.Contains(head, `"session_meta"`) && !strings.Contains(head, `"turn_context"`) && !strings.Contains(head, `"token_count"`) {
			continue
		}
		var l codexRollout
		if json.Unmarshal(line, &l) != nil {
			continue
		}
		switch {
		case l.Type == "session_meta":
			if !withinAny(roots, l.Payload.Cwd) {
				return
			}
			ours, id, version = true, l.Payload.ID, l.Payload.CLIVersion
		case l.Type == "turn_context":
			if l.Payload.Model != "" {
				fileModels[l.Payload.Model] = true
			}
			turns++
		case l.Type == "event_msg" && l.Payload.Type == "token_count" && l.Payload.Info != nil:
			last = &l
		}
	}
	if !ours || last == nil {
		return
	}
	t := last.Payload.Info.Total
	u := &tr.Usage
	u.Known = true
	u.Input += max(t.Input-t.Cached, 0)
	u.CacheRead += t.Cached
	u.CacheWrite += t.CacheWrite
	u.Output += t.Output
	u.Turns += turns
	if u.SessionID == "" {
		u.SessionID = id
	}
	if version != "" {
		u.ToolVersion = version
	}
	for m := range fileModels {
		models[m] = true
	}
}
