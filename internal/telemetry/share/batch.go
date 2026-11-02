// Package share is the anonymous half of issue-cli telemetry: a daily
// aggregate of the local names-only events, sent to a collector. On by
// default (opt-out: ISSUE_CLI_TELEMETRY=off or `issue-cli telemetry share
// off`), announced once on the first run; nothing leaves the machine until a
// collector URL is set (the engine ships none).
//
// A batch is built from a closed allowlist, never by copying events: every
// string in it is a name from the client's vocabulary (commands, flags,
// error classes, bundled templates, catalog ids) and matches the token shape
// the collector enforces, so free text cannot ride along. Project names,
// issue slugs, assignees, the unknown token, safe values, pids and the args
// hash stay local.
package share

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/telemetry"
)

// SchemaVersion is the batch's "v".
const SchemaVersion = 1

// Batch is one install's usage for one UTC day.
type Batch struct {
	V       int    `json:"v"`
	Install string `json:"install"`
	Day     string `json:"day"`
	Ver     string `json:"ver"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`

	Commands  []Command `json:"commands"`
	Templates []string  `json:"templates"`
	Features  []string  `json:"features"`
	Counts    Counts    `json:"counts"`
}

// Command is one command (or command + subcommand) for the day.
type Command struct {
	Cmd    string         `json:"cmd"`
	Sub    string         `json:"sub,omitempty"`
	Calls  int            `json:"calls"`
	Fails  int            `json:"fails"`
	P50MS  int64          `json:"p50_ms"`
	Flags  map[string]int `json:"flags,omitempty"`
	Errors map[string]int `json:"errors,omitempty"`
}

// Key is "cmd" or "cmd sub".
func (c Command) Key() string {
	if c.Sub != "" {
		return c.Cmd + " " + c.Sub
	}
	return c.Cmd
}

// Counts are sizes of the install, numbers only.
type Counts struct {
	Projects    int `json:"projects"`
	Issues      int `json:"issues"`
	Departments int `json:"departments"`
}

// Limits on a batch, enforced by both ends.
const (
	MaxTokenLen  = 40
	MaxCommands  = 400
	MaxMapKeys   = 80
	MaxListItems = 200
	MaxBodyBytes = 64 << 10
	MaxCount     = 10_000_000
)

var (
	tokenRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	installRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
	dayRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// VersionRe is what "ver" may be: a plain build or a git describe of a
	// release tag. Anything else is sent as "other".
	VersionRe = regexp.MustCompile(`^(dev|v\d{1,4}\.\d{1,4}\.\d{1,4}(-rc\.\d{1,4})?(-\d{1,6}-g[0-9a-f]{7,40})?(-dirty)?)$`)
)

// KnownOS and KnownArch are the platforms a batch may name; any other is
// sent as "other".
var (
	KnownOS   = []string{"darwin", "linux", "windows", "freebsd", "openbsd", "netbsd", "other"}
	KnownArch = []string{"amd64", "arm64", "386", "arm", "riscv64", "ppc64le", "s390x", "other"}
)

// ErrClasses are the error classes a batch may count.
var ErrClasses = []string{
	telemetry.ErrUnknownCommand, telemetry.ErrUnknownSubcommand, telemetry.ErrUnknownTopic,
	telemetry.ErrUnknownFlag, telemetry.ErrFlagParse, telemetry.ErrUsage,
	telemetry.ErrProjectResolution, telemetry.ErrApprovalMissing, telemetry.ErrIssueNotFound,
	telemetry.ErrInvalidTransition, telemetry.ErrValidation, telemetry.ErrPanic, telemetry.ErrOther,
	"wait_timeout",
}

// CustomTemplate stands for a workflow not stamped from a bundled template.
const CustomTemplate = "custom"

// Vocab is the client's closed vocabulary: the only names a batch may hold.
type Vocab struct {
	Commands  map[string]bool            // "cmd" and "cmd sub"
	Flags     map[string]map[string]bool // command key → flag names (global flags included)
	Templates map[string]bool
	Features  map[string]bool
}

