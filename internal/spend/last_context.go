package spend

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
)

// lastContextTail is how much of a transcript's end LastContext reads: the
// last turn is near the end, and a lead's transcript can run to many MB.
const lastContextTail = 4 << 20

// LastContext is the context of the newest turn of the session named agent
// in cwd's transcript folders (input + cache read + cache write, as spend
// --context counts a turn), with its time. ok is false when no transcript
// changed since since carries that name, or it has no counted turn. Only
// numbers are read.
func LastContext(projectsDir, cwd, agent string, since time.Time) (tokens int64, at time.Time, ok bool) {
	if projectsDir == "" || cwd == "" || agent == "" {
		return 0, time.Time{}, false
	}
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, folder := range bench.TranscriptFolders(projectsDir, cwd) {
		tops, _ := filepath.Glob(filepath.Join(folder, "*.jsonl"))
		for _, p := range tops {
			if st, err := os.Stat(p); err == nil && !st.ModTime().Before(since) {
				files = append(files, file{p, st.ModTime()})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files {
		if AgentName(f.path) != agent {
			continue
		}
		return lastTurn(f.path)
	}
	return 0, time.Time{}, false
}

// lastTurn reads the last assistant message with usage in the tail of path.
func lastTurn(path string) (int64, time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, time.Time{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, time.Time{}, false
	}
	partial := false
	if st.Size() > lastContextTail {
		if _, err := f.Seek(st.Size()-lastContextTail, io.SeekStart); err != nil {
			return 0, time.Time{}, false
		}
		partial = true
	}
	var tokens int64
	var at time.Time
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if partial {
			partial = false // the first line is cut
			continue
		}
		if !bytes.Contains(line, []byte(`"usage"`)) || !bytes.Contains(line, []byte(`"assistant"`)) {
			continue
		}
		var rec struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Model string `json:"model"`
				Usage *struct {
					Input      int64 `json:"input_tokens"`
					CacheRead  int64 `json:"cache_read_input_tokens"`
					CacheWrite int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" || rec.Message.Usage == nil || rec.Message.Model == "<synthetic>" {
			continue
		}
		u := rec.Message.Usage
		c := u.Input + u.CacheRead + u.CacheWrite
		if c == 0 {
			continue
		}
		tokens, found = c, true
		at, _ = time.Parse(time.RFC3339Nano, rec.Timestamp)
	}
	return tokens, at, found
}
