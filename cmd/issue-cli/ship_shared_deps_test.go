package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

// TestShipCommandSkipsAnInstallThroughSharedNodeModules: issue-cli ship
// reads its check through ci.PrepareInstall, so an npm ci in a worktree
// whose node_modules links to the main checkout's is skipped while the
// lock file is main's, and the main checkout's packages stay (D-8; the
// workspace cases are internal/ship's TestShipNeverInstallsThroughWorkspaceLinks).
func TestShipCommandSkipsAnInstallThroughSharedNodeModules(t *testing.T) {
	proj, _, root, tree := shipFixture(t)
	exclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte("node_modules\npackage-lock.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{root, tree} {
		if err := os.WriteFile(filepath.Join(d, "package-lock.json"), []byte(`{"lockfileVersion":3}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dep := filepath.Join(root, "node_modules", "dep", "index.js")
	if err := os.MkdirAll(filepath.Dir(dep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dep, []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "node_modules"), filepath.Join(tree, "node_modules")); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	standin.Write(t, filepath.Join(bin, "npm"), "rm -rf node_modules/*\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, stdout, stderr := newTestContext(proj, false)
	if err := runShip(ctx, []string{"--check", "npm ci"}); err != nil {
		t.Fatalf("ship: %v\n%s%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout.String(), "skipped: npm ci: node_modules is shared with the main checkout") {
		t.Errorf("no skip line:\n%s", stdout)
	}
	if _, err := os.Stat(dep); err != nil {
		t.Errorf("ship's npm ci went through the link: %v\n%s", err, stdout)
	}
}
