package critique

import (
	"slices"
	"testing"
)

func TestCriticSessionIsNamed(t *testing.T) {
	argv, err := Argv("claude")
	if err != nil {
		t.Fatal(err)
	}
	name := SessionName("factory/a-fix: with $odd chars")
	if name != "critic-a-fix-with-odd-chars" {
		t.Errorf("name %q", name)
	}
	got := WithName(argv, name)
	if got[0] != "claude" || got[1] != "-n" || got[2] != name || !slices.Contains(got, "-p") {
		t.Errorf("argv %q", got)
	}
	if again := WithName(got, name); len(again) != len(got) {
		t.Errorf("named twice: %q", again)
	}
	codex, _ := Argv("codex")
	if out := WithName(codex, name); !slices.Equal(out, codex) {
		t.Errorf("codex argv changed: %q", out)
	}
	if WithName(nil, name) != nil {
		t.Error("no argv stays none")
	}
}
