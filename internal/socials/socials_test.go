package socials

import (
	"os"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestPostizIsFoundByItsPerRunTool(t *testing.T) {
	c := &tracker.Connector{Name: "social", URL: "http://127.0.0.1:4007/api/mcp", PerRun: &tracker.PerRun{Tool: "postiz"},
		Headers: map[string]string{"Authorization": "Bearer keychain:postiz"}}
	a := For(c)
	if a == nil || a.Name() != "Postiz" {
		t.Fatalf("For = %v", a)
	}
	if got := a.Base(c); got != "http://127.0.0.1:4007" {
		t.Errorf("Base = %q", got)
	}
	if ref, ok := a.KeyRef(c); !ok || ref.String() != "keychain:postiz" {
		t.Errorf("KeyRef = %v %v", ref, ok)
	}
	if For(&tracker.Connector{Name: "social"}) != nil || For(&tracker.Connector{PerRun: &tracker.PerRun{Tool: "other"}}) != nil {
		t.Error("a connector without the postiz tool got the adapter")
	}
	if got := a.RedirectURI("http://localhost:4007", "linkedin"); got != "http://localhost:4007/integrations/social/linkedin" {
		t.Errorf("RedirectURI = %q", got)
	}
}

func TestPostizNetworkTableIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range postizNetworks {
		if seen[n.ID] {
			t.Errorf("%s twice", n.ID)
		}
		seen[n.ID] = true
		if n.Name == "" || n.App == "" || n.Wait == "" {
			t.Errorf("%s: name, app and wait are all needed", n.ID)
		}
		switch n.Connect {
		case ConnectOAuth, ConnectElsewhere:
		case ConnectPassword:
			if len(n.Fields) == 0 {
				t.Errorf("%s: an app-password network with no fields", n.ID)
			}
		default:
			t.Errorf("%s: connect %q", n.ID, n.Connect)
		}
		for _, v := range n.Vars {
			if !varRe.MatchString(v.Name) || v.Label == "" {
				t.Errorf("%s: var %+v", n.ID, v)
			}
		}
	}
	for _, id := range []string{"bluesky", "linkedin", "x", "instagram", "facebook", "tiktok", "youtube", "threads"} {
		if !seen[id] {
			t.Errorf("the brief's %s is missing", id)
		}
	}
}

func TestPostizErrorsNeverRepeatWhatWasSent(t *testing.T) {
	if got := safeMsg([]byte(`{"msg":"Invalid service URL"}`), "pw"); got != " (Invalid service URL)" {
		t.Errorf("safeMsg = %q", got)
	}
	if got := safeMsg([]byte(`{"msg":"login failed for secret-pw"}`), "secret-pw"); got != "" {
		t.Errorf("safeMsg repeated a sent value: %q", got)
	}
	if got := safeMsg([]byte(`not json`)); got != "" {
		t.Errorf("safeMsg = %q", got)
	}
}

func TestOverrideHoldsReferencesOnly(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := OverridePath(PostizTool)
	if err := WriteOverride(postiz{}, path, []string{"X_API_SECRET", "X_API_KEY"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteOverride(postiz{}, path, []string{"x; rm"}); err == nil {
		t.Error("a bad var name was written")
	}
	b, _ := readFile(path)
	if !strings.Contains(b, "      X_API_KEY: ${X_API_KEY:-}\n      X_API_SECRET: ${X_API_SECRET:-}\n") {
		t.Errorf("override:\n%s", b)
	}
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
