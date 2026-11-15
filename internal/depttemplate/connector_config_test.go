package depttemplate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func connectorConfig(t *testing.T, depts string) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "issues"), 0o755)
	cfg := filepath.Join(dir, "projects.yaml")
	body := "projects:\n  - name: Acme # the sandbox\n    issues: " + filepath.Join(dir, "issues") + "\n    departments:\n" + depts
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func loadDept(t *testing.T, cfg, dept string) *tracker.Department {
	t.Helper()
	ps, err := tracker.LoadProjects(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return ps[0].DepartmentByKey(dept)
}

const socialItem = "- name: social\n  kind: mcp\n  url: http://127.0.0.1:4007/api/mcp\n  # the key\n  headers:\n    Authorization: \"Bearer keychain:postiz\"\n"

func TestPlanConnectorAddsTheEntryAndKeepsEveryOtherByte(t *testing.T) {
	for name, depts := range map[string]string{
		"no connectors key": "      - name: Marketing\n        role: SEO # keep me\n",
		"a list already":    "      - name: Marketing\n        connectors:\n          - name: umami\n            kind: tool\n            command: curl\n        role: SEO # keep me\n",
		"an empty key":      "      - name: Marketing\n        connectors:\n        role: SEO # keep me\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := connectorConfig(t, depts)
			before, _ := os.ReadFile(cfg)
			pl, err := PlanConnector(cfg, "acme", "Marketing", "social", socialItem)
			if err != nil {
				t.Fatal(err)
			}
			if now, _ := os.ReadFile(cfg); string(now) != string(before) {
				t.Fatal("planning wrote the file")
			}
			if err := pl.Write(); err != nil {
				t.Fatalf("%v\n%s", err, pl.After)
			}
			after, _ := os.ReadFile(cfg)
			for _, l := range strings.Split(string(before), "\n") {
				if l != "        connectors:" && !strings.Contains(string(after), l) {
					t.Errorf("line %q is gone", l)
				}
			}
			if !strings.Contains(string(after), "# the key") || !strings.Contains(string(after), "# keep me") {
				t.Errorf("comments lost:\n%s", after)
			}
			d := loadDept(t, cfg, "marketing")
			if d.Role != "SEO" {
				t.Errorf("role = %q", d.Role)
			}
			found := false
			for _, c := range d.Connectors {
				found = found || (c.Name == "social" && c.URL == "http://127.0.0.1:4007/api/mcp")
			}
			if !found {
				t.Errorf("no social connector:\n%s", after)
			}
		})
	}
}

func TestPlannedWriteRefusesAFileChangedSinceTheDiff(t *testing.T) {
	cfg := connectorConfig(t, "      - name: Marketing\n")
	pl, err := PlanConnector(cfg, "acme", "Marketing", "social", socialItem)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg)
	os.WriteFile(cfg, append(b, []byte("# edited by hand\n")...), 0o644)
	if err := pl.Write(); !errors.Is(err, ErrChanged) {
		t.Fatalf("Write = %v, want ErrChanged", err)
	}
	if now, _ := os.ReadFile(cfg); !strings.HasSuffix(string(now), "# edited by hand\n") {
		t.Fatal("the hand edit was overwritten")
	}
}

func TestPlanConnectorRefusesASecondOfTheSameName(t *testing.T) {
	cfg := connectorConfig(t, "      - name: Marketing\n        connectors:\n          - name: social\n            kind: tool\n            command: curl\n")
	if _, err := PlanConnector(cfg, "acme", "Marketing", "social", socialItem); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanSecretsAddsOptionalReferencesOnce(t *testing.T) {
	cfg := connectorConfig(t, "      - name: Marketing\n        connectors:\n"+indentBlock(socialItem, 10)+"          - name: umami\n            kind: tool\n            command: curl\n")
	refs := map[string]string{"X_API_KEY": "keychain:postiz-env/X_API_KEY", "X_API_SECRET": "keychain:postiz-env/X_API_SECRET"}
	pl, err := PlanSecrets(cfg, "acme", "Marketing", "social", refs)
	if err != nil || pl == nil {
		t.Fatalf("plan = %v, %v", pl, err)
	}
	if err := pl.Write(); err != nil {
		t.Fatalf("%v\n%s", err, pl.After)
	}
	d := loadDept(t, cfg, "marketing")
	var social, umami *tracker.Connector
	for i := range d.Connectors {
		switch d.Connectors[i].Name {
		case "social":
			social = &d.Connectors[i]
		case "umami":
			umami = &d.Connectors[i]
		}
	}
	if social == nil || umami == nil {
		t.Fatalf("connectors lost: %+v", d.Connectors)
	}
	for v, ref := range refs {
		if social.Secrets[v] != ref || !social.IsOptional(v) {
			t.Errorf("%s: secrets %q optional %v", v, social.Secrets[v], social.IsOptional(v))
		}
	}
	if len(umami.Secrets) != 0 {
		t.Errorf("umami got secrets: %v", umami.Secrets)
	}
	// Again, plus one more: only the new one is added.
	refs["LINKEDIN_CLIENT_ID"] = "keychain:postiz-env/LINKEDIN_CLIENT_ID"
	pl, err = PlanSecrets(cfg, "acme", "Marketing", "social", refs)
	if err != nil || pl == nil {
		t.Fatalf("second plan = %v, %v", pl, err)
	}
	if strings.Count(string(pl.After), "X_API_KEY: keychain") != 1 {
		t.Errorf("X_API_KEY written twice:\n%s", pl.After)
	}
	if err := pl.Write(); err != nil {
		t.Fatal(err)
	}
	if pl, err := PlanSecrets(cfg, "acme", "Marketing", "social", refs); err != nil || pl != nil {
		t.Errorf("nothing new to add: plan %v, err %v", pl, err)
	}
}

func TestPlanSecretsRefusesAValueInsteadOfAReference(t *testing.T) {
	cfg := connectorConfig(t, "      - name: Marketing\n        connectors:\n"+indentBlock(socialItem, 10))
	if _, err := PlanSecrets(cfg, "acme", "Marketing", "social", map[string]string{"X_API_KEY": "s3cr3t-value"}); err == nil {
		t.Fatal("a raw value was accepted as a reference")
	} else if strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("the error repeats the value: %v", err)
	}
}

func TestExampleItemIsTheMarketingSocialConnector(t *testing.T) {
	item, err := ExampleItem("marketing", "social")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(item, "- name: social\n") || !strings.Contains(item, "per_run:") || !strings.Contains(item, "Bearer keychain:postiz") {
		t.Fatalf("item:\n%s", item)
	}
	if strings.Contains(item, "name: umami") {
		t.Fatalf("the next connector leaked in:\n%s", item)
	}
}
