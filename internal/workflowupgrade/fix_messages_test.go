package workflowupgrade

import (
	"strings"
	"testing"
)

// A features: record with two ids swapped fails with the expected list, in
// order, in the message.
func TestRecordDriftGivesTheExpectedOrder(t *testing.T) {
	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	data, err := ReadTemplate("review")
	if err != nil {
		t.Fatal(err)
	}
	if drift, err := RecordDrift(entries, "review.yaml", data); err != nil || drift != "" {
		t.Fatalf("the bundled record drifts already: %q (%v)", drift, err)
	}
	rec, _ := ReadRecord(data)
	want := RenderRecord(rec)
	a, b := "    - "+rec.Installed[0]+"\n", "    - "+rec.Installed[1]+"\n"
	swapped := strings.Replace(string(data), a+b, b+a, 1)
	if swapped == string(data) {
		t.Fatalf("no %q%q in review.yaml", a, b)
	}
	drift, err := RecordDrift(entries, "review.yaml", []byte(swapped))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(drift, "out of order") || !strings.Contains(drift, want) {
		t.Fatalf("drift:\n%s\nwant it to say out of order and give:\n%s", drift, want)
	}
}

// newEntryBranch is the repository with a new lifecycle entry file and the
// review template's record changed by edit.
func newEntryBranch(t *testing.T, category string, edit func(review, line string) string) Branch {
	t.Helper()
	const id = "aa-new-life"
	entry := CatalogPath + "/" + id + ".yaml"
	tmpl := TemplatesPath + "/review.yaml"
	review := readRepo(t, tmpl)
	head := map[string]string{
		entry: "id: " + id + "\nversion: v0.36.0\ncategory: " + category + "\npropose: false\nsources: [\"@prompt\"]\ndetect:\n  - path: statuses[].prompt\n",
		tmpl:  edit(review, "    - "+id+"@v0.36.0\n"),
	}
	b := realBranch(t, []string{entry, tmpl}, map[string]string{tmpl: review}, head)
	dir := b.HeadDir
	b.HeadDir = func(d string) []string {
		names := dir(d)
		if d == CatalogPath {
			names = append(names, id+".yaml")
		}
		return names
	}
	return b
}

// appendInstalled puts line after the last item of the installed: list.
func appendInstalled(t *testing.T, workflow, line string) string {
	t.Helper()
	at := strings.Index(workflow, "  installed:\n")
	if at < 0 {
		t.Fatal("no installed: list")
	}
	end := at + len("  installed:\n")
	for strings.HasPrefix(workflow[end:], "    - ") {
		end += strings.Index(workflow[end:], "\n") + 1
	}
	return workflow[:end] + line + workflow[end:]
}

// A branch that adds an entry and appends its id at the end of a template's
// installed: list, outside its category, fails catalog-check with the
// record in catalog order; the id in its place passes, and that record
// change alone asks no stamp bump. An unknown category is reported too.
func TestCatalogCheckReportsAnEntryAppendedOutsideItsCategory(t *testing.T) {
	appended := newEntryBranch(t, "lifecycle", func(review, line string) string {
		return appendInstalled(t, review, line)
	})
	rep, err := CheckBranch(appended, "v0.59.0")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rep.Problems, "\n")
	if rep.OK() || !strings.Contains(got, "review.yaml features: installed: is out of order") ||
		!strings.Contains(got, "  installed:\n    - aa-new-life@v0.36.0\n") ||
		!strings.Contains(got, "issue-cli workflow scan --template review") {
		t.Fatalf("appended outside its category: ok=%v problems:\n%s", rep.OK(), got)
	}

	inPlace := newEntryBranch(t, "lifecycle", func(review, line string) string {
		return strings.Replace(review, "  installed:\n", "  installed:\n"+line, 1)
	})
	rep, err = CheckBranch(inPlace, "v0.59.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rep.Problems {
		// The new entry is detected in the other templates too; only
		// review's record is the branch's.
		if strings.Contains(p, "/review.yaml") {
			t.Errorf("in its place: %s", p)
		}
	}
	if len(rep.Templates) > 0 {
		t.Errorf("in its place: templates %+v (a record-only change needs no stamp)", rep.Templates)
	}

	odd := newEntryBranch(t, "lifecyle", func(review, line string) string { return review })
	rep, err = CheckBranch(odd, "v0.59.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rep.Problems, "\n"); !strings.Contains(got, `aa-new-life: category "lifecyle" is not one of lifecycle, gates`) {
		t.Errorf("unknown category: problems:\n%s", got)
	}
}
