package main

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A new issue that repeats a done one: a sweep follow-up filed twice
// (retro sweep o-20261120-3387 item N4, cause C5: queue items 444 and 445
// re-filed two follow-ups of report f9bc that had shipped three days
// before). create warns and names the done issue; it never refuses, since
// a follow-up to a done issue is often what was meant.

// twinMinShared: a done issue's title is a twin when it
// shares at least twinMinShared (5) of the new title's words and at least
// half of the shorter title's words (memory.Words: stop words, short words
// and plurals folded). 444 shares 7 of 11, 445 5 of 10. twinMaxShown
// caps the warning's list.
const (
	twinMinShared = 5
	twinMaxShown  = 3
)

// twinHead is how much of a body's start names its source: the brief's
// first lines ("From the sweep report f9bc (…), item 1: …").
const twinHead = 600

var (
	twinOutputID  = regexp.MustCompile(`\bo-\d{8}-([0-9a-f]{4})\b`)
	twinShortID   = regexp.MustCompile(`(?i)\breport ([0-9a-f]{4})\b`)
	twinReportRef = regexp.MustCompile(`(?i)\breport ([a-z0-9][a-z0-9-]*/[a-z0-9-]+)`)
	twinItem      = regexp.MustCompile(`(?i)\b(?:item|follow-up) #?([a-z]?\d+)\b`)
)

// twinSource is the sweep source a body's first lines name, as
// "<id's last 4 hex>#<item>": an output id (o-20261117-f9bc), its short
// form (report f9bc) or the report issue's slug, which reportHex maps to
// its output. "" when the start names no source or no item.
func twinSource(body string, reportHex map[string]string) string {
	head := body
	if len(head) > twinHead {
		head = head[:twinHead]
	}
	item := twinItem.FindStringSubmatch(head)
	if item == nil {
		return ""
	}
	hex := ""
	if m := twinOutputID.FindStringSubmatch(head); m != nil {
		hex = m[1]
	} else if m := twinShortID.FindStringSubmatch(head); m != nil {
		hex = strings.ToLower(m[1])
	} else if m := twinReportRef.FindStringSubmatch(head); m != nil {
		hex = reportHex[strings.ToLower(m[1])]
	}
	if hex == "" {
		return ""
	}
	return hex + "#" + strings.ToUpper(item[1])
}

// reportHexes maps each output's issue slug to its id's last 4 hex, so a
// body naming the report issue finds the same source as one naming the
// output.
func reportHexes(proj *tracker.Project) map[string]string {
	list, _ := outputs.Load(proj)
	out := map[string]string{}
	for _, o := range list {
		if o.Issue == "" {
			continue
		}
		if m := twinOutputID.FindStringSubmatch(o.ID); m != nil {
			out[strings.ToLower(o.Issue)] = m[1]
		}
	}
	return out
}

// doneTwin is a done issue the new one may repeat, and why.
type doneTwin struct {
	Slug   string
	Source string // the shared sweep source, "" when only the title matched
	Shared int    // title words shared with the new title
	Of     int    // words in the new title
}

// titleShared is how many words two titles share, when that is enough to
// call them the same work (see twinMinShared), else 0.
func titleShared(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	in := map[string]bool{}
	for _, w := range b {
		in[w] = true
	}
	n := 0
	for _, w := range a {
		if in[w] {
			n++
		}
	}
	short := min(len(a), len(b))
	if n < twinMinShared || 2*n < short {
		return 0
	}
	return n
}

// findDoneTwins are the done issues a new issue (title, body) may repeat:
// the same sweep source and item first, then the most shared title words.
func findDoneTwins(proj *tracker.Project, issues []*tracker.Issue, slug, title, body string) []doneTwin {
	hexes := reportHexes(proj)
	src := twinSource(body, hexes)
	words := memory.Words(title)
	var out []doneTwin
	for _, is := range issues {
		if is.Slug == slug || !proj.IssueDone(is) {
			continue
		}
		t := doneTwin{Slug: is.Slug, Of: len(words)}
		if src != "" && twinSource(is.BodyRaw, hexes) == src {
			t.Source = src
		}
		t.Shared = titleShared(words, memory.Words(is.Title))
		if t.Source != "" || t.Shared > 0 {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Source != "") != (out[j].Source != "") {
			return out[i].Source != ""
		}
		if out[i].Shared != out[j].Shared {
			return out[i].Shared > out[j].Shared
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

// warnDoneTwins prints create's warning for the done issues the new issue
// display may repeat; nothing when there are none.
func warnDoneTwins(w io.Writer, display string, twins []doneTwin) {
	if len(twins) == 0 {
		return
	}
	fmt.Fprintf(w, "  ⚠ A done issue may already hold this; check what it shipped before building:\n")
	for i, t := range twins {
		if i == twinMaxShown {
			fmt.Fprintf(w, "    … and %d more\n", len(twins)-twinMaxShown)
			break
		}
		var why []string
		if t.Source != "" {
			hex, item, _ := strings.Cut(t.Source, "#")
			why = append(why, fmt.Sprintf("same sweep source (report %s, item %s)", hex, item))
		}
		if t.Shared > 0 {
			why = append(why, fmt.Sprintf("%d of %d title words", t.Shared, t.Of))
		}
		fmt.Fprintf(w, "    - %s: %s\n", t.Slug, strings.Join(why, ", "))
	}
	fmt.Fprintf(w, "  If it does: issue-cli close %s --fixed-by %s\n", display, twins[0].Slug)
}
