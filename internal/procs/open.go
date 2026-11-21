package procs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// openTimeout caps the lsof call: over every process of the user it takes
// a few seconds on a busy Mac.
const openTimeout = 60 * time.Second

// lsofOutput is lsof's field output for the user's processes; a var for
// tests.
var lsofOutput = func(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "lsof", "-n", "-P", "-w", "-u", strconv.Itoa(os.Getuid()), "-F", "n")
	out, err := cmd.Output()
	// lsof exits 1 when some process couldn't be read; its output is still
	// whole for the rest.
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return out, nil
}

// InUseUnder names the entries right under root that a live process of
// this user holds: a file open in them, a working directory or the
// running executable (lsof's n lines, cwd and txt included). The names are
// root's direct children. An error means nothing is known: the caller
// must then take nothing.
func InUseUnder(root string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()
	out, err := lsofOutput(ctx)
	if err != nil {
		return nil, fmt.Errorf("lsof: %w", err)
	}
	return inUseFrom(out, rootForms(root)), nil
}

// rootForms are root as given and with its links resolved, each ending in
// a slash: lsof prints macOS's /var/folders/… as /private/var/folders/….
func rootForms(root string) []string {
	forms := []string{filepath.Clean(root)}
	if real, err := filepath.EvalSymlinks(root); err == nil && real != forms[0] {
		forms = append(forms, real)
	}
	for i := range forms {
		forms[i] += string(filepath.Separator)
	}
	return forms
}

func inUseFrom(out []byte, roots []string) map[string]bool {
	held := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "n") {
			continue
		}
		path := line[1:]
		for _, r := range roots {
			rest, ok := strings.CutPrefix(path, r)
			if !ok {
				continue
			}
			if name, _, _ := strings.Cut(rest, string(filepath.Separator)); name != "" {
				held[name] = true
			}
		}
	}
	return held
}
