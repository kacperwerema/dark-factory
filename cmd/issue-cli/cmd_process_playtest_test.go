package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/guidesync"
)

func TestProcessPlaytestGuides(t *testing.T) {
	for args, want := range map[string]string{
		"":         "# Playtest sessions",
		"web":      "# Playtest example: a web app driven by headless Chrome",
		"chromedp": "# Playtest example: a web app driven by headless Chrome",
		"game":     "# Playtest example: a game",
	} {
		var out bytes.Buffer
		var a []string
		if args != "" {
			a = []string{args}
		}
		if err := runProcessPlaytest(&Context{Stdout: &out}, a); err != nil {
			t.Fatalf("process playtest %s: %v", args, err)
		}
		got := out.String()
		if !strings.HasPrefix(got, want) {
			t.Errorf("process playtest %s starts with %q, want %q", args, firstLine(got), want)
		}
		if strings.HasPrefix(got, "---") || strings.Contains(got, "\ntitle: \"") {
			t.Errorf("process playtest %s printed frontmatter", args)
		}
	}
	var out bytes.Buffer
	err := runProcessPlaytest(&Context{Stdout: &out}, []string{"cli"})
	if err == nil || !strings.Contains(err.Error(), "issue-cli process playtest web") {
		t.Errorf("unknown guide error should list the guides, got %v", err)
	}
	if normalizeTopic("ux-test") != "playtest" || normalizeTopic("Playtests") != "playtest" {
		t.Error("playtest aliases don't normalise")
	}
}

func firstLine(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }

// TestPlaytestGuidesInSync: the embedded guides are generated from the docs/
// copies the web docs view shows, and must be the same files.
func TestPlaytestGuidesInSync(t *testing.T) {
	for _, msg := range playtestGuideDrift(filepath.Join("..", "..", "docs", "patterns", "playtest"), filepath.Join("guides", "playtest")) {
		t.Error(msg)
	}
}

// playtestGuideDrift lists how the embedded folder differs from docs/, each
// line naming the command that fixes it.
func playtestGuideDrift(docs, embedded string) []string {
	fix := "edit docs/patterns/playtest/ (the source) and run " + guidesync.Command
	var out []string
	read := func(dir string) map[string][]byte {
		m := map[string][]byte{}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				m[e.Name()], _ = os.ReadFile(filepath.Join(dir, e.Name()))
			}
		}
		return m
	}
	src, gen := read(docs), read(embedded)
	if len(src) == 0 {
		return []string{"docs/patterns/playtest has no guides"}
	}
	for name, data := range src {
		g, ok := gen[name]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("cmd/issue-cli/guides/playtest/%s missing: %s", name, fix))
		case !bytes.Equal(g, data):
			out = append(out, fmt.Sprintf("cmd/issue-cli/guides/playtest/%s differs from docs/patterns/playtest/%s: %s", name, name, fix))
		}
	}
	for name := range gen {
		if _, ok := src[name]; !ok {
			out = append(out, fmt.Sprintf("cmd/issue-cli/guides/playtest/%s has no source in docs/patterns/playtest: %s", name, fix))
		}
	}
	sort.Strings(out)
	return out
}

// TestPlaytestGuideDriftNamesGenerate: copies out of sync fail with the
// go generate command, and running what that command runs makes them equal.
func TestPlaytestGuideDriftNamesGenerate(t *testing.T) {
	tmp := t.TempDir()
	docs, embedded := filepath.Join(tmp, "docs"), filepath.Join(tmp, "guides")
	for _, d := range []string{docs, embedded} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(docs, "README.md", "# Playtest sessions\nnew line\n")
	write(docs, "web-app.md", "# web\n")
	write(embedded, "README.md", "# Playtest sessions\n")
	write(embedded, "old.md", "# gone\n")

	drift := playtestGuideDrift(docs, embedded)
	if len(drift) != 3 {
		t.Fatalf("want 3 drift lines (differs, missing, no source), got %q", drift)
	}
	for _, msg := range drift {
		if !strings.Contains(msg, "run go generate ./cmd/issue-cli/...") {
			t.Errorf("drift message lacks the go generate command: %q", msg)
		}
	}

	if _, err := guidesync.Sync(docs, embedded); err != nil {
		t.Fatal(err)
	}
	if drift := playtestGuideDrift(docs, embedded); len(drift) != 0 {
		t.Errorf("after the generate step the copies still differ: %q", drift)
	}
}

// TestPlaytestGuidesGenerateLine: the go:generate line runs the sync tool
// from docs/patterns/playtest into the folder go:embed reads, and that tool
// exists, so the command the failure names is the one that fixes it.
func TestPlaytestGuidesGenerateLine(t *testing.T) {
	src, err := os.ReadFile("cmd_process_playtest.go")
	if err != nil {
		t.Fatal(err)
	}
	want := "//go:generate go run ../../tools/playtest-guides ../../docs/patterns/playtest guides/playtest"
	if !strings.Contains(string(src), want+"\n") {
		t.Fatalf("cmd_process_playtest.go lacks %q", want)
	}
	if !strings.Contains(string(src), "//go:embed guides/playtest/*.md") {
		t.Error("the embed no longer reads guides/playtest, the generate target")
	}
	if _, err := os.Stat(filepath.Join("..", "..", "tools", "playtest-guides", "main.go")); err != nil {
		t.Errorf("generate tool missing: %v", err)
	}
	if guidesync.Command != "go generate ./cmd/issue-cli/..." {
		t.Errorf("guidesync.Command = %q", guidesync.Command)
	}
}
