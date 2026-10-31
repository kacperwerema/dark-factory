package validations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFileExists_PassesForAFileWithContent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "departments/marketing/brand.md", "# Brand\n")
	issue := sampleIssue()
	issue.Department = "marketing"
	a := Action{Rule: "file_exists", Path: "departments/{{department}}/brand.md"}
	if err := FileExists(a, issue, Config{ProjectRoot: root}); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestFileExists_MissingEmptyAndDirFail(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "empty.md", "  \n\t\n")
	if err := os.MkdirAll(filepath.Join(root, "dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"missing.md": "missing.md is missing",
		"empty.md":   "empty.md is empty",
		"dir.md":     "dir.md is not a file",
	} {
		err := FileExists(Action{Rule: "file_exists", Path: path}, sampleIssue(), Config{ProjectRoot: root})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", path, want, err)
		}
	}
}

func TestFileExists_HintIsTemplated(t *testing.T) {
	issue := sampleIssue()
	issue.Department = "growth"
	a := Action{Rule: "file_exists", Path: "departments/{{department}}/icp.md",
		Hint: "open one: issue-cli create --department {{department}} --type foundation"}
	err := FileExists(a, issue, Config{ProjectRoot: t.TempDir()})
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, want := range []string{"departments/growth/icp.md is missing", "--department growth --type foundation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
}

func TestFileExists_DepartmentPathWithoutDepartment(t *testing.T) {
	err := FileExists(Action{Rule: "file_exists", Path: "departments/{{department}}/brand.md"}, sampleIssue(), Config{ProjectRoot: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "belongs to no department") {
		t.Fatalf("expected no-department error, got %v", err)
	}
}

func TestFileExists_PathMustStayInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"../outside.md", "/etc/hosts", "a/../../x"} {
		err := FileExists(Action{Rule: "file_exists", Path: p}, sampleIssue(), Config{ProjectRoot: root})
		if err == nil || !strings.Contains(err.Error(), "inside the project root") {
			t.Errorf("%s: want inside-root error, got %v", p, err)
		}
	}
}

func TestFileExists_NeedsPathAndRoot(t *testing.T) {
	if err := FileExists(Action{Rule: "file_exists"}, sampleIssue(), Config{ProjectRoot: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "requires 'path'") {
		t.Errorf("want requires-path error, got %v", err)
	}
	if err := FileExists(Action{Rule: "file_exists", Path: "x.md"}, sampleIssue(), Config{}); err == nil || !strings.Contains(err.Error(), "no project root") {
		t.Errorf("want no-root error, got %v", err)
	}
}
