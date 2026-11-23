package checks

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// The bundle is lib.sh with every part in place: nothing of the checkout
// lookup is left, and each part's helpers are there.
func TestLibBundlesEveryPart(t *testing.T) {
	lib := string(Lib)
	for _, gone := range []string{"$_ck_parts", "_ck_parts=", "# shellcheck source=lib/", "# shellcheck shell=bash"} {
		if strings.Contains(lib, gone) {
			t.Errorf("the bundle still holds %q", gone)
		}
	}
	parts, _ := fs.Glob(libFiles, "lib/*.sh")
	if len(parts) == 0 {
		t.Fatal("no parts embedded")
	}
	for _, p := range parts {
		data, _ := fs.ReadFile(libFiles, p)
		body := bytes.TrimPrefix(data, []byte(partShellLine))
		if !bytes.Contains(Lib, body) {
			t.Errorf("the bundle lacks %s", p)
		}
	}
	for _, fn := range []string{"step() {", "sandbox_project() {", "private_tmux() {", "seed_department() {", "summary() {", "_ck_real() {"} {
		if !strings.Contains(lib, "\n"+fn) {
			t.Errorf("the bundle lacks %s", fn)
		}
	}
}

func TestBundleLibRefusesAMissingOrUnusedPart(t *testing.T) {
	entry := "a=1\n_ck_parts=x/lib\n# shellcheck source=lib/one.sh\n. \"$_ck_parts/one.sh\"\nunset _ck_parts\n"
	ok := fstest.MapFS{
		"lib.sh":     {Data: []byte(entry)},
		"lib/one.sh": {Data: []byte(partShellLine + "one() { :; }")},
	}
	got, err := bundleLib(ok)
	if err != nil {
		t.Fatal(err)
	}
	if want := "a=1\none() { :; }\n"; string(got) != want {
		t.Errorf("bundle = %q, want %q", got, want)
	}
	for name, files := range map[string]fstest.MapFS{
		"missing": {"lib.sh": {Data: []byte(entry)}},
		"unused": {"lib.sh": {Data: []byte(entry)}, "lib/one.sh": {Data: []byte("x\n")},
			"lib/two.sh": {Data: []byte("y\n")}},
		"twice": {"lib.sh": {Data: []byte(entry + ". \"$_ck_parts/one.sh\"\n")}, "lib/one.sh": {Data: []byte("x\n")}},
	} {
		if _, err := bundleLib(files); err == nil {
			t.Errorf("%s: bundleLib passed", name)
		}
	}
}

// Sourced straight from the checkout (make ported-checks' default LIB), lib.sh
// finds its parts beside it although the library cds to $CHECKS_ROOT, and
// defines and does what the bundle does.
func TestLibSourcedFromTheCheckoutMatchesTheBundle(t *testing.T) {
	const body = "declare -F | sort\nstep S1 a\nexpect true ok\nsummary\n"
	bundled := runMachine(t, body)
	repo, err := filepath.Abs("lib.sh")
	if err != nil {
		t.Fatal(err)
	}
	direct := runMachine(t, body, "CHECKS_LIB="+repo)
	if bundled.out != direct.out {
		t.Errorf("sourced from the checkout:\n%s\nbundled:\n%s", direct.out, bundled.out)
	}
	contains(t, direct.out, "declare -f summary\n", "Boxes passed: S1\n")
}

// The split keeps every file of the library under 500 lines, so branches
// touching different helpers stop editing one big file.
func TestLibFilesStayUnder500Lines(t *testing.T) {
	files, _ := fs.Glob(libFiles, "lib/*.sh")
	for _, f := range append(files, "lib.sh") {
		data, _ := fs.ReadFile(libFiles, f)
		if n := bytes.Count(data, []byte("\n")); n > 500 {
			t.Errorf("%s is %d lines: split it by concern (lib/<concern>.sh, sourced from lib.sh)", f, n)
		}
	}
}
