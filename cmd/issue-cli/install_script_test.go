package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

// installScript is the repo's install.sh (the one README's curl | bash runs).
func installScript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("install.sh: %v", err)
	}
	return p
}

// TestInstallScriptNamesOnlyThisRepo: every GitHub address install.sh
// fetches from is kacperwerema/dark-factory.
func TestInstallScriptNamesOnlyThisRepo(t *testing.T) {
	b, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `REPO="kacperwerema/dark-factory"`) {
		t.Fatalf("install.sh does not set REPO to kacperwerema/dark-factory")
	}
	re := regexp.MustCompile(`(?:github\.com|githubusercontent\.com)/(?:repos/)?([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)`)
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		if m[1] != "kacperwerema/dark-factory" && m[1] != "$REPO/releases" {
			t.Errorf("install.sh names another repo: %s", m[0])
		}
	}
}

// TestGoModRetractsOldModulePathTags: v0.55.0 and v0.56.0 were tagged under
// the old module path; go.mod retracts them so @latest skips them.
func TestGoModRetractsOldModulePathTags(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^retract \[v0\.55\.0, v0\.56\.0\]$`).Match(b) {
		t.Fatalf("go.mod has no retract [v0.55.0, v0.56.0]:\n%s", b)
	}
}

// TestInstallScriptVerifiesWithShasum runs install.sh against a stand-in
// curl serving a release from a folder, on a PATH with shasum and no
// sha256sum (a Mac without coreutils): it verifies and installs both
// binaries, and a tampered archive is refused.
func TestInstallScriptVerifiesWithShasum(t *testing.T) {
	shasum, err := exec.LookPath("shasum")
	if err != nil {
		t.Skip("no shasum on this machine")
	}
	script := installScript(t)
	root := t.TempDir()
	release := filepath.Join(root, "release")
	bin := filepath.Join(root, "bin")
	for _, d := range []string{release, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Tools install.sh needs, linked one by one so sha256sum is absent.
	for _, tool := range []string{"bash", "sed", "head", "mktemp", "rm", "tar", "grep", "uname", "mkdir", "gzip", "cat", "perl", "mv", "chmod", "dirname", "basename"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			if tool == "perl" {
				continue
			}
			t.Skipf("no %s on this machine", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(shasum, filepath.Join(bin, "shasum")); err != nil {
		t.Fatal(err)
	}
	// The stand-in curl: the API call prints a tag, -o <file> <url> copies
	// the URL's last element from the release folder.
	curl := `#!/bin/bash
out=""; url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    --proto|--tlsv1.2|-fsSL) [ "$1" = "--proto" ] && shift; shift ;;
    *) url="$1"; shift ;;
  esac
done
case "$url" in
  https://api.github.com/repos/kacperwerema/dark-factory/releases/latest) printf '{\n  "tag_name": "v0.60.0",\n}\n' ;;
  https://github.com/kacperwerema/dark-factory/releases/download/v0.60.0/*) cat "${0%/bin/*}/release/${url##*/}" > "$out" ;;
  *) echo "unexpected url $url" >&2; exit 22 ;;
