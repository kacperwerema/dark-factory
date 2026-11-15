package postmortem

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/memory"
)

// The queue is the owner's view across post-mortems: every open action,
// by place, with the ones that say the same thing together. Refuse and
// Overtake answer one action, not a whole post-mortem as Decide does, and
// neither applies anything. Reopen undoes either, and Marked lists what it
// can undo.

// mu keeps two answers from rewriting a retro over each other in one
// process: the viewer is the only writer of outcome lines.
var mu sync.Mutex

// Item is one open action in the queue.
type Item struct {
	Action
	Issue, IssueTitle string
	// File is the retro's name, the handle a page posts back; Path its
	// place on disk.
	File, Path string
	Since      time.Time
}

// Group is the items of one place that say the same thing, or (Alone) the
// place's items that don't read like another one.
type Group struct {
	Place string
	Alone bool
	Items []Item
}

// PlaceGroups is one place's groups: biggest first, Alone last.
type PlaceGroups struct {
	Place  string
	Count  int
	Groups []Group
}

// SameThreshold is the word overlap (memory's words, Jaccard, over the
// action's title, Differently and Text) from which two actions of different
// issues read as one. On the live queue of 2026-11-05 (299 actions) 0.3
// joins 92 into 20 groups; at 0.25 unrelated bugfix and overlay actions
// start to join, and at 0.35 the branch_on_main copies fall apart.
// TestGroupJoinsCopies keeps real text.
const SameThreshold = 0.3

// GroupItems sorts items into places, biggest place first, and joins the
// ones that say the same thing. items keeps its order inside a group, so
// with Queue's order each group starts with its newest action.
func GroupItems(items []Item) []PlaceGroups {
	byPlace := map[string][]Item{}
	for _, it := range items {
		byPlace[it.Place] = append(byPlace[it.Place], it)
	}
	var out []PlaceGroups
	for place, its := range byPlace {
		out = append(out, PlaceGroups{Place: place, Count: len(its), Groups: sameGroups(place, its)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Place < out[j].Place
	})
	return out
}

func sameGroups(place string, items []Item) []Group {
	words := make([]map[string]bool, len(items))
	for i, it := range items {
		words[i] = map[string]bool{}
		for _, w := range memory.Words(it.Title + " " + it.Differently + " " + it.Text) {
			words[i][w] = true
		}
	}
	parent := make([]int, len(items))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	for i := range items {
		for j := 0; j < i; j++ {
			if items[i].Issue != items[j].Issue && overlap(words[i], words[j]) >= SameThreshold {
				parent[find(i)] = find(j)
			}
		}
	}
	members := map[int][]Item{}
	var roots []int
	for i, it := range items {
		r := find(i)
		if members[r] == nil {
			roots = append(roots, r)
		}
		members[r] = append(members[r], it)
	}
	var out []Group
	alone := Group{Place: place, Alone: true}
	for _, r := range roots {
		if len(members[r]) == 1 {
			alone.Items = append(alone.Items, members[r]...)
			continue
		}
		out = append(out, Group{Place: place, Items: members[r]})
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Items) > len(out[j].Items) })
	if len(alone.Items) > 0 {
		out = append(out, alone)
	}
	return out
}

func overlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// Refuse answers one open action of the retro at path with a Refused:
// line. The post-mortem's other actions stay open. Nothing is applied.
func Refuse(path, id, owner, note string, now time.Time) (string, error) {
	return settle(path, id, OutcomeRefused, outcomeLine("", owner, note, now))
}

// Overtake marks one open action as overtaken by the commit sha, with an
// Overtaken: line: the work happened elsewhere, so it no longer waits on
// the owner. The caller has checked sha (OnMain). Nothing is applied.
func Overtake(path, id, sha, owner, note string, now time.Time) (string, error) {
	return settle(path, id, OutcomeOvertaken, outcomeLine(sha+" · ", owner, note, now))
}

func outcomeLine(head, owner, note string, now time.Time) string {
	line := head + now.Format("2006-01-02") + " (" + owner + ")"
	if n := strings.Join(strings.Fields(note), " "); n != "" {
		line += ": " + n
	}
	return line
}

func settle(path, id, how, line string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	next, err := MarkAs(string(raw), id, how, line)
	if err != nil {
		return "", err
	}
	if err := gitfile.WriteAtomic(path, []byte(next), 0o644); err != nil {
		return "", err
	}
	return line, nil
}

// MarkedDays is how many calendar days back the owner's marks can be undone.
const MarkedDays = 7

// markRe reads a Refused: or Overtaken: line's date and author.
var markRe = regexp.MustCompile(`^(?:[0-9a-fA-F]{7,40} · )?(\d{4}-\d{2}-\d{2}) \(([^)]+)\)`)

