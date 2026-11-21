// Package reellegal is the legal floor a reel's script meets before it is
// rendered: nutrition and health claims (EU Regulation 1924/2006), the music's
// licence, the AI-content label (EU AI Act art. 50, Meta and TikTok rules), a
// presenter who is not a customer and gives no testimonial (Omnibus directive
// 2019/2161), the face's written consent, and no implied endorsement by a
// trademark's owner.
//
// It reads three things: the script's text (the issue's ## Script section,
// plus every string in the storyboard JSON when one is named), the reel's
// declarations (the ## Legal check section's "- key: value" lines) and the
// project's rules file (departments/<dept>/reel-legal.yaml, the phrase lists).
// It is plain Go over text: no shell, no network. The phrase lists are a
// floor, not legal advice: the owner still reviews the finished reel.
package reellegal

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// DeclarationsSection is the issue section the reel's declarations are in.
const DeclarationsSection = "Legal check"

// Rules is the project's rules file: the phrases each rule looks for.
type Rules struct {
	// Claims are nutrition and health claim phrases (stems are fine:
	// "odchudz" matches "odchudzanie"). A claim found holds the reel
	// unless a "claim: <phrase> = <authorisation>" line clears it.
	Claims []string `yaml:"claims"`
	// Testimonial phrases are a first-person result or recommendation; one
	// always holds the reel (a presenter is not a customer).
	Testimonial []string `yaml:"testimonial"`
	// CustomerPersonas are words a persona: line must not use (customer,
	// klientka, user).
	CustomerPersonas []string `yaml:"customer_personas"`
	// Marks are other companies' trademarks. A mark in the same sentence as
	// an Endorsement phrase always holds; a mark alone needs a disclaimer:.
	Marks       []string `yaml:"marks"`
	Endorsement []string `yaml:"endorsement"`
}

// ParseRules reads a rules file. A file with no phrases at all is an error:
// an empty copy would pass everything.
func ParseRules(data []byte) (Rules, error) {
	var r Rules
	if err := yaml.Unmarshal(data, &r); err != nil {
		return Rules{}, fmt.Errorf("rules file: %w", err)
	}
	if len(r.Claims)+len(r.Testimonial)+len(r.Marks) == 0 {
		return Rules{}, fmt.Errorf("rules file has no claims, testimonial or marks phrases")
	}
	return r, nil
}

// Finding is one reason the reel is held.
type Finding struct {
	Rule    string // claims, music, ai-label, testimonial, persona, consent, trademark
	Problem string
	Fix     string
}

func (f Finding) String() string { return f.Rule + ": " + f.Problem + " — " + f.Fix }

// Declarations are the reel's "- key: value" lines. Claims maps a folded
// claim phrase to its authorisation.
type Declarations struct {
	Values map[string]string
	Claims map[string]string
}

// Get is a declaration's value, "" when unset or still a <placeholder>.
func (d Declarations) Get(key string) string { return d.Values[key] }

// ParseDeclarations reads "- key: value" lines (a leading "- ", "* " or
// checkbox is optional). A value still in <angle brackets> is unset: the
// section the workflow appends starts that way. claim: lines may repeat.
func ParseDeclarations(section string) Declarations {
	d := Declarations{Values: map[string]string{}, Claims: map[string]string{}}
	for _, line := range strings.Split(section, "\n") {
		t := strings.TrimSpace(line)
		for _, p := range []string{"- [ ] ", "- [x] ", "- [X] ", "- ", "* "} {
			if strings.HasPrefix(t, p) {
				t = strings.TrimSpace(t[len(p):])
				break
			}
		}
		k, v, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.Trim(strings.TrimSpace(k), "`*"))
		v = strings.Trim(strings.TrimSpace(v), "`")
		if isPlaceholder(v) {
			v = ""
		}
		if strings.ContainsAny(k, " \t") || k == "" {
			continue
		}
		if k == "claim" {
			phrase, auth, _ := strings.Cut(v, "=")
			phrase, auth = fold(strings.TrimSpace(phrase)), strings.TrimSpace(auth)
			if phrase != "" && !isPlaceholder(auth) && !isUnknown(auth) {
				d.Claims[phrase] = auth
			}
			continue
		}
		if _, seen := d.Values[k]; !seen || d.Values[k] == "" {
			d.Values[k] = v
		}
	}
	return d
}

