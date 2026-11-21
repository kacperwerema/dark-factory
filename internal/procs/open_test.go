package procs

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestInUseFromNamesTopEntries: lsof's n lines under the root (as given or
// through /private) name the root's direct child; other paths and p/f lines
// don't count.
func TestInUseFromNamesTopEntries(t *testing.T) {
	out := strings.Join([]string{
		"p123",
		"fcwd",
		"n/var/folders/x/T/pt-bins-1",
		"ftxt",
		"n/private/var/folders/x/T/pt-bins-2/issue-cli",
		"n/var/folders/x/T/com.google.chrome.for.testing.ab12/deep/file",
		"n/var/folders/x/Tother/pt-bins-3",
		"n/Users/me/file",
		"p456",
		"n/var/folders/x/T/",
	}, "\n")
	roots := []string{"/var/folders/x/T" + string(filepath.Separator), "/private/var/folders/x/T" + string(filepath.Separator)}
	got := inUseFrom([]byte(out), roots)
	var names []string
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "com.google.chrome.for.testing.ab12,pt-bins-1,pt-bins-2" {
		t.Fatalf("in use = %v", names)
	}
}