// mark is the action's undoable line, how it ended and its text, or "".
// An applied action has none: what it made can't be taken back by a line.
func (a Action) mark() (how, line string) {
	switch {
	case a.Done || a.Applied != "":
		return "", ""
	case a.Refused != "":
		return OutcomeRefused, a.Refused
	case a.Overtaken != "":
		return OutcomeOvertaken, a.Overtaken
	}
	return "", ""
}

// Mark is the action's undoable line as the retro writes it, or "".
func (a Action) Mark() string {
	how, line := a.mark()
	if how == "" {
		return ""
	}
	return how + ": " + line
}

// Marked is what the owner can undo: the actions of the post-mortems Queue
// reads that owner refused or marked overtaken in the last MarkedDays
// calendar days, newest mark first. An older post-mortem isn't here:
// reopening its action would put it nowhere the owner looks.
func Marked(dir, owner string, now time.Time) []Item {
	since := now.AddDate(0, 0, -MarkedDays).Format("2006-01-02")
	type dated struct {
		item Item
		date string
	}
	var marks []dated
	for _, f := range newestFiles(dir) {
		w := f.waiting(0)
		for _, a := range Actions(f.body) {
			_, line := a.mark()
			m := markRe.FindStringSubmatch(line)
			if a.ID == "" || m == nil || m[2] != owner || m[1] < since {
				continue
			}
			marks = append(marks, dated{Item{Action: a, Issue: w.Issue, IssueTitle: w.Title, File: filepath.Base(f.path), Path: f.path, Since: w.Since}, m[1]})
		}
	}
	sort.SliceStable(marks, func(i, j int) bool { return marks[i].date > marks[j].date })
	out := make([]Item, len(marks))
	for i, m := range marks {
		out[i] = m.item
	}
	return out
}

// Reopen undoes the mark on one action of the retro at path: its Refused:
// or Overtaken: line becomes "Reopened: <time> (<owner>), was <the line>",
// so the action is open again and the file keeps what happened. Nothing
// else changes. It returns the text after "Reopened: ".
func Reopen(path, id, owner string, now time.Time) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	next, line, err := reopenIn(string(raw), id, owner, now)
	if err != nil {
		return "", err
	}
	if err := gitfile.WriteAtomic(path, []byte(next), 0o644); err != nil {
		return "", err
	}
	return line, nil
}

func reopenIn(raw, id, owner string, now time.Time) (string, string, error) {
	lines := strings.Split(raw, "\n")
	for _, a := range Actions(raw) {
		if a.ID != strings.ToUpper(strings.TrimSpace(id)) {
			continue
		}
		how, old := a.mark()
		if how == "" {
			return "", "", fmt.Errorf("%s has no mark to undo", a.ID)
		}
		at := -1
		for j := a.Line + 1; j < len(lines) && !headingRe.MatchString(lines[j]) && !itemRe.MatchString(lines[j]); j++ {
			if m := fieldRe.FindStringSubmatch(lines[j]); m != nil && strings.EqualFold(m[1], how) {
				at = j // the last one: the one Actions read
			}
		}
		if at < 0 {
			return "", "", fmt.Errorf("%s: no %s: line to undo", a.ID, how)
		}
		text := now.Format(time.RFC3339) + " (" + owner + "), was " + how + ": " + old
		indent := lines[at][:len(lines[at])-len(strings.TrimLeft(lines[at], " \t"))]
		lines[at] = indent + lineReopened + ": " + text
		return strings.Join(lines, "\n"), text, nil
	}
	return "", "", fmt.Errorf("no action %s in the post-mortem", id)
}

var shaRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// OnMain resolves sha to its short form when it is a commit on main
// in repo. It is the check behind an overtaken mark: a sha that isn't hex
// never reaches git.
func OnMain(repo, mainBranch, sha string) (string, error) {
	sha = strings.TrimSpace(sha)
	if !shaRe.MatchString(sha) {
		return "", fmt.Errorf("%q is not a commit sha (7 to 40 hex characters)", sha)
	}
	if strings.HasPrefix(mainBranch, "-") || mainBranch == "" {
		return "", fmt.Errorf("no main branch to check %s against", sha)
	}
	full, err := gitfile.Git(repo, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%s is not a commit in this repo", sha)
	}
	full = strings.TrimSpace(full)
	if _, err := gitfile.Git(repo, "merge-base", "--is-ancestor", full, mainBranch); err != nil {
		return "", fmt.Errorf("%s is not on %s", sha, mainBranch)
	}
	return gitdir.ShortSHA(full), nil
}
