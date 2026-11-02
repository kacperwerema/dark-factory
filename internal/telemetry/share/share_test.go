package share

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/telemetry"
)

// sentinel is free text that must never reach a batch.
const sentinel = "SECRET Private Project /home/someone/repo me@example.com"

func testVocab() Vocab {
	m := telemetry.Manifest{
		GlobalFlags: []string{"config", "project", "json"},
		Commands: []telemetry.ManifestCommand{
			{Name: "transition", Flags: map[string][]string{"transition": {"to", "dry-run"}}},
			{Name: "data", Subcommands: []string{"add"}, Flags: map[string][]string{"data add": {"key"}}},
			{Name: "help"},
		},
		Topics: []string{"transitions"},
	}
	return NewVocab(m, []string{"development", "content"}, []string{"local-ci", "yolo"})
}

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(URLEnv, "")
	t.Setenv(telemetry.DisableEnv, "")
}

const day = "2026-10-30"

func ev(cmd, sub string, flags []string, exit int, class string) telemetry.Event {
	return telemetry.Event{V: 1, TS: day + "T10:00:00Z", DurMS: 12, Cmd: cmd, Sub: sub, Flags: flags, Exit: exit, ErrClass: class}
}

func TestBuildKeepsOnlyVocabularyNames(t *testing.T) {
	e1 := ev("transition", "", []string{"to", "project", sentinel, "dry-run"}, 1, "validation")
	e1.Project, e1.Issue, e1.Assignee, e1.Unknown = sentinel, sentinel, sentinel, sentinel
	e1.Safe = &telemetry.Safe{To: sentinel, Section: sentinel}
	e1.Agent, e1.ArgsHash, e1.PID = sentinel, "deadbeefdeadbeef", 42
	events := []telemetry.Event{
		e1,
		ev("transition", "", []string{"to"}, 0, ""),
		ev("data", "add", []string{"key"}, 0, ""),
		ev("data", sentinel, nil, 1, telemetry.ErrUnknownSubcommand), // kept as "data", sub dropped
		ev(sentinel, "", nil, 1, telemetry.ErrUnknownCommand),        // dropped
		ev("transition", "", nil, 1, sentinel),                       // class → other
		{V: 1, TS: "2026-10-29T23:59:59Z", Cmd: "transition"},        // other day
	}
	meta := Meta{Install: strings.Repeat("ab", 16), Ver: sentinel, OS: sentinel, Arch: "arm64",
		Templates: []string{"development", sentinel, "custom"}, Features: []string{"yolo", sentinel},
		Counts: Counts{Projects: 2, Issues: -5, Departments: 1}}
	b := Build(events, day, meta, testVocab())

	raw, _ := json.Marshal(b)
	for _, bad := range []string{"SECRET", "Private", "/home", "@", "deadbeef"} {
		if bytes.Contains(raw, []byte(bad)) {
			t.Fatalf("batch carries %q: %s", bad, raw)
		}
	}
	if err := Validate(b, testVocab()); err != nil {
		t.Fatalf("Build's batch fails Validate: %v\n%s", err, raw)
	}
	if b.Ver != "other" || b.OS != "other" || b.Counts.Issues != 0 {
		t.Errorf("ver/os/counts not cleaned: %+v", b)
	}
	if !reflect.DeepEqual(b.Templates, []string{"custom", "development"}) || !reflect.DeepEqual(b.Features, []string{"yolo"}) {
		t.Errorf("templates %v features %v", b.Templates, b.Features)
	}
	got := map[string]Command{}
	for _, c := range b.Commands {
		got[c.Key()] = c
	}
	if len(got) != 3 {
		t.Fatalf("commands %v, want transition, data, data add", got)
	}
	tr := got["transition"]
	if tr.Calls != 3 || tr.Fails != 2 || tr.Errors["validation"] != 1 || tr.Errors["other"] != 1 {
		t.Errorf("transition = %+v", tr)
	}
	if tr.Flags["to"] != 2 || tr.Flags["project"] != 1 || len(tr.Flags) != 3 {
		t.Errorf("transition flags = %v", tr.Flags)
	}
	if got["data"].Calls != 1 || got["data add"].Calls != 1 {
		t.Errorf("data = %+v, data add = %+v", got["data"], got["data add"])
	}
}

