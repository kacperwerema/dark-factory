package tracker

import (
	"strings"
	"testing"
)

// commitAt commits with the committer date pinned, so --since sees it there.
func (r *gitRepo) commitAt(date, msg string) {
	r.t.Helper()
	r.t.Setenv("GIT_COMMITTER_DATE", date+"T12:00:00")
	r.t.Setenv("GIT_AUTHOR_DATE", date+"T12:00:00")
	r.commit(msg)
}

const stubTest = "package v\n\nimport \"testing\"\n\nfunc TestStub(t *testing.T) {\n\tt.Log(\"%s\")\n}\n\nfunc TestOther(t *testing.T) {}\n"

// holdsRepo has TestStub in v/stub_test.go since 2026-11-01.
func holdsRepo(t *testing.T) *gitRepo {
	r := newGitRepo(t)
	r.write("v/stub_test.go", strings.Replace(stubTest, "%s", "old", 1))
	r.commitAt("2026-11-01", "add the stub test")
	return r
}

func holdsIssue() *Issue {
	return &Issue{Slug: "workflow/stub-flakes", Title: "TestStub flakes under load", Created: "2026-11-15",
		BodyRaw: "Seen twice in validate.\n\n## Design\n\nAdd TestOther coverage.\n"}
}

func TestMainHoldsNamesAFixToTheBriefsTest(t *testing.T) {
	r := holdsRepo(t)
	r.write("v/stub_test.go", strings.Replace(stubTest, "%s", "fixed", 1))
	r.commitAt("2026-11-15", "test(v): the stub goes through the helper")

	m := MainHolds(r.root, holdsIssue())
	if m == nil || len(m.Tests) != 1 {
		t.Fatalf("MainHolds = %+v", m)
	}
	text := MainHoldsPromptSection(m)
	for _, want := range []string{
		"## Already on main?",
		"Since this issue was created (2026-11-15), main changed the tests its brief names:",
		"- TestStub: ",
		" 2026-11-15 test(v): the stub goes through the helper",
		"go test -count=1 -run '^TestStub$' ./v/",
		"progress: comment",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("section misses %q:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(m.Summary(), "main may already hold it: ") || !strings.Contains(m.Summary(), "(TestStub)") {
		t.Errorf("summary = %q", m.Summary())
	}
}

func TestMainHoldsCountsAMessageNamingTheTest(t *testing.T) {
	r := holdsRepo(t)
	r.write("v/helper.go", "package v\n")
	r.commitAt("2026-11-16", "fix(v): the helper waits longer\n\nTestStub went red under load.")
	m := MainHolds(r.root, holdsIssue())
	if m == nil || !strings.Contains(strings.Join(m.Lines(), "\n"), "fix(v): the helper waits longer") {
		t.Fatalf("MainHolds = %+v", m)
	}
}

func TestMainHoldsLeavesOutWhatDoesntCount(t *testing.T) {
	cases := map[string]func(r *gitRepo, i *Issue){
		"before created": func(r *gitRepo, i *Issue) {
			r.write("v/stub_test.go", strings.Replace(stubTest, "%s", "early", 1))
			r.commitAt("2026-11-14", "test(v): TestStub early")
		},
		"own commit": func(r *gitRepo, i *Issue) {
			r.write("v/stub_test.go", strings.Replace(stubTest, "%s", "mine", 1))
			r.commitAt("2026-11-16", "test(v): TestStub\n\nRefs: workflow/stub-flakes")
		},
		"other test changed": func(r *gitRepo, i *Issue) {
			r.write("v/stub_test.go", strings.Replace(strings.Replace(stubTest, "%s", "old", 1), "TestOther(t *testing.T) {}", "TestOther(t *testing.T) { t.Log(1) }", 1))
			r.commitAt("2026-11-16", "test(v): other")
		},
		"no test named": func(r *gitRepo, i *Issue) {
			i.Title = "Stub flakes under load"
			r.write("v/stub_test.go", strings.Replace(stubTest, "%s", "x", 1))
			r.commitAt("2026-11-16", "test(v): stub")
		},
		"test not on main": func(r *gitRepo, i *Issue) {
			i.Title = "TestMissing should exist"
			r.write("v/notes.txt", "TestMissing\n")
			r.commitAt("2026-11-16", "chore: TestMissing mentioned")
		},
		"no created date": func(r *gitRepo, i *Issue) {
			i.Created = ""
			r.write("v/stub_test.go", strings.Replace(stubTest, "%s", "x", 1))
			r.commitAt("2026-11-16", "test(v): TestStub")
		},
		"branch underway": func(r *gitRepo, i *Issue) {
			r.write("v/stub_test.go", strings.Replace(stubTest, "%s", "x", 1))
			r.commitAt("2026-11-16", "test(v): TestStub")
			r.branchWith("work/"+i.Slug, "main", "v/mine.go")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			r := holdsRepo(t)
			i := holdsIssue()
			setup(r, i)
			if m := MainHolds(r.root, i); m != nil {
				t.Errorf("MainHolds = %+v, want nil", m)
			}
		})
	}
}

func TestBriefTestNamesReadTheTitleAndBriefOnly(t *testing.T) {
	i := &Issue{Title: "TestA_B and BenchmarkC fail", BodyRaw: "FuzzD too, TestA_B again; not Testing or Tested.\n\n## Why\n\nTestE.\n\n## Design\n\nTestF.\n\n## Acceptance Criteria\n\n- [ ] TestG\n"}
	got := strings.Join(BriefTestNames(i), ",")
	if got != "TestA_B,BenchmarkC,FuzzD,TestE" {
		t.Errorf("names = %s", got)
	}
}
