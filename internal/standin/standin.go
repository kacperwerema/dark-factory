// Package standin writes the executable stand-ins tests put on PATH (a fake
// mem-guard, docker, codex) so that macOS's first-exec check is paid once
// per distinct script, not once per test.
//
// macOS checks a freshly written executable on its first exec: 0.1–2 s
// usually, now and then over 10 s, and the result is cached per file. A
// test that writes its stand-in fresh pays that every time. Write keeps
// each distinct script once in a cache folder named by its sha256 (under
// os.TempDir, shared by every test binary on the machine), runs it once
// there as a no-op, and hard-links it to the path the test asked for: the
// link is the same file, already checked, so the test's exec is quick.
//
//	standin.Write(t, filepath.Join(bin, "docker"), "cat \"$FIXTURE\"\n")
//
// A stand-in's body should be the same across tests: what differs (a
// fixture, an exit code, a log path) comes from the environment.
package standin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// WarmEnv is set on the warm run; the script exits 0 at once under it.
const WarmEnv = "STANDIN_WARM"

// Header starts every stand-in: the shell, then the warm run's no-op.
const Header = "#!/bin/sh\n[ -n \"$" + WarmEnv + "\" ] && exit 0\n"

// Dir is the cache folder; a var so a test can point it elsewhere.
var Dir = func() string { return filepath.Join(os.TempDir(), "dark-factory-standins") }

// Write puts an executable sh script running body at path: Header, then
// body. A body that starts with its own "#!" line (#!/bin/bash) keeps it,
// with the warm run's no-op right below; its interpreter must read the
// no-op line as sh does (sh, bash, zsh). path must not exist yet. The file is read-only (a hard link shares
// the cached file, so writing through it would change every test's copy);
// to change a stand-in, remove it and Write again.
func Write(t testing.TB, path, body string) {
	t.Helper()
	if err := write(path, body); err != nil {
		t.Fatalf("stand-in %s: %v", path, err)
	}
}

func write(path, body string) error {
	script := Script(body)
	cached, err := cache(script)
	if err == nil && os.Link(cached, path) == nil {
		return nil
	}
	// Another file system, or no cache: a fresh file, warmed in place.
	if err := os.WriteFile(path, []byte(script), 0o555); err != nil {
		return err
	}
	return warm(path)
}

// Script is the file Write writes for body: Header then body, or body's own
// "#!" line, the warm no-op, then the rest of body.
func Script(body string) string {
	if !strings.HasPrefix(body, "#!") {
		return Header + body
	}
	shebang, rest, _ := strings.Cut(body, "\n")
	return shebang + "\n" + strings.TrimPrefix(Header, "#!/bin/sh\n") + rest
}

// cache is the cached, warmed copy of script, made if it is missing or
// differs from script.
func cache(script string) (string, error) {
	dir := Dir()
	path := cachePath(script)
	if b, err := os.ReadFile(path); err == nil && string(b) == script {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Written aside, warmed, then renamed into place: a rename keeps the
	// file, so its check stays cached, and no other test binary links a
	// half-written or cold one.
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(script)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		return "", fmt.Errorf("write: %v %v", werr, cerr)
	}
	if err := os.Chmod(tmp.Name(), 0o555); err != nil {
		return "", err
	}
	if err := warm(tmp.Name()); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

// cachePath is where the cache keeps script: Dir, then its sha256.
func cachePath(script string) string {
	sum := sha256.Sum256([]byte(script))
	return filepath.Join(Dir(), hex.EncodeToString(sum[:]))
}

func warm(path string) error {
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), WarmEnv+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("warm run: %v %s", err, out)
	}
	return nil
}
