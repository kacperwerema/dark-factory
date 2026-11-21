package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/partner"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestSalesB2CPaymentsConnectorsPreset: department add sales-b2c writes the
// payments kit's two connectors commented out (keychain: references only,
// no live connector); uncommented, payments is a read-only tool and
// payments-refund sends and is held to the owner's approved output, and
// lint finds nothing about either.
func TestSalesB2CPaymentsConnectorsPreset(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "sales-b2c"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#   - name: payments\n",
		"#   - name: payments-refund\n",
		"#       PAYMENTS_TOKEN: keychain:payments-read-token",
		"#       PAYMENTS_TOKEN: keychain:payments-api-token",
		"#     needs_approved_output: true",
		"security add-generic-password -s payments-api-token",
		"mode: is the sandbox/live switch",
	} {
		assertContains(t, string(raw), want)
	}
	assertContains(t, out.String(), "payments-kit")
	proj = reloadDeptProject(t, cfg)
	if d := proj.DepartmentByKey("sales"); d == nil || len(d.Connectors) != 0 {
		t.Fatalf("sales installed with connectors %+v, want none", d)
	}
	// Every secret in the example is a reference; no value.
	for _, m := range regexp.MustCompile(`PAYMENTS_TOKEN: (\S+)`).FindAllStringSubmatch(string(raw), -1) {
		if !strings.HasPrefix(m[1], "keychain:") {
			t.Errorf("PAYMENTS_TOKEN is %q, want a keychain: reference", m[1])
		}
	}

	uncommentConnectors(t, cfg)
	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("sales")
	if d == nil {
		t.Fatal("uncommented: no sales department")
	}
	read, refund := deptConnector(d, "payments"), deptConnector(d, "payments-refund")
	if read == nil || refund == nil {
		t.Fatalf("uncommented: connectors %+v", d.Connectors)
	}
	if read.Kind != tracker.ConnectorTool || read.Command != "payments-kit" || read.Sends || read.NeedsApprovedOutput || read.Secrets["PAYMENTS_TOKEN"] != "keychain:payments-read-token" {
		t.Errorf("payments = %+v, want a read-only tool on the read token", *read)
	}
	if refund.Kind != tracker.ConnectorTool || refund.Command != "payments-kit" || !refund.Sends || !refund.NeedsApprovedOutput || refund.Secrets["PAYMENTS_TOKEN"] != "keychain:payments-api-token" {
		t.Errorf("payments-refund = %+v, want a held, sending tool on the api token", *refund)
	}
	for _, c := range []*tracker.Connector{read, refund} {
		if !strings.HasPrefix(c.Check, "payments-kit check -config ") {
			t.Errorf("%s check = %q, want payments-kit check", c.Name, c.Check)
		}
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
		if s := strings.ToLower(strings.ReplaceAll(f.Where+" "+f.Message, proj.Root(), "<root>")); f.Check == "owner-only" || strings.Contains(s, "payments") || strings.Contains(s, "connector") {
			t.Errorf("uncommented: lint finding about payments: %+v", f)
		}
	}
}

// TestSalesB2CChargingNamesPaymentsConnectors: the uncommented charging
// block's refund reads the payment through sales/payments and refunds
// through sales/payments-refund with the issue's slug as the key.
func TestSalesB2CChargingNamesPaymentsConnectors(t *testing.T) {
	path := t.TempDir() + "/sales.yaml"
	mustWrite(t, path, salesWorkflow(t, true))
	wf, err := tracker.LoadWorkflow(path)
	if err != nil {
		t.Fatal(err)
	}
	eff := wf.ForIssue(&tracker.Issue{Type: "refund", Status: "brief"})
	prompts := map[string]string{}
	for _, s := range eff.Statuses {
		prompts[s.Name] = s.Prompt
	}
	assertContains(t, prompts["brief"], "issue-cli connector exec sales/payments -- payment <payment-id>")
	assertContains(t, prompts["publish"], "issue-cli connector exec sales/payments-refund -- refund <payment-id> --amount <approved> --idempotency-key <slug>")
	if strings.Contains(salesWorkflow(t, false), "\n  checkout:") {
		t.Error("the charging block is on by default")
	}
}

// TestPartnersPaymentsSlot: the partners catalog's payments slot has the
// payments kit as its default, with its licence and the stand-in named,
// and the Sales template fills the slot.
func TestPartnersPaymentsSlot(t *testing.T) {
	asLead(t)
	proj, _ := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"partners", "--slot", "payments"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"Payments kit", "PolyForm-Noncommercial-1.0.0", "standin"} {
		assertContains(t, s, want)
	}
	tmpl, err := depttemplate.Load("sales-b2c")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tmpl.PartnerSlots, "payments") {
		t.Errorf("sales-b2c partner_slots = %v, want payments", tmpl.PartnerSlots)
	}
	rows, err := partner.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if got := partner.ForSlot(rows, "payments"); len(got) == 0 || got[0].ID != "payments-kit" || !got[0].Default {
		t.Errorf("payments slot rows = %+v, want payments-kit as the default", got)
	}
}
