package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckShotURL(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:8080/p/x/hall", "https://example.localhost/"} {
		if err := checkShotURL(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "127.0.0.1:8080/p/x", "file:///etc/passwd", "http://"} {
		if err := checkShotURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestOneShotDefaults(t *testing.T) {
	s := oneShot{URL: "http://127.0.0.1/"}.withDefaults("/repo")
	if s.W != shotWidth || s.H != shotHeight || s.Out != filepath.Join("/repo", ".playtest", "shot.png") {
		t.Errorf("defaults = %+v", s)
	}
	s = oneShot{W: 390, H: 844, Out: "a.png"}.withDefaults("/repo")
	if s.W != 390 || s.H != 844 || s.Out != "a.png" {
		t.Errorf("given values replaced: %+v", s)
	}
}

func TestNoMatchNamesTheSessionsIssue(t *testing.T) {
	all := []*Session{{Name: "hall", Issue: "ui/hall"}}
	msg := noMatch(all, opts{issue: "ui/other", session: "hall", sessions: "s"})
	for _, want := range []string{`issue="ui/other"`, `session "hall" is for issue "ui/hall"`, "ISSUE=ui/hall"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	msg = noMatch(all, opts{issue: "ui/other", session: "nope", sessions: "s"})
	if !strings.Contains(msg, `issue="ui/other" session="nope"`) || strings.Contains(msg, "is for issue") {
		t.Errorf("unknown session: %q", msg)
	}
}
