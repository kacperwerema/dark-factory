package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// workshopToken is the value of --name in workshop.css's :root block.
func workshopToken(t *testing.T, name string) string {
	t.Helper()
	css, err := os.ReadFile("static/workshop.css")
	if err != nil {
		t.Fatal(err)
	}
	root := regexp.MustCompile(`(?s):root \{(.*?)\n\}`).FindSubmatch(css)
	if root == nil {
		t.Fatal("workshop.css has no :root block")
	}
	m := regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `:\s*([^;]+);`).FindSubmatch(root[1])
	if m == nil {
		t.Fatalf("workshop.css :root has no --%s", name)
	}
	return strings.ToUpper(strings.TrimSpace(string(m[1])))
}

// hexRGB is #RRGGBB as three numbers.
func hexRGB(t *testing.T, hex string) [3]int {
	t.Helper()
	var c [3]int
	for i := range c {
		var v int
		for _, ch := range strings.ToUpper(hex[1+2*i : 3+2*i]) {
			v = v*16 + strings.IndexRune("0123456789ABCDEF", ch)
		}
		c[i] = v
	}
	return c
}

// farApart is whether two colours are clearly different: the largest
// channel gap is over 80 of 255 (HQ's yellow and the old GTM amber were 13).
func farApart(t *testing.T, a, b string) bool {
	x, y := hexRGB(t, a), hexRGB(t, b)
	gap := 0
	for i := range x {
		gap = max(gap, x[i]-y[i], y[i]-x[i])
	}
	return gap > 80
}

// A group's room is drawn in the --group token, never in its projects.yaml
// colour (the GTM template's amber, next to HQ's yellow), and the token is
// clearly different from HQ's colour and from the owner orange.
func TestGroupRoomColourDiffersFromHQ(t *testing.T) {
	group := workshopToken(t, "group")
	if group != strings.ToUpper(companyGroupColor) {
		t.Errorf("--group is %s in workshop.css, companyGroupColor %s: keep them the same", group, companyGroupColor)
	}
	if !farApart(t, group, companyHQColor) {
		t.Errorf("--group %s is too close to HQ's %s", group, companyHQColor)
	}
	if !farApart(t, group, workshopToken(t, "you")) {
		t.Errorf("--group %s is too close to the owner orange", group)
	}

	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	stubLeadChat(t, false)
	withGTMGroup(&a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company := getBody(t, ts.URL+"/p/agents/company?classic=1")

	assertContains(t, company,
		`data-testid="company.room.head.hq" style="background:`+companyHQColor+`"`,
		`data-testid="company.room.head.group">`)
	css, _ := os.ReadFile("static/workshop.css")
	assertContains(t, string(css), ".wb-room--group > .wb-room-head { background: var(--group); }")
	if strings.Contains(company, "#F2A541") {
		t.Error("GTM's projects.yaml amber still drawn on the company page")
	}
}

// Everywhere else a group's colour shows (map building, crew column, shelf,
// connections) it is the group colour too; departments keep theirs.
func TestGroupColourEverywhere(t *testing.T) {
	a := setupAgentsProject(t)
	withGTMGroup(&a)
	if c := deptColorOf(&a, a.Departments[0], 0); c != companyGroupColor {
		t.Errorf("GTM colour = %q, want %q", c, companyGroupColor)
	}
	if c := deptColorOf(&a, a.Departments[1], 1); c != deptColor(a.Departments[1], 1) {
		t.Errorf("Marketing colour = %q, want its own", c)
	}
	b := mapDepartmentsOf(&a, nil)
	if b[2].Name != "GTM" || b[2].Color != companyGroupColor {
		t.Errorf("GTM building colour = %q", b[2].Color)
	}
	if c := boxColors(&a)["gtm"]; c != companyGroupColor {
		t.Errorf("GTM connections colour = %q, want %s", c, companyGroupColor)
	}
}
