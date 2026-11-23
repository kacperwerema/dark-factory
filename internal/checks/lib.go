package checks

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
)

// libFiles is lib.sh, the entry point every check script sources, and the
// parts in lib/ it sources, one concern each.
//
//go:embed lib.sh lib/*.sh
var libFiles embed.FS

// Lib is the helper library every check script sources ($CHECKS_LIB): lib.sh
// with each part it sources inlined in its place, one file as before the
// split, so check-script --lib prints all of it and a run needs no lib/ beside
// the cached copy (LibPath).
var Lib = mustBundleLib(libFiles)

// libPartLine is lib.sh's line sourcing a part; libPartsLine one of the lines
// that find the parts in a checkout, which the bundle drops.
var (
	libPartLine  = regexp.MustCompile(`^\. "\$_ck_parts/([a-z_]+\.sh)"$`)
	libPartsLine = regexp.MustCompile(`^(_ck_parts=.*|unset _ck_parts|# shellcheck source=lib/[a-z_]+\.sh)$`)
)

// partShellLine opens every part, for shellcheck run on the part alone.
const partShellLine = "# shellcheck shell=bash\n"

func mustBundleLib(files fs.FS) []byte {
	b, err := bundleLib(files)
	if err != nil {
		panic(err)
	}
	return b
}

// bundleLib inlines lib.sh's parts. Every part in lib/ is sourced exactly
// once, and lib.sh names no part that isn't there.
func bundleLib(files fs.FS) ([]byte, error) {
	entry, err := fs.ReadFile(files, "lib.sh")
	if err != nil {
		return nil, err
	}
	parts, err := fs.Glob(files, "lib/*.sh")
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	var used []string
	for line := range bytes.Lines(entry) {
		text := string(bytes.TrimRight(line, "\n"))
		if libPartsLine.MatchString(text) {
			continue
		}
		m := libPartLine.FindStringSubmatch(text)
		if m == nil {
			out.Write(line)
			continue
		}
		name := "lib/" + m[1]
		if slices.Contains(used, name) {
			return nil, fmt.Errorf("lib.sh sources %s twice", name)
		}
		part, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("lib.sh sources %s: %w", name, err)
		}
		used = append(used, name)
		// A part's shell= line is for shellcheck on the part alone; in the
		// middle of the bundle it would be a directive out of place.
		part = bytes.TrimPrefix(part, []byte(partShellLine))
		out.Write(part)
		if !bytes.HasSuffix(part, []byte("\n")) {
			out.WriteByte('\n')
		}
	}
	for _, p := range parts {
		if !slices.Contains(used, p) {
			return nil, fmt.Errorf("lib.sh never sources %s", p)
		}
	}
	return out.Bytes(), nil
}