// NewVocab builds a Vocab from the CLI's manifest and the bundled template
// and catalog ids.
func NewVocab(m telemetry.Manifest, templates, features []string) Vocab {
	v := Vocab{
		Commands:  map[string]bool{},
		Flags:     map[string]map[string]bool{},
		Templates: map[string]bool{CustomTemplate: true},
		Features:  map[string]bool{},
	}
	add := func(key string, flags []string) {
		v.Commands[key] = true
		set := v.Flags[key]
		if set == nil {
			set = map[string]bool{}
			v.Flags[key] = set
		}
		for _, f := range m.GlobalFlags {
			set[f] = true
		}
		for _, f := range flags {
			set[f] = true
		}
	}
	for _, c := range m.Commands {
		add(c.Name, c.Flags[c.Name])
		for _, s := range c.Subcommands {
			add(c.Name+" "+s, c.Flags[c.Name+" "+s])
		}
	}
	// help topics are subs of help/process: allowed as subcommands.
	for _, t := range m.Topics {
		for _, h := range []string{"help", "process"} {
			if v.Commands[h] {
				add(h+" "+t, nil)
			}
		}
	}
	for _, t := range templates {
		v.Templates[t] = true
	}
	for _, f := range features {
		v.Features[f] = true
	}
	return v
}

// Meta is what the batch says about the install itself.
type Meta struct {
	Install   string
	Ver       string
	OS        string
	Arch      string
	Templates []string
	Features  []string
	Counts    Counts
}

// Build aggregates the events of one UTC day (YYYY-MM-DD) into a batch,
// keeping only names in vocab. The result always passes Validate.
func Build(events []telemetry.Event, day string, meta Meta, vocab Vocab) Batch {
	b := Batch{
		V:       SchemaVersion,
		Install: meta.Install,
		Day:     day,
		Ver:     cleanVer(meta.Ver),
		OS:      oneOf(meta.OS, KnownOS),
		Arch:    oneOf(meta.Arch, KnownArch),
		Counts:  clampCounts(meta.Counts),
	}
	type acc struct {
		c   Command
		dur []int64
	}
	byKey := map[string]*acc{}
	errOK := setOf(ErrClasses)
	for _, e := range events {
		t, err := time.Parse(time.RFC3339Nano, e.TS)
		if err != nil || t.UTC().Format("2006-01-02") != day {
			continue
		}
		key := e.CommandKey()
		if !vocab.Commands[key] || !tokenKey(key) {
			if !vocab.Commands[e.Cmd] || !tokenRe.MatchString(e.Cmd) {
				continue
			}
			key = e.Cmd // keep the command, drop a sub outside the vocabulary
		}
		a := byKey[key]
		if a == nil {
			cmd, sub, _ := strings.Cut(key, " ")
			a = &acc{c: Command{Cmd: cmd, Sub: sub, Flags: map[string]int{}, Errors: map[string]int{}}}
			byKey[key] = a
		}
		a.c.Calls++
		a.dur = append(a.dur, e.DurMS)
		if e.Exit != 0 {
			a.c.Fails++
			if errOK[e.ErrClass] {
				a.c.Errors[e.ErrClass]++
			} else {
				a.c.Errors[telemetry.ErrOther]++
			}
		}
		for _, f := range e.Flags {
			if vocab.Flags[key][f] && tokenRe.MatchString(f) && (len(a.c.Flags) < MaxMapKeys || a.c.Flags[f] > 0) {
				a.c.Flags[f]++
			}
		}
	}
	for _, a := range byKey {
		sort.Slice(a.dur, func(i, j int) bool { return a.dur[i] < a.dur[j] })
		a.c.P50MS = a.dur[(len(a.dur)-1)/2]
		if a.c.P50MS < 0 {
			a.c.P50MS = 0
		}
		if len(a.c.Flags) == 0 {
			a.c.Flags = nil
		}
		if len(a.c.Errors) == 0 {
			a.c.Errors = nil
		}
		b.Commands = append(b.Commands, a.c)
	}
	sort.Slice(b.Commands, func(i, j int) bool { return b.Commands[i].Key() < b.Commands[j].Key() })
	if len(b.Commands) > MaxCommands {
		b.Commands = b.Commands[:MaxCommands]
	}
	if b.Commands == nil {
		b.Commands = []Command{}
	}
	b.Templates = filterList(meta.Templates, vocab.Templates)
	b.Features = filterList(meta.Features, vocab.Features)
	return b
}

