package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) Store {
	t.Helper()
	return ProjectStore(t.TempDir())
}

func add(t *testing.T, s Store, d Draft, opt AddOptions) AddResult {
	t.Helper()
	r, err := s.Add(d, opt, t0)
	if err != nil {
		t.Fatalf("add %q: %v", d.Text, err)
	}
	return r
}

func load(t *testing.T, s Store) *Set {
	t.Helper()
	set, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestFindSecretRefusesKeysAndTokens(t *testing.T) {
	secrets := map[string]string{
		"private key":   "the deploy key is -----BEGIN OPENSSH PRIVATE KEY----- b3Blbn",
		"openai":        "use sk-proj-AbCdEf0123456789AbCdEf0123 for the bench",
		"anthropic":     "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123",
		"github":        "token " + "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789 works",
		"github pat":    "github_pat_" + "11ABCDEFG0123456789_abcdefghijkl",
		"slack":         "post with " + "xoxb-" + "1234567890-abcdefghij",
		"aws":           "AWS key AKIAIOSFODNN7EXAMPLE in the env",
		"google":        "AIzaSyA-1234567890abcdefghijklmnopqrstu is the maps key",
		"jwt":           "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"bearer":        "curl -H 'Authorization: Bearer abcdef0123456789abcdef'",
		"url password":  "the db is postgres://admin:hunter22@db.local/app",
		"password=":     "login with password=hunter22 on staging",
		"token:":        "the bot token: 9f8e7d6c5b4a",
		"api_key=":      "API_KEY=\"q1w2e3r4t5\"",
		"random":        "the cookie was Zm9vYmFyQmF6UXV4MTIzNDU2Nzg5MEFiQ2RFZkdo for the run",
		"client secret": "client_secret: 7a9b8c6d5e4f",
	}
	for name, text := range secrets {
		if kind := FindSecret(text); kind == "" {
			t.Errorf("%s: %q not refused", name, text)
		}
	}
}

func TestFindSecretLetsFactsAndReferencesThrough(t *testing.T) {
	for _, text := range []string{
		"The playtest runner finds Chrome for Testing itself on macOS",
		"merge main with -m \"chore(merge): …\"; the commit hook refuses wip: commits",
		"The GSC token is keychain:acme-gsc, set by the owner",
		"set token: env:GSC_TOKEN in the connector, never the value",
		"password: <ask the owner>",
		"fixed in commit 795b919a3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f (see the retro)",
		"see factory/shared-memory-between-agents-mem0-or-similar-open-source",
		"/Users/someone/Repo/ai-workflow-engine/.worktrees/factory/x is the worktree",
		"TestStatsPageFastOnABigProject is flaky under load; rerun once",
	} {
		if kind := FindSecret(text); kind != "" {
			t.Errorf("%q refused as %s", text, kind)
		}
	}
}

// Every write path refuses a secret, stores nothing and never echoes it.
func TestSecretsRefusedOnEveryWrite(t *testing.T) {
	s := newStore(t)
	secret := "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"
	_, err := s.Add(Draft{Text: "the token is " + secret}, AddOptions{}, t0)
	var se *SecretError
	if !errors.As(err, &se) || strings.Contains(err.Error(), secret) {
		t.Fatalf("add: err = %v", err)
	}
	if _, statErr := os.Stat(s.Path); !os.IsNotExist(statErr) {
		t.Error("a refused add wrote the log")
	}
	m := add(t, s, Draft{Text: "staging logs in through the owner's account"}, AddOptions{}).Memory
	if _, err := s.Edit(m.ID, "staging: password=hunter22", "agent-x", false, t0); !errors.As(err, &se) {
		t.Errorf("edit: err = %v", err)
	}
	if _, err := s.Add(Draft{Text: "now with password=hunter22 instead"}, AddOptions{Mode: ModeUpdates, Target: m.ID}, t0); !errors.As(err, &se) {
		t.Errorf("update: err = %v", err)
	}
	data, _ := os.ReadFile(s.Path)
	if strings.Contains(string(data), "hunter22") {
		t.Error("a refused write reached the log")
	}
}

func TestDraftChecks(t *testing.T) {
	s := newStore(t)
	for _, d := range []Draft{
		{Text: "  "},
		{Text: strings.Repeat("word ", MaxText)},
		{Text: "a fact", Kind: "gossip"},
		{Text: "a fact", Scope: "planet"},
		{Text: "a fact", Expires: "next week"},
	} {
		if _, err := s.Add(d, AddOptions{}, t0); err == nil {
			t.Errorf("draft %+v accepted", d)
		}
	}
}

func TestAddRecordsProvenanceKindAndScope(t *testing.T) {
	s := newStore(t)
	r := add(t, s, Draft{Text: "Chrome for Testing is found by the runner", Kind: KindHowto, Scope: "department:marketing",
		Project: "acme", System: "UI", Author: "agent-ui-x", Department: "Marketing",
		Source: Source{Issue: "ui/x", Commit: "abc1234", Comment: 3}, Tags: []string{"playtest"}}, AddOptions{})
	if r.Op != OpAdd || !strings.HasPrefix(r.Memory.ID, "m-") {
		t.Fatalf("result %+v", r)
	}
	m := load(t, s).Get(r.Memory.ID)
	if m == nil || m.Kind != KindHowto || m.Scope != "department:marketing" || m.Author != "agent-ui-x" || m.Created == "" {
		t.Fatalf("stored %+v", m)
	}
	if got := m.Source.String(); got != "issue ui/x · commit abc1234 · comment 3" {
		t.Errorf("source %q", got)
	}
	if def := add(t, s, Draft{Text: "a different thing entirely"}, AddOptions{}).Memory; def.Kind != KindFact || def.Scope != ScopeProject {
		t.Errorf("defaults %+v", def)
	}
}

// The update step (mem0's ADD/UPDATE/DELETE/NONE, decided by the agent).
func TestUpdateStep(t *testing.T) {
	s := newStore(t)
	first := add(t, s, Draft{Text: "The playtest runner needs Chrome for Testing on macOS"}, AddOptions{}).Memory

	// NONE: the same fact again (case and spacing aside) writes nothing.
	r := add(t, s, Draft{Text: "the playtest runner needs  chrome for testing on macOS."}, AddOptions{})
	if r.Op != OpNoop || r.Memory.ID != first.ID {
		t.Errorf("same fact: %+v", r)
	}

	// A near-duplicate is refused with the candidates and the ways on.
	_, err := s.Add(Draft{Text: "The playtest runner needs Chrome for Testing on macOS and Linux"}, AddOptions{}, t0)
	var sim *SimilarError
	if !errors.As(err, &sim) || len(sim.Similar) == 0 || sim.Similar[0].ID != first.ID {
		t.Fatalf("near duplicate: err = %v", err)
	}

	// UPDATE: same id, new text, history kept.
	r = add(t, s, Draft{Text: "The playtest runner finds Chrome for Testing itself on macOS"}, AddOptions{Mode: ModeUpdates, Target: first.ID})
	if r.Op != OpUpdate || r.Memory.ID != first.ID {
		t.Fatalf("update: %+v", r)
	}
	// SUPERSEDE: the old one is invalidated, not deleted, and linked.
	r = add(t, s, Draft{Text: "Playtests run in headless Chrome everywhere now"}, AddOptions{Mode: ModeSupersedes, Target: first.ID})
	if r.Op != OpSupersede {
		t.Fatalf("supersede: %+v", r)
	}
	set := load(t, s)
	old, nu := set.Get(first.ID), set.Get(r.Memory.ID)
	if old == nil || old.Valid(t0) || old.SupersededBy != nu.ID || nu.Supersedes != old.ID {
		t.Fatalf("after supersede old=%+v new=%+v", old, nu)
	}
	// ADD anyway.
	if r := add(t, s, Draft{Text: "Playtests run in headless Chrome everywhere now, also on CI"}, AddOptions{Mode: ModeNew}); r.Op != OpAdd {
		t.Errorf("--new: %+v", r)
	}
	if _, err := s.Add(Draft{Text: "x y z"}, AddOptions{Mode: ModeUpdates, Target: "m-nope"}, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("update unknown: %v", err)
	}

	hist := set.History(first.ID)
	var ops []string
	for _, e := range hist {
		ops = append(ops, e.Op)
	}
	if got := strings.Join(ops, ","); got != "add,update,supersede" {
		t.Errorf("history %s", got)
	}
	if hist[1].Old != "The playtest runner needs Chrome for Testing on macOS" || hist[1].Text != "The playtest runner finds Chrome for Testing itself on macOS" {
		t.Errorf("update event old/new: %+v", hist[1])
	}
}

func TestForgetPinUseAndExpiry(t *testing.T) {
	s := newStore(t)
	a := add(t, s, Draft{Text: "the release branch is release/vX.Y"}, AddOptions{}).Memory
	b := add(t, s, Draft{Text: "staging resets every night", Expires: "2026-11-15"}, AddOptions{}).Memory
	if err := s.Forget(a.ID, "wrong: releases cut from main", "agent-x", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.Pin(b.ID, "lead-acme", true, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUse([]string{b.ID}, "agent-y", t0); err != nil {
		t.Fatal(err)
	}
	set := load(t, s)
	if m := set.Get(a.ID); m.Valid(t0) || m.InvalidReason != "wrong: releases cut from main" {
		t.Errorf("forgotten %+v", m)
	}
	m := set.Get(b.ID)
	if !m.Pinned || m.PinnedBy != "lead-acme" || m.Uses != 1 || m.LastUsed == "" {
		t.Errorf("pinned/used %+v", m)
	}
	if !m.Valid(t0) || m.Valid(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) {
		t.Error("expiry not honoured")
	}
	if n := len(set.Active(t0)); n != 1 {
		t.Errorf("%d active, want 1", n)
	}
}

// The owner's delete forgets for real: the text leaves every event.
func TestOwnerDeleteScrubsTheText(t *testing.T) {
	s := newStore(t)
	m := add(t, s, Draft{Text: "the client's staging host is staging.private-client.example"}, AddOptions{}).Memory
	if _, err := s.Edit(m.ID, "the client's staging host is beta.private-client.example", "agent-x", false, t0); err != nil {
		t.Fatal(err)
	}
	keep := add(t, s, Draft{Text: "an unrelated fact stays"}, AddOptions{}).Memory
	if err := s.OwnerDelete(m.ID, t0); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(s.Path)
	if strings.Contains(string(data), "private-client") {
		t.Errorf("the deleted text is still in the log:\n%s", data)
	}
	set := load(t, s)
	if set.Get(m.ID) != nil || set.Get(keep.ID) == nil {
		t.Error("delete removed the wrong memory")
	}
	h := set.History(m.ID)
	if len(h) == 0 || h[len(h)-1].Op != OpOwnerDelete {
		t.Errorf("history %+v", h)
	}
}

func TestScopesAndVisibility(t *testing.T) {
	cases := []struct {
		scope string
		v     Viewer
		want  bool
	}{
		{ScopeProject, Viewer{}, true},
		{ScopeMachine, Viewer{}, true},
		{"department:marketing", Viewer{Department: "marketing"}, true},
		{"department:marketing", Viewer{Department: "sales"}, false},
		{"department:marketing", Viewer{}, false},
		{"department:marketing", Viewer{All: true}, true},
		{"agent:lead-acme", Viewer{Agent: "lead-acme"}, true},
		{"agent:lead-acme", Viewer{Agent: "agent-x"}, false},
	}
	for _, c := range cases {
		if got := (Memory{Scope: c.scope}).VisibleTo(c.v); got != c.want {
			t.Errorf("%s visible to %+v = %v", c.scope, c.v, got)
		}
	}
}

func TestProjectStoresAreSeparate(t *testing.T) {
	a, b := ProjectStore(filepath.Join(t.TempDir(), "a")), ProjectStore(filepath.Join(t.TempDir(), "b"))
	add(t, a, Draft{Text: "project A builds with make validate"}, AddOptions{})
	if n := len(load(t, b).Active(t0)); n != 0 {
		t.Errorf("project B sees %d", n)
	}
}

func TestMachineStoreFollowsXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	s := MachineStore()
	if s.Path != filepath.Join(dir, "ai-workflow-engine", "memory", "events.jsonl") {
		t.Errorf("path %s", s.Path)
	}
	if m := add(t, s, Draft{Text: "machine fact"}, AddOptions{}).Memory; !strings.HasPrefix(m.ID, "s-") || m.Scope != ScopeMachine || StoreOfID(m.ID) != "machine" {
		t.Errorf("machine add %+v", m)
	}
}

func TestConcurrentAddsKeepEveryMemory(t *testing.T) {
	s := newStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Add(Draft{Text: fmt.Sprintf("worker %d learned fact %d about lane %d", i, i*7, i*13)}, AddOptions{Mode: ModeNew}, t0); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if n := len(load(t, s).Active(t0)); n != 20 {
		t.Errorf("%d memories, want 20", n)
	}
}

func TestBM25RanksAndBoosts(t *testing.T) {
	mems := []Memory{
		{ID: "m-1", Text: "The playtest runner needs Chrome", Created: "2026-10-01T00:00:00Z"},
		{ID: "m-2", Text: "Chrome for Testing playtest shots are slow under load", System: "UI", Created: "2026-10-02T00:00:00Z"},
		{ID: "m-3", Text: "merge main with chore(merge)", Created: "2026-10-03T00:00:00Z"},
		{ID: "m-4", Text: "one browser at a time", Tags: []string{"playtest"}, Created: "2026-10-04T00:00:00Z"},
	}
	var r Ranker = BM25{}
	hits := r.Rank(Query{Text: "playtest runner chrome"}, mems)
	if len(hits) != 3 || hits[0].ID != "m-1" {
		t.Fatalf("hits %+v", hits)
	}
	for _, h := range hits {
		if h.ID == "m-3" {
			t.Error("unrelated memory matched")
		}
	}
	boosted := r.Rank(Query{Text: "chrome playtest", System: "UI"}, mems)
	if boosted[0].ID != "m-2" {
		t.Errorf("system boost: first %s", boosted[0].ID)
	}
	if got := r.Rank(Query{Text: "the and for"}, mems); len(got) != 0 {
		t.Errorf("stop words matched %+v", got)
	}
}

// TestSearchAt5000IsCheap bounds what start does: fold the log and rank.
func TestSearchAt5000IsCheap(t *testing.T) {
	s := newStore(t)
	if err := s.seed(5000, t0); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	set := load(t, s)
	hits := BM25{}.Rank(Query{Text: "playtest runner board 12", Limit: 5}, set.Active(t0))
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("load + rank of 5000 took %s", d)
	}
	if len(hits) != 5 {
		t.Errorf("%d hits", len(hits))
	}
}