func isPlaceholder(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || (strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">"))
}

func isUnknown(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "?", "unknown", "tbd", "todo", "n/a", "none yet", "nie wiem":
		return true
	}
	return false
}

func yes(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "y", "true", "tak":
		return true
	}
	return false
}

func no(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "no", "n", "false", "nie", "none":
		return true
	}
	return false
}

// Input is what one check reads.
type Input struct {
	Script string
	Decl   Declarations
	Rules  Rules
	// FileOK reports whether a path a declaration names (consent:) is a
	// non-empty file in the project. Nil: every path is missing.
	FileOK func(path string) bool
}

// Check returns every reason to hold the reel, in a stable order; none
// means it passes.
func Check(in Input) []Finding {
	var out []Finding
	text := fold(in.Script)
	d := in.Decl

	// Claims: EU 1924/2006 allows only authorised claims.
	for _, p := range matches(text, in.Rules.Claims) {
		if cleared(p, d.Claims) {
			continue
		}
		out = append(out, Finding{"claims",
			fmt.Sprintf("%q is a nutrition or health claim (EU 1924/2006)", p),
			fmt.Sprintf("drop it, or add `claim: %s = <its authorisation: the EU register entry or the Annex condition it meets>` under ## %s", p, DeclarationsSection)})
	}

	// Music: none, or a track with its licence.
	switch music := d.Get("music"); {
	case music == "":
		out = append(out, Finding{"music", "no music: line",
			"add `music: none` or `music: <track>` with `music_licence: <where from, which licence>`"})
	case !no(music) && isUnknown(d.Get("music_licence")):
		out = append(out, Finding{"music", fmt.Sprintf("the track %q has no licence", music),
			"add `music_licence: <the library or seller and the licence that covers ads on Instagram/TikTok>`, or use `music: none`"})
	}

	// AI label: an AI person, a cloned voice or AI footage is labelled.
	face := strings.ToLower(d.Get("face"))
	ai := d.Get("ai_content")
	switch {
	case ai == "":
		out = append(out, Finding{"ai-label", "no ai_content: line",
			"add `ai_content: yes` (an AI person, a cloned voice or AI footage) or `ai_content: no`"})
	case (yes(ai) || face == "avatar") && !yes(d.Get("ai_label")):
		out = append(out, Finding{"ai-label", "AI-generated content with no label (EU AI Act art. 50, Meta and TikTok rules)",
			"add `ai_label: yes`: set the platform's AI label when publishing and say it on screen or in the caption"})
	}

	// Testimonial: a presenter gives no first-person result (Omnibus).
	for _, p := range matches(text, in.Rules.Testimonial) {
		out = append(out, Finding{"testimonial",
			fmt.Sprintf("%q reads as a customer's testimonial (Omnibus directive 2019/2161: no fake reviews)", p),
			"rewrite it as the presenter speaking about the app, not as someone who used it"})
	}

	// The face: who it is and their consent.
	switch face {
	case "":
		out = append(out, Finding{"persona", "no face: line",
			"add `face: none` (nobody on screen), `face: avatar` or `face: real`"})
	case "none", "no":
	default:
		persona := d.Get("persona")
		if persona == "" {
			out = append(out, Finding{"persona", "a face with no persona: line",
				"add `persona: presenter` (the face presents the app; it is never a customer)"})
		} else if bad := matches(fold(persona), in.Rules.CustomerPersonas); len(bad) > 0 {
			out = append(out, Finding{"persona",
				fmt.Sprintf("the persona %q presents the face as a customer (Omnibus directive 2019/2161)", persona),
				"use `persona: presenter`"})
		}
		consent := d.Get("consent")
		if consent == "" {
			out = append(out, Finding{"consent", "a face with no consent: line",
				"add `consent: <path to the person's written consent for commercial use of their face and voice>`"})
		} else if in.FileOK == nil || !in.FileOK(consent) {
			out = append(out, Finding{"consent", fmt.Sprintf("the consent %s is not a file in the project", consent),
				"put the signed consent at that path (it stays private: the issues folder or the project's own files)"})
		}
	}

	// Trademarks: no implied endorsement; a mention needs a disclaimer.
	var bare []string
	for _, s := range sentences(text) {
		marks := matches(s, in.Rules.Marks)
		if len(marks) == 0 {
			continue
		}
		if e := matches(s, in.Rules.Endorsement); len(e) > 0 {
			out = append(out, Finding{"trademark",
				fmt.Sprintf("%q with %q implies the mark's owner endorses the app", marks[0], e[0]),
				"say only what the app works with, never that the mark's owner approves, recommends or partners with it"})
			continue
		}
		bare = append(bare, marks...)
	}
	if len(bare) > 0 && d.Get("disclaimer") == "" {
		out = append(out, Finding{"trademark",
			fmt.Sprintf("%q is named with no disclaimer", dedupe(bare)[0]),
			"add `disclaimer: <on-screen text, e.g. \"Not affiliated with the mark's owner\">`"})
	}
	return out
}