// Validate is the client's check: the shape rules plus every name in vocab.
func Validate(b Batch, vocab Vocab) error {
	if err := ValidateShape(b); err != nil {
		return err
	}
	for _, c := range b.Commands {
		if !vocab.Commands[c.Key()] {
			return fmt.Errorf("command %q is not an issue-cli command", c.Key())
		}
		for f := range c.Flags {
			if !vocab.Flags[c.Key()][f] {
				return fmt.Errorf("flag %q is not a flag of %q", f, c.Key())
			}
		}
	}
	for _, t := range b.Templates {
		if !vocab.Templates[t] {
			return fmt.Errorf("template %q is not a bundled template", t)
		}
	}
	for _, f := range b.Features {
		if !vocab.Features[f] {
			return fmt.Errorf("feature %q is not a catalog id", f)
		}
	}
	return nil
}

// ValidateShape is the collector's check, which needs no vocabulary: every
// string is a short lowercase token (no spaces, slashes, dots, @ or capitals
// outside ver), every number in range, every list capped.
func ValidateShape(b Batch) error {
	if b.V != SchemaVersion {
		return fmt.Errorf("v must be %d", SchemaVersion)
	}
	if !installRe.MatchString(b.Install) {
		return fmt.Errorf("install must be 32 lowercase hex characters")
	}
	if !dayRe.MatchString(b.Day) {
		return fmt.Errorf("day must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", b.Day); err != nil {
		return fmt.Errorf("day is not a date")
	}
	if b.Ver != "other" && !VersionRe.MatchString(b.Ver) {
		return fmt.Errorf("ver is not a version")
	}
	if !setOf(KnownOS)[b.OS] {
		return fmt.Errorf("os is not a known platform")
	}
	if !setOf(KnownArch)[b.Arch] {
		return fmt.Errorf("arch is not a known platform")
	}
	if len(b.Commands) > MaxCommands {
		return fmt.Errorf("too many commands")
	}
	errOK := setOf(ErrClasses)
	seen := map[string]bool{}
	for _, c := range b.Commands {
		if !tokenRe.MatchString(c.Cmd) || (c.Sub != "" && !tokenRe.MatchString(c.Sub)) {
			return fmt.Errorf("command names must be lowercase tokens")
		}
		if seen[c.Key()] {
			return fmt.Errorf("command %q twice", c.Key())
		}
		seen[c.Key()] = true
		if !inRange(c.Calls) || !inRange(c.Fails) || c.Fails > c.Calls || c.P50MS < 0 || c.P50MS > 24*3600*1000 {
			return fmt.Errorf("command %q: numbers out of range", c.Key())
		}
		if len(c.Flags) > MaxMapKeys || len(c.Errors) > len(ErrClasses) {
			return fmt.Errorf("command %q: too many keys", c.Key())
		}
		for f, n := range c.Flags {
			if !tokenRe.MatchString(f) || !inRange(n) {
				return fmt.Errorf("command %q: flag names must be lowercase tokens", c.Key())
			}
		}
		for e, n := range c.Errors {
			if !errOK[e] || !inRange(n) {
				return fmt.Errorf("command %q: unknown error class", c.Key())
			}
		}
	}
	for _, l := range [][]string{b.Templates, b.Features} {
		if len(l) > MaxListItems {
			return fmt.Errorf("too many list items")
		}
		for _, s := range l {
			if !tokenRe.MatchString(s) {
				return fmt.Errorf("template and feature names must be lowercase tokens")
			}
		}
	}
	for _, n := range []int{b.Counts.Projects, b.Counts.Issues, b.Counts.Departments} {
		if !inRange(n) {
			return fmt.Errorf("counts out of range")
		}
	}
	return nil
}

func tokenKey(key string) bool {
	cmd, sub, has := strings.Cut(key, " ")
	return tokenRe.MatchString(cmd) && (!has || tokenRe.MatchString(sub))
}

func inRange(n int) bool { return n >= 0 && n <= MaxCount }

func cleanVer(v string) string {
	if VersionRe.MatchString(v) {
		return v
	}
	return "other"
}

func oneOf(s string, known []string) string {
	if setOf(known)[s] {
		return s
	}
	return "other"
}

func clampCounts(c Counts) Counts {
	clamp := func(n int) int {
		if n < 0 {
			return 0
		}
		if n > MaxCount {
			return MaxCount
		}
		return n
	}
	return Counts{Projects: clamp(c.Projects), Issues: clamp(c.Issues), Departments: clamp(c.Departments)}
}

func filterList(in []string, allowed map[string]bool) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if allowed[s] && tokenRe.MatchString(s) && !seen[s] && len(out) < MaxListItems {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func setOf(l []string) map[string]bool {
	m := make(map[string]bool, len(l))
	for _, s := range l {
		m[s] = true
	}
	return m
}
