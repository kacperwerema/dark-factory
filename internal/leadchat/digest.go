package leadchat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The digest and the history search: what a rotated lead's old transcript
// comes down to (the owner's words, the lead's decisions, the commands that
// changed something), and a search over those and the transcripts still on
// disk that returns only the matching lines. Tool results are never read.

// Kinds of Entry.
const (
	WhoOwner   = "owner"
	WhoLead    = "lead"
	WhoCommand = "command"
)

// Entry is one line of a lead's history.
type Entry struct {
	At   time.Time
	Who  string
	Text string
}

// Digest caps.
const (
	DigestMaxLines = 400
	ownerMaxChars  = 2000
	lineMaxChars   = 300
)

// leadKeepRe picks the lead lines worth keeping in a digest: what was
// decided, approved, started, landed or ruled.
var leadKeepRe = regexp.MustCompile(`(?i)\b(decid|decision|approv|dispatch|merged|landed|land |rule|owner|blocked|rotat|ship|release|revert|next:|D-\d+)`)

// commandKeepRe picks the commands that changed something.
var commandKeepRe = regexp.MustCompile(`\bissue-cli\b.*\b(dispatch|approve|decide|transition|close|queue (add|move|block|done|pause|resume)|memory (add|pin|forget)|job (run|auto)|lead (spawn|rotate|state (note|ask|done))|release|comment|create|yolo)\b|\bgit\b.*\b(merge|push|tag|revert|reset)\b`)

// ReadEntries is what was said in the transcript at path since since, and
// every Bash command the session ran (its first line), oldest first. Tool
// results, subagents and harness lines are left out.
func ReadEntries(path string, since time.Time) []Entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			Type        string `json:"type"`
			Timestamp   string `json:"timestamp"`
			IsMeta      bool   `json:"isMeta"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.IsMeta || line.IsSidechain {
			continue
		}
		who := map[string]string{"user": WhoOwner, "assistant": WhoLead}[line.Type]
		if who == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, line.Timestamp)
		if err != nil || at.Before(since) {
			continue
		}
		if text := said(line.Message.Content); text != "" {
			out = append(out, Entry{At: at, Who: who, Text: text})
		}
		if who == WhoLead {
			for _, c := range commands(line.Message.Content) {
				out = append(out, Entry{At: at, Who: WhoCommand, Text: c})
			}
		}
	}
	return out
}

// commands are the Bash tool calls in an assistant message's blocks.
func commands(raw json.RawMessage) []string {
	var blocks []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Input struct {
			Command string `json:"command"`
		} `json:"input"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name == "Bash" && strings.TrimSpace(b.Input.Command) != "" {
			out = append(out, strings.TrimSpace(b.Input.Command))
		}
	}
	return out
}

// Digest is the compressed form of entries: every owner message, the lead
// lines that decide or report something, the commands that changed
// something; the newest DigestMaxLines of them.
func Digest(lead string, entries []Entry, from, to time.Time) string {
	var keep []string
	for _, e := range entries {
		switch e.Who {
		case WhoOwner:
			keep = append(keep, entryLine(e, ownerMaxChars))
		case WhoLead:
			if leadKeepRe.MatchString(e.Text) {
				keep = append(keep, entryLine(e, lineMaxChars))
			}
		case WhoCommand:
			if first, _, _ := strings.Cut(e.Text, "\n"); commandKeepRe.MatchString(first) {
				keep = append(keep, entryLine(Entry{At: e.At, Who: e.Who, Text: first}, lineMaxChars))
			}
		}
	}
	cut := 0
	if len(keep) > DigestMaxLines {
		cut = len(keep) - DigestMaxLines
		keep = keep[cut:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Digest: %s, %s to %s\n\n", lead, from.Local().Format("2006-01-02 15:04"), to.Local().Format("2006-01-02 15:04"))
	b.WriteString("The owner's words, the lead's decisions and the commands that changed something; no tool output. Search it with issue-cli lead history <words>.\n\n")
	if cut > 0 {
		fmt.Fprintf(&b, "(%d older lines left out: the transcript has them while it is on disk)\n", cut)
	}
	if len(keep) == 0 {
		b.WriteString("Nothing said.\n")
	}
	for _, l := range keep {
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

func entryLine(e Entry, n int) string {
	text := strings.Join(strings.Fields(e.Text), " ")
	if r := []rune(text); len(r) > n {
		text = string(r[:n]) + "…"
	}
	return fmt.Sprintf("- %s %s: %s", e.At.Local().Format("2006-01-02 15:04"), e.Who, text)
}

// digestLineRe reads a digest line back.
var digestLineRe = regexp.MustCompile(`^- (\d{4}-\d{2}-\d{2} \d{2}:\d{2}) (owner|lead|command): (.*)$`)

// ReadDigest is the entries of the digest file at path.
func ReadDigest(path string) []Entry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []Entry
	for _, l := range strings.Split(string(data), "\n") {
		m := digestLineRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		at, err := time.ParseInLocation("2006-01-02 15:04", m[1], time.Local)
		if err != nil {
			continue
		}
		out = append(out, Entry{At: at, Who: m[2], Text: m[3]})
	}
	return out
}

// DigestFiles are lead's digests in dir, oldest first.
func DigestFiles(dir, lead string) []string {
	all, _ := filepath.Glob(filepath.Join(dir, "*-"+lead+"*.digest.md"))
	var out []string
	for _, p := range all {
		// <date>-<lead>.digest.md or <date>-<lead>.<HHMM>.digest.md: not
		// another lead whose name starts with this one's.
		name := filepath.Base(p)
		if len(name) <= 11 {
			continue
		}
		if rest, ok := strings.CutPrefix(name[11:], lead); ok && strings.HasPrefix(rest, ".") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Hit is one matching line and where it came from.
type Hit struct {
	Entry
	Source string
}

// Search keeps the entries in which every word appears (case-insensitive),
// newest first, one per time and text.
func Search(words []string, sources map[string][]Entry) []Hit {
	var low []string
	for _, w := range words {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			low = append(low, w)
		}
	}
	seen := map[string]bool{}
	var out []Hit
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, src := range names {
		for _, e := range sources[src] {
			text := strings.ToLower(e.Text)
			ok := len(low) > 0
			for _, w := range low {
				if !strings.Contains(text, w) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			key := e.At.Local().Format("2006-01-02 15:04") + "\x00" + e.Who + "\x00" + oneLine(e.Text, lineMaxChars)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Hit{Entry: e, Source: src})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// HitLine is a hit as history prints it.
func HitLine(h Hit) string {
	return fmt.Sprintf("%s %s: %s  [%s]", h.At.Local().Format("2006-01-02 15:04"), h.Who, oneLine(h.Text, lineMaxChars), h.Source)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