esac
`
	standin.Write(t, filepath.Join(bin, "curl"), curl)

	const name = "issue-viewer_v0.60.0_"
	osName := map[string]string{"Darwin": "darwin", "Linux": "linux"}
	unameS, _ := exec.Command("uname", "-s").Output()
	unameM, _ := exec.Command("uname", "-m").Output()
	arch := map[string]string{"x86_64": "amd64", "amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[strings.TrimSpace(string(unameM))]
	goos := osName[strings.TrimSpace(string(unameS))]
	if goos == "" || arch == "" {
		t.Skipf("install.sh has no release for %s %s", unameS, unameM)
	}
	stage := name + goos + "_" + arch
	asset := stage + ".tar.gz"
	archive := writeReleaseArchive(t, filepath.Join(release, asset), stage)
	sum := sha256.Sum256(archive)
	if err := os.WriteFile(filepath.Join(release, asset+".sha256"), []byte(fmt.Sprintf("%x  %s\n", sum, asset)), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(dest string) (string, error) {
		cmd := exec.Command(filepath.Join(bin, "bash"), script)
		cmd.Env = []string{"PATH=" + bin, "HOME=" + root, "INSTALL_DIR=" + dest, "TMPDIR=" + root}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	dest := filepath.Join(root, "installed")
	// An older install is in place: each binary has a second hard link, so
	// a write into the old inode (cp's way, which macOS answers with SIGKILL
	// at the next exec) shows in it.
	kept := plantOldBinaries(t, dest, "issue-viewer", "issue-cli")
	out, err := run(dest)
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, out)
	}
	for _, b := range []string{"issue-viewer", "issue-cli"} {
		got, err := os.ReadFile(filepath.Join(dest, b))
		if err != nil || string(got) != "#!/bin/sh\necho "+b+"\n" {
			t.Fatalf("%s not installed (%v): %q\n%s", b, err, got, out)
		}
	}
	assertReplacedNotOverwritten(t, dest, kept)
	if !strings.Contains(out, "installed issue-cli") {
		t.Fatalf("install.sh did not say what it installed:\n%s", out)
	}

	// A sum that does not match is refused.
	if err := os.WriteFile(filepath.Join(release, asset+".sha256"), []byte(fmt.Sprintf("%064x  %s\n", 0, asset)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run(filepath.Join(root, "tampered"))
	if err == nil || !strings.Contains(out, "checksum verification failed") {
		t.Fatalf("a wrong checksum was accepted (%v):\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "tampered", "issue-cli")); err == nil {
		t.Fatal("a tampered archive was installed")
	}
}

// writeReleaseArchive writes a tar.gz shaped like the release Action's: a
// <stage>/ folder with both binaries. It returns the archive's bytes.
func writeReleaseArchive(t *testing.T, path, stage string) []byte {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: stage + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"issue-viewer", "issue-cli"} {
		body := "#!/bin/sh\necho " + b + "\n"
		if err := tw.WriteHeader(&tar.Header{Name: stage + "/" + b, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []interface{ Close() error }{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// plantOldBinaries writes an old file for each name in dir and a hard link
// to it under keep/, returning the keep folder. assertReplacedNotOverwritten
// reads both back.
func plantOldBinaries(t *testing.T, dir string, names ...string) string {
	t.Helper()
	keep := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+".keep")
	for _, d := range []string{dir, keep} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range names {
		p := filepath.Join(dir, n)
		// not a stand-in: an old binary the install replaces, never run
		if err := os.WriteFile(p, []byte("old "+n+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(p, filepath.Join(keep, n)); err != nil {
			t.Fatal(err)
		}
	}
	return keep
}

// assertReplacedNotOverwritten: every file planted in keep still holds its
// old contents and is no longer the same file as dir's (a new inode was
// renamed into place), and dir holds no leftover temporary file.
func assertReplacedNotOverwritten(t *testing.T, dir, keep string) {
	t.Helper()
	ents, err := os.ReadDir(keep)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		old, err := os.ReadFile(filepath.Join(keep, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if string(old) != "old "+e.Name()+"\n" {
			t.Errorf("%s was written in place (the old inode now holds %d new bytes): macOS kills such a binary at exec", e.Name(), len(old))
		}
		a, errA := os.Stat(filepath.Join(keep, e.Name()))
		b, errB := os.Stat(filepath.Join(dir, e.Name()))
		if errA != nil || errB != nil {
			t.Fatalf("stat %s: %v %v", e.Name(), errA, errB)
		}
		if os.SameFile(a, b) {
			t.Errorf("%s is still the old inode", e.Name())
		}
		if b.Mode().Perm() != 0o755 {
			t.Errorf("%s mode %v, want 0755", e.Name(), b.Mode().Perm())
		}
	}
	ents, err = os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() != "issue-cli" && e.Name() != "issue-viewer" {
			t.Errorf("left behind in %s: %s", dir, e.Name())
		}
	}
}

// TestMakeInstallReplacesBinaryAtomically runs the Makefile's install
// (without the hooks step) into a BINDIR that already holds both binaries:
// each new build is renamed over the old file, never copied into it, and the
// installed issue-cli runs.
func TestMakeInstallReplacesBinaryAtomically(t *testing.T) {
	if testing.Short() {
		t.Skip("builds both binaries")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make on this machine")
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "bin")
	kept := plantOldBinaries(t, dest, "issue-viewer", "issue-cli")
	cmd := exec.Command("make", "-C", repo, "-o", "hooks", "install", "BINDIR="+dest, "VERSION=v0.0.0-installtest")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make install: %v\n%s", err, out)
	}
	assertReplacedNotOverwritten(t, dest, kept)
	v, err := exec.Command(filepath.Join(dest, "issue-cli"), "version").CombinedOutput()
	if err != nil || !strings.Contains(string(v), "v0.0.0-installtest") {
		t.Fatalf("installed issue-cli does not run (%v): %s", err, v)
	}
	if !strings.Contains(string(out), dest) {
		t.Errorf("make install does not say where it installed:\n%s", out)
	}
}