// StoryboardText is every string value in a storyboard JSON, one per line,
// leaving out values under keys that name a file or an id (src, path, url,
// id), so file names don't read as claims.
func StoryboardText(data []byte) (string, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return "", err
	}
	var b strings.Builder
	var walk func(key string, v any)
	walk = func(key string, v any) {
		switch t := v.(type) {
		case string:
			k := strings.ToLower(key)
			for _, skip := range []string{"src", "path", "file", "url", "id", "voice", "template", "font", "color"} {
				if k == skip || strings.HasSuffix(k, "_"+skip) {
					return
				}
			}
			b.WriteString(t)
			b.WriteString("\n")
		case []any:
			for _, e := range t {
				walk(key, e)
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(k, t[k])
			}
		}
	}
	walk("", v)
	return b.String(), nil
}

// cleared: a declared claim's phrase contains the phrase found.
func cleared(found string, claims map[string]string) bool {
	for p := range claims {
		if strings.Contains(p, found) || strings.Contains(found, p) {
			return true
		}
	}
	return false
}

// matches is each phrase (folded) that starts a word in text, in the
// phrases' order, once each.
func matches(text string, phrases []string) []string {
	var out []string
	for _, raw := range phrases {
		p := fold(raw)
		if p == "" {
			continue
		}
		if atWordStart(text, p) {
			out = append(out, p)
		}
	}
	return dedupe(out)
}

func atWordStart(text, p string) bool {
	for i := 0; i < len(text); {
		j := strings.Index(text[i:], p)
		if j < 0 {
			return false
		}
		at := i + j
		if at == 0 {
			return true
		}
		prev := []rune(text[:at])
		if r := prev[len(prev)-1]; !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return true
		}
		i = at + 1
	}
	return false
}

func sentences(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n' || r == ';'
	})
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// fold lower-cases, drops diacritics (Polish, German, French, Spanish) and
// squeezes whitespace, so "Zdrowa" and "zdrowa", "ł" and "l" match.
func fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if f, ok := folds[r]; ok {
			r = f
		}
		if unicode.IsSpace(r) && r != '\n' {
			if !space {
				b.WriteRune(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

var folds = map[rune]rune{
	'ą': 'a', 'ć': 'c', 'ę': 'e', 'ł': 'l', 'ń': 'n', 'ó': 'o', 'ś': 's', 'ź': 'z', 'ż': 'z',
	'ä': 'a', 'ö': 'o', 'ü': 'u', 'á': 'a', 'à': 'a', 'â': 'a', 'é': 'e', 'è': 'e', 'ê': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ú': 'u', 'ù': 'u', 'û': 'u', 'ñ': 'n', 'ç': 'c', 'ô': 'o',
	'’': '\'', '‘': '\'',
}
