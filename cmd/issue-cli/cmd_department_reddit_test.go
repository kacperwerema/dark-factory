package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/socials"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// redditUserCredentials are the reddit-mcp-buddy settings that would give
// it a Reddit login and its write tools. The read connector never names one.
var redditUserCredentials = []string{"REDDIT_USERNAME", "REDDIT_PASSWORD", "REDDIT_REFRESH_TOKEN", "REDDIT_BUDDY_WRITE"}

// TestMarketingRedditExampleIsReadOnly is AC3, AC5 and AC6 on the template:
// department add marketing writes the owner's subreddits.md starter and a
// commented `reddit` connector that, uncommented, is a read-only MCP
// server: app-only keys as Keychain references (the two items Postiz's
// Reddit app uses too), its write tools switched off, no user login, no
// per_run: (a light process that lives with the session). Postiz, the one
// heavy tool, keeps its per_run: and reads a subreddit's flairs at brief
// through triggerTool.
func TestMarketingRedditExampleIsReadOnly(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "departments/marketing/subreddits.md")
	list, err := os.ReadFile(filepath.Join(proj.Root(), "departments", "marketing", "subreddits.md"))
	if err != nil {
		t.Fatalf("department add wrote no subreddits.md: %v", err)
	}
	for _, want := range []string{"| Subreddit | Why | Self-promotion rule | Allowed | Checked |", "r/example", "2026-10-31", "Social: Reddit"} {
		assertContains(t, string(list), want)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#   - name: reddit", "#       REDDIT_CLIENT_ID: keychain:postiz-env/REDDIT_CLIENT_ID", "security add-generic-password -s postiz-env -a REDDIT_CLIENT_SECRET -w"} {
		assertContains(t, string(raw), want)
	}

	uncommentConnectors(t, cfg)
	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("marketing")
	byName := map[string]*tracker.Connector{}
	for i := range d.Connectors {
		byName[d.Connectors[i].Name] = &d.Connectors[i]
	}
	c := byName["reddit"]
	if c == nil {
		t.Fatalf("uncommented: no reddit connector in %+v", d.Connectors)
	}
	if c.Kind != tracker.ConnectorMCP || c.Command != "npx" || c.URL != "" || c.Sends {
		t.Errorf("reddit = %+v, want a stdio MCP server run by npx that doesn't send", *c)
	}
	if len(c.Args) != 2 || c.Args[0] != "-y" || !strings.HasPrefix(c.Args[1], "reddit-mcp-buddy@") || strings.HasSuffix(c.Args[1], "@latest") {
		t.Errorf("args %v, want -y reddit-mcp-buddy@<pinned version>", c.Args)
	}
	// The items the connect-socials page writes for the social connector's
	// Reddit row: the owner saves the app's keys once.
	a := socials.For(byName["social"])
	if a == nil || socials.Find(a, "reddit") == nil {
		t.Fatal("the connect-socials page has no Reddit row for the social connector")
	}
	want := map[string]string{}
	for _, v := range []string{"REDDIT_CLIENT_ID", "REDDIT_CLIENT_SECRET"} {
		want[v] = a.EnvRef(v).String()
	}
	if len(c.Secrets) != len(want) {
		t.Errorf("secrets %v, want only %v", c.Secrets, want)
	}
	for k, v := range want {
		if c.Secrets[k] != v {
			t.Errorf("secret %s = %q, want %q", k, c.Secrets[k], v)
		}
	}
	item := exampleItem(t, marketingExample, "reddit", "    ")
	for _, cred := range redditUserCredentials {
		if _, ok := c.Secrets[cred]; ok || strings.Contains(item, cred) {
			t.Errorf("the reddit connector names %s: reading needs no Reddit login and gets no write tool", cred)
		}
	}
	for _, w := range []string{"create_post", "add_comment"} {
		if !slices.Contains(c.DeniedTools(), w) {
			t.Errorf("reddit's write tool %s is not in deny_tools: %v", w, c.DeniedTools())
		}
	}
	if c.PerRun != nil {
		t.Errorf("reddit has per_run: %+v; it is a light process that lives with the session", c.PerRun)
	}
	s := byName["social"]
	if s == nil || s.PerRun == nil || s.PerRun.Tool != "postiz" {
		t.Fatalf("social lost its per_run: lease: %+v", s)
	}
	for _, status := range []string{"brief", "publish"} {
		if !slices.Contains(s.At[status].Allow, "triggerTool") {
			t.Errorf("social at %s doesn't allow triggerTool (a subreddit's flairs and post requirements)", status)
		}
	}
	if slices.Contains(s.At["brief"].Allow, "integrationSchedulePostTool") {
		t.Error("social schedules at brief")
	}

	for _, f := range []string{"brand.md", "icp.md"} {
		mustWrite(t, filepath.Join(proj.Root(), "departments", "marketing", f), "# settled\n")
	}
	for _, f := range strings.Split(socialLint(t, proj), "\n") {
		if strings.Contains(strings.ToLower(f), "reddit") || strings.Contains(f, "subreddits.md") {
			t.Errorf("uncommented: lint finding about Reddit: %s", f)
		}
	}
}

