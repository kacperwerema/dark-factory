package tracker

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/liveboard"
)

// The Settled rules (docs/departments.md) as decision records: the table
// testdata/settled-rules.tsv names each rule's sources and the scope and
// strength each has on the board; the lead writes them with decide
// --amend (the first 44 through the guided check script settled-rules-scopes
// of this issue, removed at ship; check-script finds it in history).

type settledRow struct{ Rule, Ref, Scope, Strength string }

func loadSettledTable(t *testing.T) []settledRow {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "settled-rules.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []settledRow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 4 {
			t.Fatalf("settled-rules.tsv: want rule, ref, scope, strength: %q", line)
		}
		rows = append(rows, settledRow{p[0], p[1], p[2], p[3]})
	}
	return rows
}

var (
	settledRuleRe = regexp.MustCompile(`^- \*\*(.+?)\*\*`)
	settledRefRe  = regexp.MustCompile(`([a-z0-9][a-z0-9/-]*/[a-z0-9-]+)?#D-(\d+)`)
)

// docSettledRules is each rule of docs/departments.md's Settled rules with
// the D-n refs its line cites ("#D-3" after a slug is that slug's).
func docSettledRules(t *testing.T) (order []string, refs map[string][]string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	refs = map[string][]string{}
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(line) == "## Settled rules"
			continue
		}
		m := settledRuleRe.FindStringSubmatch(line)
		if !in || m == nil {
			continue
		}
		rule := m[1]
		order = append(order, rule)
		refs[rule] = []string{}
		last := ""
		for _, r := range settledRefRe.FindAllStringSubmatch(line, -1) {
			if r[1] != "" {
				last = r[1]
			}
			refs[rule] = append(refs[rule], last+"#D-"+r[2])
		}
	}
	if len(order) == 0 {
		t.Fatal("docs/departments.md has no Settled rules")
	}
	return order, refs
}

// TestSettledRulesTableMatchesDoc: every rule and every ref the doc cites
// has a row, and every row is a rule and ref the doc cites; a rule citing
// no D-n has a "-" row; hard rows carry a real scope.
func TestSettledRulesTableMatchesDoc(t *testing.T) {
	rows := loadSettledTable(t)
	order, refs := docSettledRules(t)
	table := map[string]map[string]bool{}
	scopes := map[string]string{}
	for _, r := range rows {
		if table[r.Rule] == nil {
			table[r.Rule] = map[string]bool{}
		}
		table[r.Rule][r.Ref] = true
		if r.Ref == "-" {
			continue
		}
		if r.Strength != "hard" && r.Strength != "soft" {
			t.Errorf("%s: strength %q", r.Ref, r.Strength)
		}
		if s, err := NormalizeScope(r.Scope); err != nil || s == ScopeIssue {
			t.Errorf("%s: scope %q is not a real scope (%v)", r.Ref, r.Scope, err)
		}
		if prev, ok := scopes[r.Ref]; ok && prev != r.Scope+"/"+r.Strength {
			t.Errorf("%s: two rows disagree: %s and %s/%s", r.Ref, prev, r.Scope, r.Strength)
		}
		scopes[r.Ref] = r.Scope + "/" + r.Strength
	}
	for _, rule := range order {
		if table[rule] == nil {
			t.Errorf("rule %q has no row in settled-rules.tsv", rule)
			continue
		}
		for _, ref := range refs[rule] {
			if !table[rule][ref] {
				t.Errorf("rule %q cites %s, which has no row", rule, ref)
			}
		}
		if len(refs[rule]) == 0 && !table[rule]["-"] {
			t.Errorf("rule %q cites no D-n: give it a \"-\" row", rule)
		}
		delete(table, rule)
	}
	for rule := range table {
		t.Errorf("settled-rules.tsv names %q, which is not a Settled rule", rule)
	}
	for _, r := range rows {
		if r.Ref == "-" {
			continue
		}
		found := false
		for _, ref := range refs[r.Rule] {
			found = found || ref == r.Ref
		}
		if !found {
			t.Errorf("row %q %s: the rule doesn't cite it", r.Rule, r.Ref)
		}
	}
}