func validBatch() Batch {
	return Batch{V: 1, Install: strings.Repeat("0f", 16), Day: day, Ver: "v0.55.0-rc.2-3-gabc1234", OS: "linux", Arch: "amd64",
		Commands:  []Command{{Cmd: "transition", Calls: 3, Fails: 1, P50MS: 5, Flags: map[string]int{"to": 3}, Errors: map[string]int{"validation": 1}}},
		Templates: []string{"development"}, Features: []string{"local-ci"}, Counts: Counts{Projects: 1, Issues: 9}}
}

// Every string a batch can hold is refused by the collector's shape check
// when it looks like free text.
func TestValidateShapeRefusesFreeText(t *testing.T) {
	if err := ValidateShape(validBatch()); err != nil {
		t.Fatalf("valid batch refused: %v", err)
	}
	texts := []string{"hello world", "a/b", "Golf", "x@y", "my.repo", strings.Repeat("a", 41), "", "-x", "naïve", "a\nb"}
	mut := map[string]func(*Batch, string){
		"install":  func(b *Batch, s string) { b.Install = s },
		"day":      func(b *Batch, s string) { b.Day = s },
		"ver":      func(b *Batch, s string) { b.Ver = s },
		"os":       func(b *Batch, s string) { b.OS = s },
		"arch":     func(b *Batch, s string) { b.Arch = s },
		"cmd":      func(b *Batch, s string) { b.Commands[0].Cmd = s },
		"sub":      func(b *Batch, s string) { b.Commands[0].Sub = s + " " },
		"flag":     func(b *Batch, s string) { b.Commands[0].Flags = map[string]int{s: 1} },
		"errclass": func(b *Batch, s string) { b.Commands[0].Errors = map[string]int{s: 1} },
		"template": func(b *Batch, s string) { b.Templates = []string{s} },
		"feature":  func(b *Batch, s string) { b.Features = []string{s} },
	}
	for name, f := range mut {
		for _, s := range texts {
			b := validBatch()
			b.Commands = append([]Command(nil), b.Commands...)
			f(&b, s)
			if err := ValidateShape(b); err == nil {
				t.Errorf("%s = %q accepted", name, s)
			}
		}
	}
	b := validBatch()
	b.Commands[0].Fails = 4
	if ValidateShape(b) == nil {
		t.Error("fails > calls accepted")
	}
}

// stringRules lists every string-shaped place in a Batch and the rule that
// guards it. A new string field fails this test until it gets a rule in
// ValidateShape and a line here.
var stringRules = map[string]string{
	"Batch.Install":         "installRe",
	"Batch.Day":             "dayRe",
	"Batch.Ver":             "VersionRe",
	"Batch.OS":              "KnownOS",
	"Batch.Arch":            "KnownArch",
	"Batch.Commands.Cmd":    "tokenRe",
	"Batch.Commands.Sub":    "tokenRe",
	"Batch.Commands.Flags":  "tokenRe keys",
	"Batch.Commands.Errors": "ErrClasses keys",
	"Batch.Templates":       "tokenRe",
	"Batch.Features":        "tokenRe",
}

