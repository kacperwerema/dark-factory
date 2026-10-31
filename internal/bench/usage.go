package bench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// apiUsage is the usage block of Claude Code's JSON result and of each
// assistant message in its transcript.
type apiUsage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

// ParseResult reads the worker's stdout: Claude Code's
// `--output-format json` result object (the last JSON object on stdout).
// ok is false when stdout holds no result with usage.
func ParseResult(stdout []byte) (u Usage, ok bool) {
	var res struct {
		SessionID string         `json:"session_id"`
		Turns     int            `json:"num_turns"`
		CostUSD   *float64       `json:"total_cost_usd"`
		Usage     *apiUsage      `json:"usage"`
		Models    map[string]any `json:"modelUsage"`
	}
	data := bytes.TrimSpace(stdout)
	if i := bytes.LastIndex(data, []byte("\n{")); i >= 0 {
		data = data[i+1:]
	}
	if err := json.Unmarshal(data, &res); err != nil || res.Usage == nil {
		return Usage{}, false
	}
	u = Usage{
		Known: true, Source: "result", SessionID: res.SessionID, Turns: res.Turns,
		Input: res.Usage.Input, Output: res.Usage.Output,
		CacheRead: res.Usage.CacheRead, CacheWrite: res.Usage.CacheWrite,
	}
	if res.CostUSD != nil {
		u.USD, u.USDKnown = *res.CostUSD, true
	}
	var models []string
	for m := range res.Models {
		models = append(models, m)
	}
	sort.Strings(models)
	u.Model = strings.Join(models, ",")
	return u, true
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// TranscriptDir is where Claude Code keeps the transcripts of sessions run
// in cwd: ~/.claude/projects/<cwd with every non-alphanumeric as ->.
func TranscriptDir(home, cwd string) string {
	return filepath.Join(home, ".claude", "projects", nonAlnum.ReplaceAllString(cwd, "-"))
}

// Transcript is what a session's transcript tells: usage summed over its
// assistant messages (each message id once), and the files its tools wrote
// outside every root.
type Transcript struct {
	Usage   Usage
	Outside []string
}

// ReadTranscripts sums every transcript in dir (a worker may start more than
// one session). A missing dir is a Transcript with Usage.Known false.
func ReadTranscripts(dir string, roots ...string) Transcript {
	var tr Transcript
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	seen := map[string]bool{}
	outside := map[string]bool{}
	models := map[string]bool{}
	for _, f := range files {
		readTranscript(f, roots, &tr, seen, outside, models)
	}
	for p := range outside {
		tr.Outside = append(tr.Outside, p)
	}
	sort.Strings(tr.Outside)
	var ms []string
	for m := range models {
		ms = append(ms, m)
	}
	sort.Strings(ms)
	tr.Usage.Model = strings.Join(ms, ",")
	if tr.Usage.Known {
		tr.Usage.Source = "transcript"
	}
	return tr
}

func readTranscript(path string, roots []string, tr *Transcript, seen, outside, models map[string]bool) {
	EachMessage(path, seen, func(m Message) {
		if m.Version != "" {
			tr.Usage.ToolVersion = m.Version
		}
		if tr.Usage.SessionID == "" {
			tr.Usage.SessionID = m.SessionID
		}
		for _, p := range m.Wrote {
			if !withinAny(roots, p) {
				outside[p] = true
			}
		}
		if !m.Counted {
			return
		}
		if m.Model != "" {
			models[m.Model] = true
		}
		tr.Usage.Known = true
		tr.Usage.Input += m.Input
		tr.Usage.Output += m.Output
		tr.Usage.CacheRead += m.CacheRead
		tr.Usage.CacheWrite += m.CacheWrite
		tr.Usage.Turns++
	})
}

// Message is one assistant message of a transcript: the numbers only, never
// its text. Counted is false for a message without usage or one already
// counted (seen by message id), whose files written still count.
type Message struct {
	// ID is the API message id; "" when the transcript has none.
	ID                 string
	SessionID, Version string
	// Model is "" for Claude Code's own placeholders ("<synthetic>").
	Model                                string
	Timestamp                            time.Time
	Counted                              bool
	Input, Output, CacheRead, CacheWrite int64
	// Wrote are the file_path / notebook_path inputs of its tool calls.
	Wrote []string
}

// Tokens is every token the message used.
func (m Message) Tokens() int64 { return m.Input + m.Output + m.CacheRead + m.CacheWrite }

// EachMessage calls fn for each assistant message in the transcript at
// path. seen is the message ids already counted (shared across a session's
// files); nil counts each message of this file once. A file it can't open
// calls fn never.
func EachMessage(path string, seen map[string]bool, fn func(Message)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if seen == nil {
		seen = map[string]bool{}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Version   string `json:"version"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				ID      string    `json:"id"`
				Model   string    `json:"model"`
				Usage   *apiUsage `json:"usage"`
				Content []struct {
					Type  string         `json:"type"`
					Name  string         `json:"name"`
					Input map[string]any `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "assistant" {
			continue
		}
		m := Message{ID: line.Message.ID, SessionID: line.SessionID, Version: line.Version}
		if t, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
			m.Timestamp = t
		}
		for _, c := range line.Message.Content {
			if c.Type != "tool_use" {
				continue
			}
			for _, key := range []string{"file_path", "notebook_path"} {
				if p, ok := c.Input[key].(string); ok && p != "" {
					m.Wrote = append(m.Wrote, p)
				}
			}
		}
		if u := line.Message.Usage; u != nil && (line.Message.ID == "" || !seen[line.Message.ID]) {
			seen[line.Message.ID] = true
			m.Counted = true
			m.Input, m.Output, m.CacheRead, m.CacheWrite = u.Input, u.Output, u.CacheRead, u.CacheWrite
			if !strings.HasPrefix(line.Message.Model, "<") {
				m.Model = line.Message.Model
			}
		}
		fn(m)
	}
}

