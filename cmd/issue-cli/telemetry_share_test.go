package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/telemetry"
	"github.com/kacperwerema/dark-factory/internal/telemetry/share"
)

// shareReceiver is a local collector on 127.0.0.1 storing into t.TempDir().
func shareReceiver(t *testing.T) (url, dir string, hits *atomic.Int32) {
	t.Helper()
	dir = t.TempDir()
	hits = &atomic.Int32{}
	h := share.Handler(share.Store{Dir: dir})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, dir, hits
}

// yesterdayEvents writes local events dated yesterday, with free text in
// every field that holds any, into the project's telemetry file.
func yesterdayEvents(t *testing.T, workDir string) {
	t.Helper()
	ts := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour).Add(9 * time.Hour).Format(time.RFC3339Nano)
	sink := telemetry.FileSink{Path: telemetry.ProjectPath(workDir)}
	for _, e := range []telemetry.Event{
		{V: 1, TS: ts, Cmd: "transition", Flags: []string{"to", "project"}, Exit: 1, ErrClass: "validation",
			Project: "SENTINEL-PROJ", Issue: "SENTINEL-ISSUE", Assignee: "SENTINEL-AGENT", Safe: &telemetry.Safe{To: "SENTINEL TO"}},
		{V: 1, TS: ts, Cmd: "list", Flags: []string{"status"}},
		{V: 1, TS: ts, Cmd: "", Unknown: "SENTINEL-UNKNOWN", Exit: 1, ErrClass: "unknown_command"},
	} {
		if err := sink.Write(e); err != nil {
			t.Fatal(err)
		}
	}
}

func noBackoff(t *testing.T) {
	old := shareSender
	shareSender = share.Sender{Backoff: []time.Duration{}, Sleep: func(time.Duration) {}}
	t.Cleanup(func() { shareSender = old })
}

// On by default, but with no collector URL (the engine ships none) nothing
// is sent; with a URL, each off switch stops it.
func TestShareSendsNothingWithoutAURLOrWhenOff(t *testing.T) {
	isolateTelemetryEnv(t)
	cfg, workDir := telemetryFixture(t, "")
	yesterdayEvents(t, workDir)
	url, _, hits := shareReceiver(t)
	noBackoff(t)

	out, err := invoke(t, "--config", cfg, "telemetry", "send")
	if err == nil || !strings.Contains(err.Error(), "no collector URL") {
		t.Fatalf("send without a URL: out %q err %v", out, err)
	}
	t.Setenv(share.URLEnv, url)
	t.Setenv(telemetry.DisableEnv, "off")
	if _, err := invoke(t, "--config", cfg, "telemetry", "send"); err == nil {
		t.Error("send went through with ISSUE_CLI_TELEMETRY=off")
	}
	t.Setenv(telemetry.DisableEnv, "")
	if out, err := invoke(t, "--config", cfg, "telemetry", "share", "off"); err != nil || !strings.Contains(out, "Sharing off") {
		t.Fatalf("share off: %q %v", out, err)
	}
	if _, err := invoke(t, "--config", cfg, "telemetry", "send"); err == nil {
		t.Error("send went through after share off")
	}
	if out, _ := invoke(t, "--config", cfg, "telemetry", "path"); !strings.Contains(out, "share: off (turn it back on: issue-cli telemetry share on)") {
		t.Errorf("telemetry path doesn't show the off state:\n%s", out)
	}
	for i := 0; i < 3; i++ {
		_, _ = invoke(t, "--config", cfg, "list")
	}
	if hits.Load() != 0 {
		t.Fatalf("collector got %d requests while off", hits.Load())
	}
}

