package docref

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Measuring which docs pages workers read and what their first hour costs,
// from Claude Code transcripts: numbers and paths only, nothing else of the
// transcript is kept.

// Read is one page and how many sessions read it.
type Read struct {
	Rel      string
	Sessions int
	Reads    int
}

// Report is what Measure found.
type Report struct {
	Sessions int
	// FirstHour is each session's first-hour tokens: the sum over its
	// assistant turns in the hour after its first of input + cache read +
	// cache write + output, one count per message id.
	FirstHour []int64
	Pages     []Read
}

// Median is the median first-hour tokens, 0 with no session.
func (r Report) Median() int64 {
	if len(r.FirstHour) == 0 {
		return 0
	}
	v := append([]int64(nil), r.FirstHour...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	if n := len(v); n%2 == 1 {
		return v[n/2]
	} else {
		return (v[n/2-1] + v[n/2]) / 2
	}
}

var docPathRe = regexp.MustCompile(`(?:^|[\s/"'])docs/([\w./-]+\.md)`)

// readCmdRe: a shell command that reads a file.
var readCmdRe = regexp.MustCompile(`\b(cat|sed|head|tail|less|grep|rg|awk)\b|rtk read`)

// Measure reads the top-level transcripts in the folders under projectsDir
// whose name starts with prefix (a project's worktree sessions), changed
// since since.
func Measure(projectsDir, prefix string, since time.Time) Report {
	var rep Report
	folders, _ := filepath.Glob(filepath.Join(projectsDir, "*"))
	pages := map[string]*Read{}
	for _, folder := range folders {
		if !strings.HasPrefix(filepath.Base(folder), prefix) {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(folder, "*.jsonl"))
		for _, f := range files {
			st, err := os.Stat(f)
			if err != nil || st.ModTime().Before(since) {
				continue
			}
			hour, read, ok := measureFile(f)
			if !ok {
				continue
			}
			rep.Sessions++
			rep.FirstHour = append(rep.FirstHour, hour)
			for rel, n := range read {
				r := pages[rel]
				if r == nil {
					r = &Read{Rel: rel}
					pages[rel] = r
				}
				r.Sessions++
				r.Reads += n
			}
		}
	}
	for _, r := range pages {
		rep.Pages = append(rep.Pages, *r)
	}
	sort.Slice(rep.Pages, func(i, j int) bool {
		a, b := rep.Pages[i], rep.Pages[j]
		if a.Sessions != b.Sessions {
			return a.Sessions > b.Sessions
		}
		if a.Reads != b.Reads {
			return a.Reads > b.Reads
		}
		return a.Rel < b.Rel
	})
	return rep
}

func measureFile(path string) (hour int64, reads map[string]int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, false
	}
	defer f.Close()
	reads = map[string]int{}
	seen := map[string]bool{}
	var t0 time.Time
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				ID    string `json:"id"`
				Usage *struct {
					Input      int64 `json:"input_tokens"`
					CacheRead  int64 `json:"cache_read_input_tokens"`
					CacheWrite int64 `json:"cache_creation_input_tokens"`
					Output     int64 `json:"output_tokens"`
				} `json:"usage"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "assistant" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, line.Timestamp)
		if err != nil {
			continue
		}
		if t0.IsZero() {
			t0 = at
		}
		ok = true
		if u := line.Message.Usage; u != nil && at.Sub(t0) < time.Hour && !seen[line.Message.ID] {
			seen[line.Message.ID] = true
			hour += u.Input + u.CacheRead + u.CacheWrite + u.Output
		}
		var blocks []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				FilePath string `json:"file_path"`
				Command  string `json:"command"`
			} `json:"input"`
		}
		if json.Unmarshal(line.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_use" {
				continue
			}
			var s string
			switch b.Name {
			case "Read":
				s = b.Input.FilePath
			case "Bash":
				if readCmdRe.MatchString(b.Input.Command) {
					s = b.Input.Command
				}
			}
			for _, m := range docPathRe.FindAllStringSubmatch(s, -1) {
				reads[m[1]]++
			}
		}
	}
	return hour, reads, ok
}