func TestBatchSchemaEveryStringFieldHasARule(t *testing.T) {
	var walk func(prefix string, typ reflect.Type)
	found := map[string]bool{}
	walk = func(prefix string, typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			path := prefix + "." + f.Name
			ft := f.Type
			switch ft.Kind() {
			case reflect.String:
				found[path] = true
			case reflect.Map:
				if ft.Key().Kind() != reflect.String || ft.Elem().Kind() != reflect.Int {
					t.Errorf("%s: maps must be name → count", path)
				}
				found[path] = true
			case reflect.Slice:
				switch ft.Elem().Kind() {
				case reflect.String:
					found[path] = true
				case reflect.Struct:
					walk(path, ft.Elem())
				default:
					t.Errorf("%s: unexpected slice", path)
				}
			case reflect.Struct:
				walk(path, ft)
			case reflect.Int, reflect.Int64:
			default:
				t.Errorf("%s: kind %s has no rule", path, ft.Kind())
			}
		}
	}
	walk("Batch", reflect.TypeOf(Batch{}))
	for p := range found {
		if _, ok := stringRules[p]; !ok {
			t.Errorf("%s is a string field with no free-text rule: add one to ValidateShape and stringRules", p)
		}
	}
	for p := range stringRules {
		if !found[p] {
			t.Errorf("stringRules lists %s, which Batch no longer has", p)
		}
	}
}

// Sharing is on by default (D-7), yet nothing is sent while no URL is set,
// and each off switch stops it.
func TestOnByDefaultOffSwitchesStopIt(t *testing.T) {
	isolate(t)
	st := Current()
	if !st.Settings.Share || !st.Settings.Default || st.Sending || !strings.Contains(st.Why, "no collector URL") {
		t.Fatalf("no settings file must mean on, but not sending without a URL: %+v", st)
	}
	if DefaultURL != "" {
		t.Fatal("the engine ships no collector URL; setting one is the owner's step")
	}
	t.Setenv(URLEnv, "http://127.0.0.1:1")
	if st := Current(); !st.Sending {
		t.Fatalf("on by default with a URL should send: %+v", st)
	}
	t.Setenv(telemetry.DisableEnv, "off")
	if st := Current(); st.Sending {
		t.Fatal("ISSUE_CLI_TELEMETRY=off must stop sharing")
	}
	t.Setenv(telemetry.DisableEnv, "")

	s1, created, err := EnsureInstall(time.Now())
	if err != nil || !created || !s1.Share || !installRe.MatchString(s1.Install) || s1.Notice == "" {
		t.Fatalf("first run: %+v created %v err %v", s1, created, err)
	}
	if _, again, _ := EnsureInstall(time.Now()); again {
		t.Fatal("EnsureInstall must write the file only once")
	}
	if _, err := TurnOff(time.Now()); err != nil {
		t.Fatal(err)
	}
	if st := Current(); st.Sending || st.Settings.Share || st.Settings.Install != "" {
		t.Fatalf("share off must stop sending and forget the id: %+v", st)
	}
	if _, again, _ := EnsureInstall(time.Now()); again {
		t.Fatal("an explicit off must not be reset to on")
	}
	s2, _ := TurnOn(time.Now())
	if s2.Install == s1.Install || !Current().Sending {
		t.Error("turning on again must send under a new install id")
	}
}

func TestPendingDays(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	got := PendingDays(now, map[string]bool{"2026-10-29": true})
	want := []string{"2026-10-24", "2026-10-25", "2026-10-26", "2026-10-27", "2026-10-28", "2026-10-30"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PendingDays = %v, want %v", got, want)
	}
}

