package connectors

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestAtError(t *testing.T) {
	c := &tracker.Connector{At: map[string]tracker.ConnectorAt{
		"In Progress": {DenyArgs: map[string]map[string][]string{"post": {"type": {"now"}, "count": {"3"}, "live": {"true"}}}},
		"publish":     {Allow: []string{"post"}},
	}}
	for _, tc := range []struct {
		status, tool, args, want string
	}{
		{"in progress", "anything", `{}`, ""},
		{"in progress", "post", `{"type":"draft"}`, ""},
		{"in progress", "post", ``, ""},
		{"in progress", "post", `{"type":"now"}`, "post with type: now is refused"},
		{"in progress", "post", `{"count":3}`, "post with count: 3 is refused"},
		{"in progress", "post", `{"live":true}`, "post with live: true is refused"},
		{"publish", "post", `{"type":"now"}`, ""},
		{"publish", "look", `{}`, "at publish: look is not allowed (at: publish allows post)"},
		{"idea", "post", `{}`, "at idea: no tools are allowed (at: names In Progress, publish)"},
	} {
		err := AtError("marketing/postiz", c, tc.status, tc.tool, []byte(tc.args))
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s %s %s: %v", tc.status, tc.tool, tc.args, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s %s %s: %v, want %q", tc.status, tc.tool, tc.args, err, tc.want)
		}
	}
	if AtError("x/y", &tracker.Connector{}, "idea", "post", nil) != nil {
		t.Error("a connector without at: refused")
	}
	if AtCallerError("x/y", Caller{Owner: true}) != nil || AtCallerError("x/y", Caller{Agent: true, Issue: "a/b"}) != nil {
		t.Error("owner or worker refused")
	}
	if err := AtCallerError("x/y", Caller{Agent: true, Lead: true}); err == nil || !strings.Contains(err.Error(), "--as-owner") {
		t.Errorf("lead: %v", err)
	}
}

// AC1: a deny_args: key may be a path into objects and every item of an
// array; the bridge refuses a call whose value there matches, naming
// where it sits, and a malformed path refuses every call of that tool.
func TestAtErrorNestedPath(t *testing.T) {
	c := &tracker.Connector{At: map[string]tracker.ConnectorAt{
		"publish": {DenyArgs: map[string]map[string][]string{
			"schedule": {"socialPost[].type": {"now"}, "meta.mode": {"live"}, "grid[][].x": {"1"}, "big": {"12345678901234567890"}},
			"typo":     {"socialPost[.type": {"now"}},
		}},
	}}
	for _, tc := range []struct{ tool, args, want string }{
		{"schedule", `{"socialPost":[{"type":"schedule"},{"type":"draft"}]}`, ""},
		{"schedule", `{"socialPost":[{"type":"schedule"},{"type":"now"}]}`, "schedule with socialPost[1].type: now is refused (at: publish: deny_args:); another value of socialPost[].type passes"},
		{"schedule", `{"socialPost":{"type":"now"}}`, "socialPost.type: now is refused"},
		{"schedule", `{"socialPost":[{"value":"x"}]}`, ""},
		{"schedule", `{"socialPost":"now"}`, ""},
		{"schedule", `{"socialPost":null}`, ""},
		{"schedule", `{"type":"now"}`, ""},
		{"schedule", `{"meta":{"mode":"live"}}`, "meta.mode: live is refused"},
		{"schedule", `{"meta":{"mode":"test"}}`, ""},
		{"schedule", `{"grid":[[{"x":0}],[{"x":2},{"x":1}]]}`, "grid[1][1].x: 1 is refused"},
		{"schedule", `{"big":12345678901234567890}`, "big: 12345678901234567890 is refused"},
		{"schedule", `[1,2]`, ""},
		{"typo", `{}`, `"socialPost[.type" is not an argument path`},
	} {
		err := AtError("marketing/social", c, "publish", tc.tool, []byte(tc.args))
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s %s: %v", tc.tool, tc.args, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s %s: %v, want %q", tc.tool, tc.args, err, tc.want)
		}
	}
	// A plain key with no path characters is still a top-level name as
	// written, whatever else it holds.
	odd := &tracker.Connector{At: map[string]tracker.ConnectorAt{"p": {DenyArgs: map[string]map[string][]string{"t": {"my key": {"x"}}}}}}
	if err := AtError("a/b", odd, "p", "t", []byte(`{"my key":"x"}`)); err == nil || !strings.Contains(err.Error(), "my key: x is refused") {
		t.Errorf("odd top-level key: %v", err)
	}
}