// The first run prints one notice line and records it; later runs and an
// off install print nothing.
func TestShareNoticeOnce(t *testing.T) {
	isolateTelemetryEnv(t)
	var w strings.Builder
	shareNotice(&w, time.Now())
	if !strings.Contains(w.String(), "anonymous usage stats are on") || !strings.Contains(w.String(), "issue-cli telemetry share off") || strings.Count(w.String(), "\n") != 1 {
		t.Fatalf("notice = %q", w.String())
	}
	if s, _ := share.Load(); s.Notice == "" || s.Install == "" || s.Default {
		t.Fatalf("notice not recorded: %+v", s)
	}
	w.Reset()
	shareNotice(&w, time.Now())
	if w.Len() != 0 {
		t.Fatalf("second notice: %q", w.String())
	}

	isolateTelemetryEnv(t)
	t.Setenv(telemetry.DisableEnv, "off")
	shareNotice(&w, time.Now())
	if w.Len() != 0 {
		t.Fatalf("notice with telemetry off: %q", w.String())
	}
}

func TestShareSendAndSent(t *testing.T) {
	isolateTelemetryEnv(t)
	cfg, workDir := telemetryFixture(t, "")
	yesterdayEvents(t, workDir)
	url, dir, hits := shareReceiver(t)
	t.Setenv(share.URLEnv, url)
	noBackoff(t)

	// No opt-in step: on by default.
	out, err := invoke(t, "--config", cfg, "telemetry", "send")
	if err != nil || !strings.Contains(out, "sent") || hits.Load() != 1 {
		t.Fatalf("send: %q %v hits %d", out, err, hits.Load())
	}
	stored, _ := os.ReadFile(filepath.Join(dir, share.DataFile))
	if strings.Contains(string(stored), "SENTINEL") || !strings.Contains(string(stored), `"cmd":"transition"`) {
		t.Fatalf("stored batch:\n%s", stored)
	}

	// What was sent is in the sent log, byte for byte the stored batch.
	sentJSON, err := invoke(t, "--config", cfg, "--json", "telemetry", "sent")
	if err != nil {
		t.Fatal(err)
	}
	var log struct {
		Sent []share.SentRecord `json:"sent"`
	}
	if err := json.Unmarshal([]byte(sentJSON), &log); err != nil || len(log.Sent) != 1 || !log.Sent[0].OK {
		t.Fatalf("sent log: %v %s", err, sentJSON)
	}
	got, _, _ := share.ReadStore(dir)
	a, _ := json.Marshal(got[0])
	b, _ := json.Marshal(log.Sent[0].Batch)
	if string(a) != string(b) {
		t.Errorf("sent log batch differs from the stored one:\n%s\n%s", a, b)
	}
	out, _ = invoke(t, "--config", cfg, "telemetry", "sent")
	if !strings.Contains(out, "✓") || !strings.Contains(out, url) {
		t.Errorf("sent log:\n%s", out)
	}
	if out, _ := invoke(t, "--config", cfg, "telemetry", "send"); !strings.Contains(out, "nothing pending") || hits.Load() != 1 {
		t.Errorf("second send: %q hits %d", out, hits.Load())
	}
}

// The real vocabulary is all tokens the collector accepts: otherwise names
// would be dropped silently from every batch.
func TestShareVocabularyIsTokens(t *testing.T) {
	isolateTelemetryEnv(t)
	tok := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	v := shareVocab()
	if len(v.Commands) < 20 || len(v.Features) < 5 || len(v.Templates) < 3 {
		t.Fatalf("vocabulary too small: %d commands, %d features, %d templates", len(v.Commands), len(v.Features), len(v.Templates))
	}
	check := func(kind, s string) {
		if !tok.MatchString(s) {
			t.Errorf("%s %q is not a token the collector accepts", kind, s)
		}
	}
	for k := range v.Commands {
		for _, part := range strings.Split(k, " ") {
			check("command", part)
		}
		for f := range v.Flags[k] {
			check("flag of "+k, f)
		}
	}
	for f := range v.Features {
		check("feature", f)
	}
	for tm := range v.Templates {
		check("template", tm)
	}
}
