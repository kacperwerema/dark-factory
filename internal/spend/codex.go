package spend

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Subscription is what one agent CLI's subscription spent for the project,
// by week (this week first): claude from Claude Code's transcripts (every
// box plus the owner's own sessions), codex from Codex's session logs.
type Subscription struct {
	Agent string `json:"agent"`
	Weeks []Week `json:"weeks"`
	// Read is false when nothing of this CLI's could be read (no folder).
	Read bool `json:"read"`
}

// ThisWeek is the subscription's tally this week.
func (s Subscription) ThisWeek() Tally { return s.Weeks[0].Tally }

// Total is the subscription's tally over every week shown.
func (s Subscription) Total() Tally {
	var t Tally
	for _, w := range s.Weeks {
		t.Add(w.Tally)
	}
	return t
}

// DefaultCodexSessionsDir is where Codex keeps its session logs:
// $CODEX_HOME/sessions, else ~/.codex/sessions. Only rollout-*.jsonl files
// under it are read, numbers only; nothing else in Codex's folder (its
// login, its config) is ever opened.
func DefaultCodexSessionsDir() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

// codexLine is one line of a Codex rollout file (codex-rs/protocol: a
// RolloutLine {timestamp, type, payload}). session_meta's payload has the
// session's cwd; an event_msg of type token_count has the last response's
// usage (TokenUsageInfo.last_token_usage).
type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
		Info *struct {
			Last struct {
				Input      int64 `json:"input_tokens"`
				Cached     int64 `json:"cached_input_tokens"`
				CacheWrite int64 `json:"cache_write_input_tokens"`
				Output     int64 `json:"output_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// codexWeeks sums the token usage of Codex sessions started in root (the
// project checkout or one of its worktrees) into weeks. Codex's input
// counts include the cached part, so input is the uncached rest. Tokens are
// left unpriced: a subscription is a flat fee, and no Codex price is
// bundled.
func codexWeeks(dir, root string, starts []time.Time, since, now time.Time) ([]Week, bool) {
	weeks := emptyWeeks(starts)
	if dir == "" || root == "" {
		return weeks, false
	}
	if _, err := os.Stat(dir); err != nil {
		return weeks, false
	}
	rootAbs, _ := filepath.Abs(root)
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
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
		codexFile(path, rootAbs, weeks, starts, since, now)
		return nil
	})
	return weeks, true
}

func codexFile(path, root string, weeks []Week, starts []time.Time, since, now time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	ours := false
	for sc.Scan() {
		line := sc.Bytes()
		// Only the two kinds of line read here are decoded.
		if !ours && !strings.Contains(string(line[:min(len(line), 200)]), `"session_meta"`) {
			continue
		}
		var l codexLine
		if json.Unmarshal(line, &l) != nil {
			continue
		}
		switch {
		case l.Type == "session_meta":
			ours = l.Payload.Cwd != "" && underRoot(l.Payload.Cwd, root)
			if !ours {
				return
			}
		case ours && l.Type == "event_msg" && l.Payload.Type == "token_count" && l.Payload.Info != nil:
			at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
			if err != nil || at.Before(since) || at.After(now) {
				continue
			}
			i := weekIndex(starts, at.In(now.Location()))
			if i < 0 {
				continue
			}
			u := l.Payload.Info.Last
			t := Tally{Input: max(u.Input-u.Cached, 0), CacheRead: u.Cached, CacheWrite: u.CacheWrite, Output: u.Output}
			t.Unpriced = t.Tokens()
			weeks[i].Add(t)
		}
	}
}

func underRoot(path, root string) bool {
	p, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
		return true
	}
	if r, err := filepath.EvalSymlinks(root); err == nil && r != root {
		if rp, err := filepath.EvalSymlinks(p); err == nil {
			return rp == r || strings.HasPrefix(rp, r+string(filepath.Separator))
		}
	}
	return false
}
