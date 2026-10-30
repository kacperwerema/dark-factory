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
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Version   string `json:"version"`
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
		if line.Version != "" {
			tr.Usage.ToolVersion = line.Version
		}
		if tr.Usage.SessionID == "" {
			tr.Usage.SessionID = line.SessionID
		}
		for _, c := range line.Message.Content {
			if c.Type != "tool_use" {
				continue
			}
			for _, key := range []string{"file_path", "notebook_path"} {
				if p, ok := c.Input[key].(string); ok && p != "" && !withinAny(roots, p) {
					outside[p] = true
				}
			}
		}
		u := line.Message.Usage
		if u == nil || (line.Message.ID != "" && seen[line.Message.ID]) {
			continue
		}
		seen[line.Message.ID] = true
		if line.Message.Model != "" && !strings.HasPrefix(line.Message.Model, "<") {
			models[line.Message.Model] = true
		}
		tr.Usage.Known = true
		tr.Usage.Input += u.Input
		tr.Usage.Output += u.Output
		tr.Usage.CacheRead += u.CacheRead
		tr.Usage.CacheWrite += u.CacheWrite
		tr.Usage.Turns++
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
