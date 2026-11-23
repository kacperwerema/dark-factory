package spend

import "testing"

func TestReadOnlyCall(t *testing.T) {
	for _, c := range []struct {
		name, cmd string
		want      bool
	}{
		{"Read", "", true},
		{"Grep", "", true},
		{"Glob", "", true},
		{"Edit", "", false},
		{"Write", "", false},
		{"Bash", `grep -rn "x" internal | head -20`, true},
		{"Bash", `/usr/bin/grep -n foo a.go; sed -n '1,80p' b.go`, true},
		{"Bash", `rtk grep pattern`, true},
		{"Bash", `rtk read internal/x.go`, true},
		{"Bash", `ls -la cmd && cat a.go 2>&1 | wc -l`, true},
		{"Bash", `printf '%s\n' '----'; find . -name '*.go'`, true},
		{"Bash", `git log --oneline -5`, true},
		{"Bash", `/usr/bin/git -C /repo diff main`, true},
		{"Bash", `git branch -a`, true},
		{"Bash", `issue-cli --project acme show seo/x`, true},
		{"Bash", `./issue-cli --project acme checklist`, true},
		{"Bash", `issue-cli memory search token cost`, true},
		{"Bash", `issue-cli ref cli spend`, true},
		{"Bash", `sed -i '' 's/a/b/' x.go`, false},
		{"Bash", `sed 's/a/b/' x.go`, false},
		{"Bash", `find . -name '*.tmp' -delete`, false},
		{"Bash", `git commit -m x`, false},
		{"Bash", `git branch -D old`, false},
		{"Bash", `git stash`, false},
		{"Bash", `issue-cli check AC1`, false},
		{"Bash", `issue-cli memory add "x"`, false},
		{"Bash", `issue-cli transition --to testing`, false},
		{"Bash", `go test ./internal/x`, false},
		{"Bash", `make validate`, false},
		{"Bash", `cat a.go > b.go`, false},
		{"Bash", `cat <<'EOF' > x`, false},
		{"Bash", `grep $X a.go`, false},
		{"Bash", `python3 -c 'print(1)'`, false},
		{"Bash", `/opt/bin/read x`, false},
		{"Bash", ``, false},
	} {
		if got := ReadOnlyCall(c.name, c.cmd); got != c.want {
			t.Errorf("ReadOnlyCall(%s, %q) = %v, want %v", c.name, c.cmd, got, c.want)
		}
	}
}