// TestSocialPromptsTeachReddit is AC3 and AC4 on the prompts: research reads
// only the listed subreddits, only through the `reddit` connector, and
// reads nothing from Reddit without it; the brief plans only in a listed
// subreddit, its self-promotion rule quoted; the draft puts replies in
// other people's threads aside for the owner's hand; publish sends none of
// them; the Brief box the brief → draft gate checks names the list. The
// only way out stays the owner_only gate into publish.
func TestSocialPromptsTeachReddit(t *testing.T) {
	wf := loadMarketingWorkflow(t)
	prompts := map[string]string{}
	for _, s := range wf.Types["social"].Statuses {
		prompts[s.Name] = strings.ToLower(s.Prompt)
	}
	for status, words := range map[string][]string{
		"research": {"departments/<department>/subreddits.md", "`reddit` connector's read calls", "top posts", "permalink", "competitors.md",
			"never read one that isn't listed", "proposal for the owner", "read nothing from reddit's site", "no scraping", "no logged-in browser", "2026-10-31"},
		"brief":   {"only in a subreddit listed in departments/<department>/subreddits.md", "self-promotion rule", "flairs and post requirements", "the owner updates the list, never you"},
		"draft":   {"subreddit, title, type", "flair", "first comment under our own post", "### replies (posted by the owner)", "permalink", "no connector call sends one"},
		"publish": {"schedule each approved post in its subreddit", "send nothing from `### replies (posted by the owner)`"},
		"measure": {"reddit has none", "no value yet"},
	} {
		for _, w := range words {
			if !strings.Contains(prompts[status], strings.ToLower(w)) {
				t.Errorf("%s prompt doesn't say %q:\n%s", status, w, prompts[status])
			}
		}
	}
	box := "Each Reddit post or reply names a subreddit from departments/<department>/subreddits.md, quotes its self-promotion rule and keeps within it"
	found := false
	for _, tr := range wf.Types["social"].Transitions {
		if tr.From != "research" || tr.To != "brief" {
			continue
		}
		for _, a := range tr.Actions {
			if a.Type == "append_section" && a.Title == "Brief" && strings.Contains(a.Body, "- [ ] "+box) {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("research → brief doesn't append the Brief box %q", box)
	}
	assertChannelPath(t, wf, "social", socialPath)
}

// TestDocsSayRedditNeedsAnApp is AC1 and AC5 in the docs: Social: Reddit
// names the closure, that the app is the owner's step, the Keychain items
// and the redirect URI, and that replies are posted by the owner.
func TestDocsSayRedditNeedsAnApp(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, want := range []string{
		"#### Social: Reddit", "2026-10-31", "That step is the owner's", "keychain:postiz-env/REDDIT_CLIENT_ID", "keychain:postiz-env/REDDIT_CLIENT_SECRET",
		"<postiz>/integrations/social/reddit", "### Replies (posted by the owner)", "reddit-mcp-buddy", "(MIT)", "`files:` lists starter files",
	} {
		assertContains(t, doc, want)
	}
}
