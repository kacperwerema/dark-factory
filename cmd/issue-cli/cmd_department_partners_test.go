package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/partner"
)

// TestDepartmentPartnersJSON: department partners --json --slot email is
// the email rows only, each with its licence, read date, runs and money;
// without --slot every slot's rows; an unknown slot is refused naming the
// slots (AC2).
func TestDepartmentPartnersJSON(t *testing.T) {
	proj, _ := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, true)
	if err := runDepartment(ctx, []string{"partners", "--slot", "email"}); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(rows) < 2 {
		t.Fatalf("want the email rows (SendPortal, Listmonk), got %d:\n%s", len(rows), out)
	}
	var ids []string
	for _, r := range rows {
		if r["slot"] != "email" {
			t.Errorf("--slot email printed a %v row: %v", r["slot"], r["id"])
		}
		for _, k := range []string{"id", "name", "shape", "licence", "licence_url", "licence_read", "upstream", "memory", "runs", "money"} {
			if s, _ := r[k].(string); s == "" {
				t.Errorf("row %v has no %s", r["id"], k)
			}
		}
		ids = append(ids, r["id"].(string))
	}
	if ids[0] != "sendportal" || !slices.Contains(ids, "listmonk") {
		t.Errorf("email rows %v: want the default (sendportal) first, then listmonk", ids)
	}
	for _, r := range rows {
		if r["id"] == "listmonk" && !strings.HasPrefix(r["exception"].(string), "none: ") {
			t.Errorf("listmonk is AGPL with no recorded exception: %v", r["exception"])
		}
	}

	ctx, out = goalsCtx(t, proj, true)
	if err := runDepartment(ctx, []string{"partners"}); err != nil {
		t.Fatal(err)
	}
	var all []partner.CatalogRow
	if err := json.Unmarshal(out.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	want, _ := partner.Catalog()
	if len(all) != len(want) {
		t.Errorf("without --slot: %d rows, the catalog has %d", len(all), len(want))
	}

	ctx, _ = goalsCtx(t, proj, true)
	err := runDepartment(ctx, []string{"partners", "--slot", "crm"})
	if err == nil || !strings.Contains(err.Error(), "analytics, email, seo, social, video, support") {
		t.Errorf("unknown slot: %v", err)
	}

	// The text names social's default, Postiz, and its swap candidate.
	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"partners", "--slot", "social"}); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"== social == (templates: marketing)", "postiz: Postiz, tool · default", "mixpost: Mixpost Lite", "exception: factory/", "Listing turns nothing on"} {
		assertContains(t, out.String(), w)
	}
}

// TestDepartmentAddNamesItsPartnerOptions: department add sales-b2c names
// the catalog's options for its analytics and email slots (and support)
// with the command to read them, in text and JSON, and writes exactly the
// template's files and entry: no connector, no partner row (AC3).
func TestDepartmentAddNamesItsPartnerOptions(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)

	ctx, out := goalsCtx(t, proj, true)
	if err := runDepartment(ctx, []string{"add", "sales-b2c", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
		PartnerOptions map[string][]string `json:"partner_options"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := plan.PartnerOptions["analytics"]; len(got) == 0 || got[0] != "umami" {
		t.Errorf("analytics options %v: want umami first", got)
	}
	if got := plan.PartnerOptions["email"]; !slices.Contains(got, "sendportal") || !slices.Contains(got, "listmonk") {
		t.Errorf("email options %v: want sendportal and listmonk", got)
	}

	before := treeFiles(t, filepath.Dir(cfg))
	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "sales-b2c"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, w := range []string{
		"Partner options for Sales's slots (listed only: nothing is installed or turned on):",
		"  analytics: Umami (MIT, always up, default), Medama",
		"  email: SendPortal (MIT, always up, default), Listmonk (AGPL-3.0, always up)",
		"  support: Chatwoot",
		"issue-cli --project acme department partners --slot <slot>",
	} {
		assertContains(t, s, w)
	}

	// Nothing extra: the new files are the plan's, and the entry names no tool
	// from the catalog outside the template's own commented example.
	after := treeFiles(t, filepath.Dir(cfg))
	var added []string
	for _, f := range after {
		if !slices.Contains(before, f) {
			added = append(added, f)
		}
	}
	var planned []string
	for _, f := range plan.Files {
		planned = append(planned, f.Path)
	}
	slices.Sort(added)
	slices.Sort(planned)
	if !slices.Equal(added, planned) {
		t.Errorf("department add wrote %v, the plan names %v", added, planned)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The template's own commented examples (umami, sendportal) stay
	// comments: only the lines that load are checked.
	var live []string
	for _, l := range strings.Split(string(raw), "\n") {
		if x := strings.TrimSpace(l); x != "" && !strings.HasPrefix(x, "#") {
			live = append(live, strings.ToLower(l))
		}
	}
	for _, tool := range []string{"umami", "medama", "sendportal", "listmonk", "chatwoot", "partner_slots", "partner_options"} {
		if strings.Contains(strings.Join(live, "\n"), tool) {
			t.Errorf("projects.yaml names %s outside a comment after the add:\n%s", tool, raw)
		}
	}
}

// treeFiles are the files under root, relative and slash-separated.
func treeFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
