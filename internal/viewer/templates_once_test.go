package viewer

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestTemplatesParsedOncePerProcess: NewServer clones one parse of the
// embedded templates instead of parsing them again (the viewer's tests
// build hundreds of servers).
func TestTemplatesParsedOncePerProcess(t *testing.T) {
	for i := 0; i < 3; i++ {
		if _, err := NewServer(nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := templateParses.Load(); got != 1 {
		t.Errorf("template parses in this process = %d, want 1", got)
	}
}

// TestServerTemplateFuncsBindToTheirServer: the per-server "sidebar" func is
// bound on each server's own clone, so one server's template never reads
// another server's projects.
func TestServerTemplateFuncsBindToTheirServer(t *testing.T) {
	alpha, _ := setupTestProject(t)
	alpha.Slug, alpha.Name = "alpha", "Alpha"
	beta, _ := setupTestProject(t)
	beta.Slug, beta.Name = "beta", "Beta"
	withMockTmuxSessions(t, nil)

	srvA, err := NewServer([]tracker.Project{alpha})
	if err != nil {
		t.Fatal(err)
	}
	srvB, err := NewServer([]tracker.Project{beta})
	if err != nil {
		t.Fatal(err)
	}
	// html/template refuses Parse after Execute: add each server's probe
	// first. The probe runs on that server's own set, so it calls the funcs
	// bound to that server.
	for _, srv := range []*Server{srvA, srvB} {
		if _, err := srv.tmpl.New("probe").Parse(`{{(sidebar . "board").ProjectName}}`); err != nil {
			t.Fatal(err)
		}
	}
	render := func(srv *Server, prefix string) string {
		t.Helper()
		var b strings.Builder
		if err := srv.tmpl.ExecuteTemplate(&b, "probe", prefix); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	if srvA.tmpl == srvB.tmpl {
		t.Fatal("two servers share one template set")
	}
	cases := []struct {
		srv          *Server
		name, prefix string
		want         string
	}{
		{srvA, "A sees alpha", "/p/alpha", "Alpha"},
		{srvA, "A does not see beta", "/p/beta", ""},
		{srvB, "B sees beta", "/p/beta", "Beta"},
		{srvB, "B does not see alpha", "/p/alpha", ""},
	}
	for _, c := range cases {
		if got := render(c.srv, c.prefix); got != c.want {
			t.Errorf("%s: sidebar(%q).ProjectName = %q, want %q", c.name, c.prefix, got, c.want)
		}
	}
}
