package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// sendPortalExample is the bundled sales-b2c connector example file.
var sendPortalExample = filepath.Join("templates", "departments", "sales-b2c", "connectors.example.yaml")

// sendPortalConsentLines are the double opt-in warning's words, which stay
// in the example as shipped and as installed.
var sendPortalConsentLines = []string{
	"double opt-in is unverified in SendPortal",
	"a list collected under a new consent version",
}

// TestSalesExampleConnectorsLintClean: department add sales-b2c writes
// umami and sendportal commented into the entry. Uncommented, both load:
// sendportal is a curl tool that sends, held to an approved output, its
// one secret a keychain: reference, no per_run:. Lint says nothing about
// either connector, and the launch type, the path that uses sendportal,
// reaches publish only through an owner_only gate.
func TestSalesExampleConnectorsLintClean(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	// Sales alone: under GTM, Marketing's own commented example comes
	// first in the file.
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "sales-b2c"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#   - name: umami", "#   - name: sendportal", "#       SENDPORTAL_API_TOKEN: keychain:sendportal-api-token"} {
		assertContains(t, string(raw), want)
	}
	if d := reloadDeptProject(t, cfg).DepartmentByKey("sales"); d == nil || len(d.Connectors) != 0 {
		t.Fatalf("sales installed with connectors %+v, want none (the examples are commented)", d)
	}

	uncommentConnectors(t, cfg)
	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("sales")
	if d == nil {
		t.Fatal("no sales department after uncommenting")
	}
	var names []string
	var sp *tracker.Connector
	for i := range d.Connectors {
		names = append(names, d.Connectors[i].Name)
		if d.Connectors[i].Name == "sendportal" {
			sp = &d.Connectors[i]
		}
	}
	if strings.Join(names, ",") != "umami,sendportal" || sp == nil {
		t.Fatalf("uncommented: sales connectors %v, want umami,sendportal", names)
	}
	if sp.Kind != tracker.ConnectorTool || sp.Command != "curl" || !sp.Sends || !sp.NeedsApprovedOutput || sp.PerRun != nil || sp.URL != "" {
		t.Errorf("sendportal = %+v, want a curl tool, sends, needs_approved_output, no per_run", *sp)
	}
	if len(sp.Secrets) != 1 || sp.Secrets["SENDPORTAL_API_TOKEN"] != "keychain:sendportal-api-token" {
		t.Errorf("sendportal secrets = %v, want SENDPORTAL_API_TOKEN: keychain:sendportal-api-token only", sp.Secrets)
	}
	if !strings.Contains(sp.Check, "/api/v1/ping") {
		t.Errorf("sendportal check = %q, want the /api/v1/ping call", sp.Check)
	}

	env, err := lint.Load(proj, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := lint.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		// The temp folder's name holds the test's name: leave it out.
		s := strings.ToLower(strings.ReplaceAll(f.Where+" "+f.Message, proj.Root(), "<root>"))
		if f.Check == "owner-only" || strings.Contains(s, "sendportal") || strings.Contains(s, "umami") || strings.Contains(s, "connector") {
			t.Errorf("uncommented: lint finding: %+v", f)
		}
	}

	wf, err := proj.WorkflowFor("Sales")
	if err != nil {
		t.Fatal(err)
	}
	launch := wf.ForType("launch")
	if launch == nil {
		t.Fatal("the sales workflow has no launch type")
	}
	into := 0
	for _, tr := range launch.Transitions {
		if tr.To != "publish" {
			continue
		}
		into++
		if !launch.OwnerOnlyGate(tr.From, tr.To) {
			t.Errorf("launch: %s → publish is not an owner_only gate", tr.From)
		}
	}
	if into == 0 {
		t.Error("launch: no transition into publish")
	}
	assertContains(t, launch.StatusPrompt("publish"), "`sendportal` connector")
}

// TestSendPortalExampleNamesTheConsentCheck: the double opt-in warning
// stays in the example file and reaches the installed entry with it.
func TestSendPortalExampleNamesTheConsentCheck(t *testing.T) {
	raw, err := os.ReadFile(sendPortalExample)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range sendPortalConsentLines {
		assertContains(t, string(raw), want)
	}

	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "sales-b2c"}); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range sendPortalConsentLines {
		assertContains(t, string(installed), want)
		assertContains(t, out.String(), want)
	}
}

// TestSalesLaunchPublishNamesSendPortal: the launch type's publish prompt
// sends through the sendportal connector after the consent check, stops
// with no email connector, and names no mail provider (the owner's pick).
func TestSalesLaunchPublishNamesSendPortal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sales.yaml")
	mustWrite(t, path, salesWorkflow(t, false))
	wf, err := tracker.LoadWorkflow(path)
	if err != nil {
		t.Fatal(err)
	}
	launch := wf.ForType("launch")
	if launch == nil {
		t.Fatal("no launch type")
	}
	p := launch.StatusPrompt("publish")
	for _, want := range []string{"`sendportal` connector", "double opt-in is unverified in SendPortal", "no email connector", "the owner sends it"} {
		assertContains(t, p, want)
	}
	for _, provider := range []string{"SES", "Postmark", "SendGrid", "Mailgun", "Mailjet", "SMTP"} {
		if strings.Contains(p, provider) {
			t.Errorf("the launch publish prompt names mail provider %s", provider)
		}
	}
	// The other types' publish keeps the base prompt only.
	if strings.Contains(wf.ForType("landing").StatusPrompt("publish"), "sendportal") {
		t.Error("landing's publish prompt names sendportal")
	}
}
