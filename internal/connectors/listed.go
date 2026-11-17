package connectors

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Skills Claude Code lists without a folder on disk: the ones bundled with
// Claude Code and the ones synced from the claude.ai account
// (anthropic-skills:<name>). Their names are known only from what Claude
// Code showed a session: each transcript's skill_listing attachment near
// the top of its .jsonl. A lean worker's launch turns them off by these
// names (internal/agentrun/worker_setup.go).

// listedTranscripts is how many of the newest transcripts ListedSkills
// reads; listedHead is how much of each.
const (
	listedTranscripts = 20
	listedHead        = 256 << 10
)

var (
	transcriptDirChars = regexp.MustCompile(`[^A-Za-z0-9]`)
	listedSkillName    = regexp.MustCompile(`^[A-Za-z0-9._-]+(:[A-Za-z0-9._-]+)?$`)
)

// ListedSkills are the skill names Claude Code listed in the newest
// transcripts of sessions started in root or in a dot-folder below it (its
// worktrees: .worktrees/…, .claude/worktrees/…; a sibling project root-x is
// not read),
// sorted: the union of their skill_listing attachments. Only the first
// listedHead bytes of each file are read. nil when there is none.
func ListedSkills(root string) []string {
	if root == "" {
		return nil
	}
	base := filepath.Join(claudeConfigDir(), "projects")
	prefix := transcriptDirChars.ReplaceAllString(root, "-")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	type file struct {
		path string
		mod  int64
	}
	var files []file
	for _, e := range entries {
		if !e.IsDir() || (e.Name() != prefix && !strings.HasPrefix(e.Name(), prefix+"--")) {
			continue
		}
		dir := filepath.Join(base, e.Name())
		jsonl, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for _, p := range jsonl {
			if fi, err := os.Stat(p); err == nil {
				files = append(files, file{p, fi.ModTime().UnixNano()})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
	if len(files) > listedTranscripts {
		files = files[:listedTranscripts]
	}
	seen := map[string]bool{}
	for _, f := range files {
		for _, n := range transcriptSkills(f.path) {
			seen[n] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// transcriptSkills are the names in the skill_listing attachments within
// the head of one transcript.
func transcriptSkills(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, listedHead))
	sc.Buffer(make([]byte, 0, 64<<10), listedHead)
	var out []string
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"skill_listing"`)) {
			continue
		}
		var r struct {
			Attachment struct {
				Type    string `json:"type"`
				Content string `json:"content"`
			} `json:"attachment"`
		}
		if json.Unmarshal(line, &r) != nil || r.Attachment.Type != "skill_listing" {
			continue
		}
		out = append(out, ParseSkillListing(r.Attachment.Content)...)
	}
	return out
}

// ParseSkillListing reads the names out of a skill listing: one
// "- <name>: <description>" line per skill ("- <name>" when listed by
// name only).
func ParseSkillListing(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		rest, ok := strings.CutPrefix(line, "- ")
		if !ok {
			continue
		}
		name := rest
		if i := strings.Index(rest, ": "); i >= 0 {
			name = rest[:i]
		}
		name = strings.TrimSuffix(strings.TrimSpace(name), ":")
		if listedSkillName.MatchString(name) {
			out = append(out, name)
		}
	}
	return out
}