// liveBoard loads the live board (internal/liveboard: <repo>/issues beside
// the git common dir; LIVE_BOARD, or the older SETTLED_RULES_BOARD, names
// another, such as a copy to try the amends on), or skips on a fresh clone.
func liveBoard(t *testing.T) []*Issue {
	t.Helper()
	if dir := os.Getenv("SETTLED_RULES_BOARD"); dir != "" {
		t.Setenv(liveboard.Env, dir)
	}
	issues, err := LoadIssues(liveboard.Dir(t))
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

// settledBoard is the live board's decision index, skipping until the
// lead has run the script (no table ref has a scope yet).
func settledBoard(t *testing.T, rows []settledRow) *DecisionIndex {
	t.Helper()
	x := BuildDecisionIndex(liveBoard(t), nil)
	for _, r := range rows {
		if e := x.Lookup(r.Ref); e != nil && e.ScopeOrDefault() != ScopeIssue {
			return x
		}
	}
	t.Skip("no Settled-rules source is scoped yet: the lead applies settled-rules.tsv with decide --amend (issue-cli check-script factory/scope-the-settled-rules-as-hard-decisions-and-lint-check-decisions finds the script in history)")
	return nil
}

var ownerWordsMarkRe = regexp.MustCompile(`(?i)\b(provisional|yolo)\b`)

// TestSettledRulesScoped: on the board every table ref resolves, is not
// replaced and has the table's scope; a hard one carries the owner's quote
// with no provisional or YOLO mark anywhere on its line. The rules with no
// hard source are the owner's list, printed here.
func TestSettledRulesScoped(t *testing.T) {
	rows := loadSettledTable(t)
	x := settledBoard(t, rows)
	hardRule := map[string]bool{}
	var rules []string
	for _, r := range rows {
		if !hardRule[r.Rule] {
			hardRule[r.Rule] = false
		}
		if r.Ref == "-" {
			continue
		}
		e := x.Lookup(r.Ref)
		switch {
		case e == nil:
			t.Errorf("%s (%s) is not on the board", r.Ref, r.Rule)
			continue
		case e.ReplacedBy != nil:
			t.Errorf("%s (%s) is replaced by %s: the Settled rule should cite that", r.Ref, r.Rule, e.ReplacedBy.Ref)
		}
		if got := e.ScopeOrDefault(); got != r.Scope {
			t.Errorf("%s: scope %s, the table says %s", r.Ref, got, r.Scope)
		}
		hard := r.Strength == "hard"
		if e.Hard != hard {
			t.Errorf("%s: hard %v, the table says %s", r.Ref, e.Hard, r.Strength)
		}
		if hard {
			if e.Quote == "" || NeedsOwnerWords(e.Decision) || ownerWordsMarkRe.MatchString(e.How+" "+e.Text+" "+e.Quote) {
				t.Errorf("%s is hard without the owner's words: how %q, quote %q", r.Ref, e.How, e.Quote)
			}
			hardRule[r.Rule] = true
		}
	}
	for rule, hard := range hardRule {
		if !hard {
			rules = append(rules, rule)
		}
	}
	sort.Strings(rules)
	t.Logf("Settled rules with no hard source (soft: no D-n, or no owner words), for the owner:\n- %s", strings.Join(rules, "\n- "))
}

// TestSettledRulesForAFactoryIssue: a Factory issue in design gets every
// table ref scoped project or system:Factory in force, and at most 8 hard.
func TestSettledRulesForAFactoryIssue(t *testing.T) {
	rows := loadSettledTable(t)
	x := settledBoard(t, rows)
	issue := &Issue{Slug: "factory/settled-rules-probe", Title: "Probe", System: "Factory", Status: "in design", Type: "feature"}
	in := map[string]bool{}
	for _, f := range InForce(x, issue, "in design", InForceOptions{}) {
		in[f.Entry.Ref] = true
	}
	hard := 0
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Ref == "-" || seen[r.Ref] {
			continue
		}
		seen[r.Ref] = true
		reaches := r.Scope == ScopeProject || strings.EqualFold(r.Scope, "system:Factory") || strings.EqualFold(r.Scope, "department:product")
		if reaches && !in[r.Ref] {
			t.Errorf("%s (%s) doesn't reach a Factory issue in design", r.Ref, r.Scope)
		}
		if !reaches && in[r.Ref] {
			t.Errorf("%s (%s) reaches a Factory issue in design", r.Ref, r.Scope)
		}
		if in[r.Ref] && r.Strength == "hard" {
			hard++
		}
	}
	if hard > 8 {
		t.Errorf("a Factory issue in design gets %d hard settled rules, want at most 8", hard)
	}
	t.Logf("a Factory issue in design: %d hard settled rules in force", hard)
}
