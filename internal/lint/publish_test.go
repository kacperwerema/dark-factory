package lint

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"gopkg.in/yaml.v3"
)

// Direct for Instagram and TikTok, Postiz for LinkedIn: the config as an
// owner writes it in projects.yaml lints clean.
const mixedRoutes = `
- name: Video
  publish:
    instagram: instagram
    tiktok: tiktok
    linkedin: marketing/postiz
  connectors:
    - name: instagram
      kind: tool
      publish: instagram-reels
      secrets:
        META_PAGE_TOKEN: keychain:meta-page-token
        META_APP_ID: keychain:meta-app/id
        META_APP_SECRET: keychain:meta-app/secret
      optional: [META_APP_ID, META_APP_SECRET]
      account: {page_id: "100", ig_user_id: "200"}
      sends: true
      needs_approved_output: true
    - name: tiktok
      kind: tool
      publish: tiktok
      secrets:
        TIKTOK_CLIENT_KEY: keychain:tiktok-app/key
        TIKTOK_CLIENT_SECRET: keychain:tiktok-app/secret
        TIKTOK_REFRESH_TOKEN: keychain:tiktok-refresh
      sends: true
      needs_approved_output: true
- name: Marketing
  connectors:
    - name: postiz
      kind: mcp
      url: http://localhost:4007/api/mcp
      headers:
        Authorization: "Bearer keychain:postiz"
      exception: factory/open-source-partners-integrate-outside-tools-openmontage-openseo-postiz-instead-of-building-them-in-house#D-4
      sends: true
      needs_approved_output: true
      read_tools: [integrationList]
      per_run:
        tool: postiz
        start: 'docker compose -p postiz start'
        ready: 'curl -fsS http://localhost:4007'
        stop: 'docker compose -p postiz stop'
`

func publishLint(t *testing.T, depts []tracker.Department) []string {
	t.Helper()
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Departments = depts
	var got []string
	for _, f := range checkDepartments(env) {
		// Postiz's licence exception cites a decision this test board
		// doesn't hold; the licence rule has its own tests.
		if strings.Contains(f.Message, "AGPL") {
			continue
		}
		got = append(got, f.Where+": "+f.Message)
	}
	return got
}

func mixedDepts(t *testing.T) []tracker.Department {
	t.Helper()
	var depts []tracker.Department
	if err := yaml.Unmarshal([]byte(mixedRoutes), &depts); err != nil {
		t.Fatal(err)
	}
	return depts
}

func TestPublishLintMixedRoutes(t *testing.T) {
	if got := publishLint(t, mixedDepts(t)); len(got) != 0 {
		t.Fatalf("direct for one channel and Postiz for another should lint clean:\n%s", strings.Join(got, "\n"))
	}
}

func TestPublishLintNamesWhatIsMissing(t *testing.T) {
	depts := mixedDepts(t)
	v := &depts[0]
	v.Publish["pinterest"] = "nope"
	v.Publish["x"] = "marketing/nothing"
	v.Publish["Bad Name"] = "instagram"
	v.Connectors[0].Account = map[string]string{"page_id": "100"}
	v.Connectors[0].NeedsApprovedOutput = false
	delete(v.Connectors[1].Secrets, "TIKTOK_CLIENT_SECRET")
	v.Connectors = append(v.Connectors, tracker.Connector{Name: "pin", Kind: "tool", Publish: "pinterest-video", Sends: true, NeedsApprovedOutput: true})
	got := strings.Join(publishLint(t, depts), "\n")
	for _, want := range []string{
		"connector instagram: publish: instagram-reels needs account: ig_user_id",
		"connector instagram: a publish: connector posts outside the machine: give it sends: true and needs_approved_output: true",
		"connector tiktok: TIKTOK_REFRESH_TOKEN needs TIKTOK_CLIENT_SECRET beside it",
		`connector pin: publish: "pinterest-video" is not instagram-reels, facebook-reels, tiktok, pinterest`,
		`publish: pinterest: Video has no connector "nope"`,
		`publish: x: Marketing has no connector "nothing"`,
		`publish: Bad Name: channel "Bad Name" is not a lowercase word`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestPublishLintSecretsNeeded(t *testing.T) {
	got := strings.Join(connectorFindings(tracker.Department{Connectors: []tracker.Connector{
		{Name: "fb", Kind: "tool", Publish: "facebook-reels", Sends: true, NeedsApprovedOutput: true, Account: map[string]string{"page_id": "1"}},
		{Name: "pin", Kind: "tool", Publish: "pinterest", Command: "x", Sends: true, NeedsApprovedOutput: true, Secrets: map[string]string{"PINTEREST_ACCESS_TOKEN": "keychain:pin"}},
		{Name: "plain", Kind: "tool", Command: "x", Account: map[string]string{"a": "b"}},
	}}), "\n")
	for _, want := range []string{
		"connector fb: publish: facebook-reels needs secrets: META_PAGE_TOKEN",
		"connector pin: a publish: connector runs no command:",
		"connector plain: account: is only for a publish: connector",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
