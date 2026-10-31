package memory

import (
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMentionsFindTopicsAndNames(t *testing.T) {
	cases := map[string][]string{
		"ask @lead-dark-factory about #Playtest.":        {"@lead-dark-factory", "#playtest"},
		"(#ci/local) and [@owner], then #playtest again": {"#ci/local", "@owner", "#playtest"},
		"issue #42 and a@b.com and page#top":             nil,
		"# heading, @ alone, #-dash, @9lives":            nil,
		"#make.shot works; @agent-factory-x_y, ok":       {"#make.shot", "@agent-factory-x_y"},
		"\"#quoted\" `@code`":                            {"#quoted", "@code"},
		"trailing #slash/ and @dots...":                  {"#slash", "@dots"},
	}
	for text, want := range cases {
		if got := Mentions(text); !reflect.DeepEqual(got, want) {
			t.Errorf("Mentions(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestEntitiesJoinTextAndTags(t *testing.T) {
	m := Memory{Text: "@lead-x knows #playtest", Tags: []string{"Playtest", "@owner", "ci", "#42"}}
	want := []string{"@lead-x", "#playtest", "@owner", "#ci"}
	if got := m.Entities(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Entities = %v, want %v", got, want)
	}
	if !m.HasEntities([]string{"#CI", "@lead-x"}) || m.HasEntities([]string{"#chrome"}) {
		t.Fatal("HasEntities")
	}
}

func TestSplitEntitiesSeparatesFiltersFromWords(t *testing.T) {
	ents, words := SplitEntities("#playtest chrome @Lead-X #42 runner")
	if !reflect.DeepEqual(ents, []string{"#playtest", "@lead-x"}) || words != "chrome #42 runner" {
		t.Fatalf("SplitEntities = %v %q", ents, words)
	}
}

func TestIndexCountsAndWhoKnowsWhat(t *testing.T) {
	mems := []Memory{
		{ID: "m-1", Kind: KindWho, Text: "@lead-x knows #playtest and #chrome best"},
		{ID: "m-2", Kind: KindWho, Text: "ask @owner about #playtest judgement steps"},
		{ID: "m-3", Kind: KindFact, Text: "#playtest runs headless", Tags: []string{"ci"}},
		{ID: "m-4", Kind: KindFact, Text: "@bystander mentioned #chrome"},
	}
	idx := Index(mems)
	if idx[0].Name != "#playtest" || len(idx[0].IDs) != 3 {
		t.Fatalf("first entity = %+v, want #playtest with 3", idx[0])
	}
	if !reflect.DeepEqual(idx[0].Who, []string{"@lead-x", "@owner"}) {
		t.Fatalf("who knows #playtest = %v", idx[0].Who)
	}
	if idx[0].KindsLine() != "fact 1, who 2" {
		t.Fatalf("KindsLine = %q", idx[0].KindsLine())
	}
	byName := map[string]Entity{}
	for _, e := range idx {
		byName[e.Name] = e
	}
	if w := byName["#chrome"].Who; !reflect.DeepEqual(w, []string{"@lead-x"}) {
		t.Fatalf("who knows #chrome = %v (a fact's mention is not a who link)", w)
	}
	if byName["@owner"].Kind != EntityAgent || byName["#ci"].Kind != EntityTopic {
		t.Fatal("entity kinds")
	}
}

// The index is folded from the current set: an edit changes it, the
// owner's delete takes the entities with the text.
func TestIndexFollowsEditsAndOwnerDelete(t *testing.T) {
	s := newStore(t)
	r := add(t, s, Draft{Text: "@lead-x knows the #playtest runner", Kind: KindWho}, AddOptions{})
	if _, err := s.Edit(r.Memory.ID, "@owner knows the #chrome runner", "agent", false, t0); err != nil {
		t.Fatal(err)
	}
	names := func() []string {
		var out []string
		for _, e := range Index(load(t, s).Active(t0)) {
			out = append(out, e.Name)
		}
		return out
	}
	if got := strings.Join(names(), " "); got != "#chrome @owner" {
		t.Fatalf("after edit: %q", got)
	}
	if err := s.OwnerDelete(r.Memory.ID, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := names(); len(got) != 0 {
		t.Fatalf("after delete: %v", got)
	}
}

func TestIndexOfFiveThousandIsQuick(t *testing.T) {
	s := newStore(t)
	if err := s.seed(5000, t0); err != nil {
		t.Fatal(err)
	}
	mems := load(t, s).Active(t0)
	for i := range mems {
		mems[i].Text += " #playtest @lead-x"
	}
	start := time.Now()
	idx := Index(mems)
	if len(idx) != 2 || len(idx[0].IDs) != 5000 {
		t.Fatalf("index = %d entities", len(idx))
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("index of 5000 took %s", d)
	}
}

// AC3: entities are fully local. The entity code imports only the
// standard library's text helpers: no network, no process (a model or
// a server), no file of its own (it folds what the store already read).
func TestEntitiesNeedNoServer(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "entity.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"sort": true, "strconv": true, "strings": true, "unicode": true}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if !allowed[p] {
			t.Errorf("entity.go imports %s: entities must stay local text work, no server, model or extra store", p)
		}
	}
}