// A local receiver (127.0.0.1, t.TempDir()) takes a batch end to end, after
// a failing first try, and refuses free text without storing it.
func TestCollectorReceivesAndValidates(t *testing.T) {
	dir := t.TempDir()
	var fail atomic.Int32
	fail.Store(1)
	h := Handler(Store{Dir: dir})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Add(-1) >= 0 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") {
		t.Fatalf("receiver not on loopback: %s", srv.URL)
	}
	var slept []time.Duration
	snd := Sender{Backoff: []time.Duration{time.Millisecond, time.Millisecond}, Sleep: func(d time.Duration) { slept = append(slept, d) }}
	b := validBatch()
	status, err := snd.Send(srv.URL+"/", b)
	if err != nil || status != http.StatusNoContent || len(slept) != 1 {
		t.Fatalf("send = %d, %v (slept %v)", status, err, slept)
	}
	// Same day again replaces, not duplicates.
	b.Counts.Issues = 10
	if _, err := snd.Send(srv.URL, b); err != nil {
		t.Fatal(err)
	}

	bad := validBatch()
	bad.Commands[0].Cmd = "rm -rf " + sentinel
	status, err = snd.Send(srv.URL, bad)
	if err == nil || status != http.StatusBadRequest {
		t.Fatalf("free text accepted: %d %v", status, err)
	}
	for _, body := range []string{
		`{"v":1,"install":"` + strings.Repeat("0", 32) + `","day":"2026-10-30","ver":"dev","os":"linux","arch":"amd64","commands":[],"templates":[],"features":[],"counts":{},"note":"hi"}`,
		`not json`,
	} {
		resp, err := http.Post(srv.URL+BatchPath, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q got %d", body, resp.StatusCode)
		}
	}
	resp, _ := http.Get(srv.URL + BatchPath)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET batch = %d", resp.StatusCode)
	}

	got, skipped, err := ReadStore(dir)
	if err != nil || skipped != 0 || len(got) != 1 || got[0].Counts.Issues != 10 {
		t.Fatalf("store = %+v skipped %d err %v", got, skipped, err)
	}
	raw, _ := os.ReadFile(Store{Dir: dir}.Path())
	if bytes.Contains(raw, []byte("SECRET")) {
		t.Fatal("refused batch reached the store")
	}
}

func TestSendDoesNotRetryARefusal(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "refused", http.StatusBadRequest)
	}))
	defer srv.Close()
	snd := Sender{Backoff: []time.Duration{0, 0, 0}, Sleep: func(time.Duration) {}}
	if _, err := snd.Send(srv.URL, validBatch()); err == nil || hits.Load() != 1 {
		t.Fatalf("err %v hits %d", err, hits.Load())
	}
	if _, err := Endpoint("file:///etc/passwd"); err == nil {
		t.Error("a non-http URL was accepted")
	}
}

func TestSentLog(t *testing.T) {
	isolate(t)
	if recs, err := ReadSent(); err != nil || len(recs) != 0 {
		t.Fatal(recs, err)
	}
	_ = AppendSent(SentRecord{At: "x", Day: "2026-10-29", URL: "u", OK: true, Batch: validBatch()})
	_ = AppendSent(SentRecord{At: "x", Day: "2026-10-30", URL: "u", OK: false})
	recs, _ := ReadSent()
	if len(recs) != 2 || !reflect.DeepEqual(SentDays(recs, "u"), map[string]bool{"2026-10-29": true}) {
		t.Fatalf("recs %+v", recs)
	}
}

func TestSummarize(t *testing.T) {
	a, b := validBatch(), validBatch()
	b.Install = strings.Repeat("1e", 16)
	b.Ver = "v0.54.0"
	b.Features = []string{"local-ci", "yolo"}
	b.Commands = []Command{{Cmd: "list", Calls: 10}, {Cmd: "transition", Calls: 1, Fails: 1, Errors: map[string]int{"usage": 1}}}
	c := b
	c.Day = "2026-10-28"
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	s := Summarize([]Batch{a, b, c}, now, 30)
	if s.Installs != 2 || s.Batches != 3 || s.Calls != 25 || s.Fails != 3 {
		t.Fatalf("summary %+v", s)
	}
	if s.Commands[0].Name != "list" || s.Commands[0].Count != 20 {
		t.Errorf("commands %v", s.Commands)
	}
	if len(s.Features) != 2 || s.Features[0] != (NameCount{"local-ci", 2}) {
		t.Errorf("features %v", s.Features)
	}
	if len(s.Versions) != 2 || len(s.PerDay) != 30 || s.PerDay[len(s.PerDay)-1] != (DayCount{day, 2}) {
		t.Errorf("versions %v perday tail %v", s.Versions, s.PerDay[len(s.PerDay)-1])
	}
	if len(s.Errors) != 1 || s.Errors[0].Command != "transition" || s.Errors[0].TopClass != "usage" {
		t.Errorf("errors %+v", s.Errors)
	}
}
