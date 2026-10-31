package memory

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Entities answer "who knows what" without a graph database (Design v2
// P17): an entity is a #topic or an @agent (or person) named in a
// memory's text or tags. Nothing about entities is written to the log;
// the index is folded from the current set on each load, so an edit or
// the owner's scrubbing delete changes it at once
// (factory/shared-memory-phase-4-entities-and-who-knows-what#D-1).

// Entity kinds.
const (
	EntityTopic = "topic"
	EntityAgent = "agent"
)

// Entity is one #topic or @name with the memories that carry it.
type Entity struct {
	Name string   `json:"name"`
	Kind string   `json:"kind"`
	IDs  []string `json:"ids"`
	// Kinds counts the memories by kind (fact, who, …).
	Kinds map[string]int `json:"kinds"`
	// Who is, for a #topic, the @names that share a who memory with it:
	// who knows about it.
	Who []string `json:"who,omitempty"`
}

// EntityKind is "topic" for #x, "agent" for @x, "" for anything else.
func EntityKind(name string) string {
	switch {
	case len(name) > 1 && name[0] == '#':
		return EntityTopic
	case len(name) > 1 && name[0] == '@':
		return EntityAgent
	}
	return ""
}

// NormEntity is an entity as the index keys it: lowercased, trailing
// punctuation dropped; "" when s is not one (#42, @, a word).
func NormEntity(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || (s[0] != '#' && s[0] != '@') {
		return ""
	}
	sigil, rest := s[:1], strings.ToLower(s[1:])
	rest = strings.TrimRight(rest, ".-/_")
	r := []rune(rest)
	if len(r) == 0 || !unicode.IsLetter(r[0]) {
		return ""
	}
	for _, c := range r {
		if !entityRune(c) {
			return ""
		}
	}
	return sigil + rest
}

func entityRune(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '-' || c == '_' || c == '.' || c == '/'
}

// Mentions are the #topics and @names written in text: a # or @ after
// the start, a space or an opening bracket, then a letter. "issue #42",
// "a@b.com", "page#top" and "# heading" are not mentions.
func Mentions(text string) []string {
	var out []string
	r := []rune(text)
	for i := 0; i < len(r); i++ {
		if r[i] != '#' && r[i] != '@' {
			continue
		}
		if i > 0 && !mentionStart(r[i-1]) {
			continue
		}
		j := i + 1
		for j < len(r) && entityRune(r[j]) {
			j++
		}
		if e := NormEntity(string(r[i:j])); e != "" {
			out = appendUnique(out, e)
		}
		i = j - 1
	}
	return out
}

func mentionStart(c rune) bool {
	return unicode.IsSpace(c) || strings.ContainsRune("([{\"'`,;", c)
}

// TagEntity is a tag as an entity: "@lead-x" stays an @name, anything
// else is a #topic.
func TagEntity(tag string) string {
	t := strings.TrimSpace(tag)
	if strings.HasPrefix(t, "@") || strings.HasPrefix(t, "#") {
		return NormEntity(t)
	}
	return NormEntity("#" + t)
}

// Entities are m's entities in first-seen order: its text's mentions,
// then its tags.
func (m Memory) Entities() []string {
	out := Mentions(m.Text)
	for _, t := range m.Tags {
		if e := TagEntity(t); e != "" {
			out = appendUnique(out, e)
		}
	}
	return out
}

// HasEntities reports whether m carries every one of names (normalised).
func (m Memory) HasEntities(names []string) bool {
	if len(names) == 0 {
		return true
	}
	have := map[string]bool{}
	for _, e := range m.Entities() {
		have[e] = true
	}
	for _, n := range names {
		if !have[NormEntity(n)] {
			return false
		}
	}
	return true
}

// SplitEntities splits a search query into its entity filters (#t, @x)
// and its words. A # or @ word that is not an entity stays a word.
func SplitEntities(query string) (entities []string, words string) {
	var rest []string
	for _, f := range strings.Fields(query) {
		if e := NormEntity(f); e != "" {
			entities = appendUnique(entities, e)
			continue
		}
		rest = append(rest, f)
	}
	return entities, strings.Join(rest, " ")
}

// Index is the entities of mems, most memories first, then by name. A
// memory counts under every entity it carries.
func Index(mems []Memory) []Entity {
	byName := map[string]*Entity{}
	who := map[string]map[string]bool{}
	for _, m := range mems {
		ents := m.Entities()
		var agents, topics []string
		for _, e := range ents {
			en := byName[e]
			if en == nil {
				en = &Entity{Name: e, Kind: EntityKind(e), Kinds: map[string]int{}}
				byName[e] = en
			}
			en.IDs = append(en.IDs, m.ID)
			en.Kinds[m.Kind]++
			if en.Kind == EntityAgent {
				agents = append(agents, e)
			} else {
				topics = append(topics, e)
			}
		}
		if m.Kind != KindWho {
			continue
		}
		for _, t := range topics {
			if who[t] == nil {
				who[t] = map[string]bool{}
			}
			for _, a := range agents {
				who[t][a] = true
			}
		}
	}
	out := make([]Entity, 0, len(byName))
	for name, en := range byName {
		for a := range who[name] {
			en.Who = append(en.Who, a)
		}
		sort.Strings(en.Who)
		out = append(out, *en)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].IDs) != len(out[j].IDs) {
			return len(out[i].IDs) > len(out[j].IDs)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// KindsLine is the entity's kinds in words: "who 2, fact 1".
func (e Entity) KindsLine() string {
	var parts []string
	for _, k := range Kinds {
		if n := e.Kinds[k]; n > 0 {
			parts = append(parts, k+" "+strconv.Itoa(n))
		}
	}
	return strings.Join(parts, ", ")
}