func withinAny(roots []string, path string) bool {
	for _, r := range roots {
		if within(r, path) {
			return true
		}
	}
	return len(roots) == 0
}

// within reports whether path is root or under it.
func within(root, path string) bool {
	if !filepath.IsAbs(path) {
		return true
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Cost fills u.USD from prices when the worker did not report it. A model
// with no price leaves the cost unknown.
func Cost(u *Usage, prices map[string]Price) {
	if u.USDKnown || !u.Known {
		return
	}
	p, ok := prices[u.Model]
	if !ok {
		return
	}
	u.USD = (float64(u.Input)*p.In + float64(u.Output)*p.Out +
		float64(u.CacheRead)*p.CacheRead + float64(u.CacheWrite)*p.CacheWrite) / 1e6
	u.USDKnown = true
}

// TranscriptSlugMax is the longest transcript folder name Claude Code keeps
// whole; a longer cwd's folder is its first TranscriptSlugMax characters,
// "-" and a hash.
const TranscriptSlugMax = 200

// TranscriptFolders are the folders under projectsDir (~/.claude/projects)
// that may hold the transcripts of sessions run in cwd: the cwd's slug, or
// for a long cwd every folder with its cut slug and a hash (and the uncut
// one when it is there).
func TranscriptFolders(projectsDir, cwd string) []string {
	if projectsDir == "" || cwd == "" {
		return nil
	}
	slug := nonAlnum.ReplaceAllString(cwd, "-")
	if len(slug) <= TranscriptSlugMax {
		return []string{filepath.Join(projectsDir, slug)}
	}
	out, _ := filepath.Glob(filepath.Join(projectsDir, slug[:TranscriptSlugMax]+"-*"))
	// An older Claude Code kept the whole name.
	if st, err := os.Stat(filepath.Join(projectsDir, slug)); err == nil && st.IsDir() {
		out = append([]string{filepath.Join(projectsDir, slug)}, out...)
	}
	return out
}

// SessionFiles are a transcript folder's sessions: each <id>.jsonl with its
// subagents' transcripts (<id>/subagents/*.jsonl), which count as the same
// session.
func SessionFiles(folder string) map[string][]string {
	out := map[string][]string{}
	tops, _ := filepath.Glob(filepath.Join(folder, "*.jsonl"))
	for _, f := range tops {
		id := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		subs, _ := filepath.Glob(filepath.Join(folder, id, "subagents", "*.jsonl"))
		out[f] = append([]string{f}, subs...)
	}
	return out
}

// PriceFor is the price of model: its own key, else the longest key that is
// a prefix of it ("claude-opus-5" prices "claude-opus-5-5"). ok is false
// when no key matches.
func PriceFor(model string, prices map[string]Price) (Price, bool) {
	if p, ok := prices[model]; ok {
		return p, true
	}
	best, found := "", false
	for k := range prices {
		if k != "" && strings.HasPrefix(model, k) && len(k) > len(best) {
			best, found = k, true
		}
	}
	return prices[best], found && model != ""
}

// USD is what the tokens cost at p.
func (p Price) USD(in, out, cacheRead, cacheWrite int64) float64 {
	return (float64(in)*p.In + float64(out)*p.Out + float64(cacheRead)*p.CacheRead + float64(cacheWrite)*p.CacheWrite) / 1e6
}
