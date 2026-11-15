package tracker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

// SecretWrite hands the value to security on stdin: a stand-in security on
// PATH records its argv and stdin, and the value is only in stdin.
func TestSecretWriteKeepsTheValueOutOfArgv(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Keychain is macOS only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	// argv and stdin sit beside bin/, so the body is the same every run.
	standin.Write(t, filepath.Join(bin, "security"), "echo \"$*\" >> \"${0%/bin/*}/argv\"\ncat >> \"${0%/bin/*}/stdin\"\nexit 0\n")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	value := `SENTINEL a"b\c$d`
	ref := SecretRef{Scheme: "keychain", Name: "df-test", Account: "X_API_KEY"}
	if err := SecretWrite(ref, value); err != nil {
		t.Fatal(err)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if strings.Contains(string(argv), "SENTINEL") {
		t.Fatalf("the value is in argv: %s", argv)
	}
	want := `add-generic-password -U -s df-test -a X_API_KEY -w "SENTINEL a\"b\\c$d"`
	if !strings.Contains(string(stdin), want) {
		t.Fatalf("stdin = %q, want %q", stdin, want)
	}
}

func TestSecretWriteRefusesWhatCantBeOneSecret(t *testing.T) {
	ref := SecretRef{Scheme: "keychain", Name: "df-test"}
	for _, v := range []string{"", "   ", "two\nlines", "tab\tin", strings.Repeat("x", 5000)} {
		if _, err := keychainAddLine(ref, v); err == nil {
			t.Errorf("%q accepted", v)
		} else if v != "" && strings.Contains(err.Error(), v) {
			t.Errorf("the error repeats the value: %v", err)
		}
	}
	if err := SecretWrite(SecretRef{Scheme: "env", Name: "X"}, "v"); err == nil {
		t.Error("an env: reference was written")
	}
	if _, err := keychainAddLine(SecretRef{Scheme: "keychain", Name: "a b"}, "v"); err == nil {
		t.Error("a service name with a space was accepted")
	}
}
