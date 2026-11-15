package partner

import (
	"bytes"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
)

// TestTrackedFilesHoldNoPartnerFile is the licence rule's "never vendored"
// guard (docs/departments.md#the-licence-rule), for every tool in the
// partners catalog: no tracked path carries one of a row's marks (paths
// only its own checkout has; our fixtures under testdata/ use none of
// them), .gitmodules names no row's id or upstream, and for each checkout
// named in PARTNER_CHECKOUTS (":"-separated; OPENMONTAGE_CHECKOUT too) no
// tracked file is byte for byte one of its files. OpenMontage (AGPL-3.0)
// was its first row: the video epic's D-3.
func TestTrackedFilesHoldNoPartnerFile(t *testing.T) {
	rows, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not a git checkout")
	}
	root := strings.TrimSpace(string(top))
	files := trackedBlobs(t, root)
	for p := range files {
		for _, r := range rows {
			for _, m := range r.Marks {
				if hasMark(p, m) {
					t.Errorf("%s looks like %s's own file (%s, %s): a partner is never vendored, it runs from its own checkout", p, r.Name, m, r.Licence)
				}
			}
		}
		if path.Base(p) == ".gitmodules" {
			raw, _ := os.ReadFile(root + "/" + p)
			low := bytes.ToLower(raw)
			for _, r := range rows {
				up := strings.ToLower(strings.TrimPrefix(r.Upstream, "https://"))
				if bytes.Contains(low, []byte(strings.ToLower(r.ID))) || bytes.Contains(low, []byte(up)) {
					t.Errorf("%s names a %s submodule: install it outside the repository", p, r.Name)
				}
			}
		}
	}
	var checkouts []string
	for _, v := range []string{os.Getenv("PARTNER_CHECKOUTS"), os.Getenv("OPENMONTAGE_CHECKOUT")} {
		for _, dir := range strings.Split(v, ":") {
			if dir = strings.TrimSpace(dir); dir != "" {
				checkouts = append(checkouts, dir)
			}
		}
	}
	if len(checkouts) == 0 {
		t.Log("PARTNER_CHECKOUTS unset: blob comparison skipped")
		return
	}
	for _, checkout := range checkouts {
		byBlob := map[string]string{}
		for p, b := range trackedBlobs(t, checkout) {
			byBlob[b] = p
		}
		for p, b := range files {
			if b == emptyBlob {
				continue
			}
			if their, ok := byBlob[b]; ok {
				t.Errorf("%s is byte for byte %s's %s: never copy a partner's files here", p, checkout, their)
			}
		}
	}
}

// TestPartnersCatalogAGPLRowsHaveMarks: every AGPL row in the bundled
// catalog lists its marks, and CheckRows refuses one that doesn't.
func TestPartnersCatalogAGPLRowsHaveMarks(t *testing.T) {
	rows, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rows {
		if r.AGPL() {
			n++
			if len(r.Marks) == 0 {
				t.Errorf("AGPL row %s has no marks:", r.ID)
			}
		}
	}
	if n == 0 {
		t.Fatal("no AGPL row in the catalog: the guard has nothing to read")
	}
	bad := []CatalogRow{{ID: "x", Name: "X", Slot: "email", Shape: "tool", Runs: "always", Licence: "AGPL-3.0",
		LicenceURL: "https://example.org/LICENSE", LicenceRead: "2026-11-08", Upstream: "https://example.org/x",
		Exception: "none: a candidate", Memory: "unmeasured", Money: "free"}}
	if err := CheckRows(bad); err == nil || !strings.Contains(err.Error(), "marks:") {
		t.Errorf("CheckRows on an AGPL row with no marks: %v", err)
	}
	bad[0].Marks = []string{"x.service"}
	if err := CheckRows(bad); err != nil {
		t.Errorf("CheckRows with marks: %v", err)
	}
}

// emptyBlob is git's id of an empty file: shared by everyone, not a copy.
const emptyBlob = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"

// trackedBlobs is every tracked file under dir's HEAD, path → blob id.
func trackedBlobs(t *testing.T, dir string) map[string]string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "ls-files", "-s").Output()
	if err != nil {
		t.Fatalf("git ls-files in %s: %v", dir, err)
	}
	blobs := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// <mode> <blob> <stage>\t<path>
		meta, p, ok := strings.Cut(line, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) < 2 {
			continue
		}
		blobs[p] = f[1]
	}
	return blobs
}

func hasMark(p, mark string) bool {
	if strings.HasSuffix(mark, "/") {
		return strings.HasPrefix(p, mark) || strings.Contains(p, "/"+mark)
	}
	return p == mark || strings.HasSuffix(p, "/"+mark)
}

func TestHasMark(t *testing.T) {
	for _, tc := range []struct {
		p, mark string
		want    bool
	}{
		{"AGENT_GUIDE.md", "AGENT_GUIDE.md", true},
		{"vendor/om/AGENT_GUIDE.md", "AGENT_GUIDE.md", true},
		{"docs/NOT_AGENT_GUIDE.md", "AGENT_GUIDE.md", false},
		{"third_party/remotion-composer/src/x.tsx", "remotion-composer/", true},
		{"skills/pipelines/a.md", "skills/pipelines/", true},
		{"internal/partner/testdata/openmontage/pipeline_defs/short-explainer.yaml", "skills/pipelines/", false},
	} {
		if got := hasMark(tc.p, tc.mark); got != tc.want {
			t.Errorf("hasMark(%q, %q) = %v", tc.p, tc.mark, got)
		}
	}
}
