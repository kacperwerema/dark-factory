package checks

import (
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// buildDirs are the folders a build of issue-cli is made from.
var buildDirs = []string{"cmd", "internal"}

// StaleBuild says that a build is older than the code in its checkout.
type StaleBuild struct {
	Built   time.Time // the build's mtime
	Changed time.Time // when the newest change happened
	What    string    // what changed: a path relative to the checkout, or "commit <short>"
}

// BuildStale compares the build's mtime with the newest commit and the newest
// Go source or embedded file under cmd/ and internal/ in root, and returns what
// is newer than the build, or nil when nothing is. A checkout without those folders or outside
// git only has fewer things to compare. A commit made after the build counts
// even when the build holds its code: the mtime can't tell, and a rebuild is
// cheap.
func BuildStale(root, build string) (*StaleBuild, error) {
	fi, err := os.Stat(build)
	if err != nil {
		return nil, err
	}
	built := fi.ModTime()
	stale := newestCommit(root)
	file, err := newestFile(root)
	if err != nil {
		return nil, err
	}
	if stale == nil || (file != nil && file.Changed.After(stale.Changed)) {
		stale = file
	}
	if stale == nil || !stale.Changed.After(built) {
		return nil, nil
	}
	stale.Built = built
	return stale, nil
}

func newestCommit(root string) *StaleBuild {
	args := append([]string{"-C", root, "log", "-1", "--format=%ct %h", "--"}, buildDirs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	secs, short, ok := strings.Cut(strings.TrimSpace(string(out)), " ")
	if !ok {
		return nil
	}
	ct, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return nil
	}
	return &StaleBuild{Changed: time.Unix(ct, 0), What: "commit " + short}
}

// newestFile finds the newest file a build is made from: Go sources (not
// _test.go files) and the files their go:embed lines name. Anything else a run leaves there (a clilog,
// a test's output) changes no build.
func newestFile(root string) (*StaleBuild, error) {
	var newest *StaleBuild
	see := func(path string) error {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return nil // gone since it was listed
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || newest != nil && !info.ModTime().After(newest.Changed) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		newest = &StaleBuild{Changed: info.ModTime(), What: rel}
		return nil
	}
	for _, dir := range buildDirs {
		top := filepath.Join(root, dir)
		err := filepath.WalkDir(top, func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil // gone mid-walk, or the checkout has no such folder
			}
			if err != nil {
				return err
			}
			if path != top && ignoredByGo(d.Name()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				// A test file is in no build: editing one leaves the binary current.
				if strings.HasSuffix(d.Name(), ".go") && !strings.HasSuffix(d.Name(), "_test.go") {
					return see(path)
				}
				return nil
			}
			for _, file := range embeddedFiles(path) {
				if err := see(file); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", dir, err)
		}
	}
	return newest, nil
}

// ignoredByGo is a name the go command leaves out of ./... and a package.
func ignoredByGo(name string) bool {
	return name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// embeddedFiles lists the files the package in dir embeds. A package go/build
// can't fully read still gives the patterns of the files it could: the broken
// file is itself a Go source that counts.
func embeddedFiles(dir string) []string {
	pkg, _ := build.ImportDir(dir, 0)
	var files []string
	source := os.DirFS(dir)
	for _, pattern := range pkg.EmbedPatterns {
		pattern, all := strings.CutPrefix(pattern, "all:")
		matches, err := fs.Glob(source, pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			// A folder embeds what is under it, without hidden names unless all:.
			_ = fs.WalkDir(source, match, func(name string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil // gone mid-walk
				}
				if d.IsDir() {
					if name != match && !all && hiddenFromEmbed(d.Name()) {
						return fs.SkipDir
					}
					return nil
				}
				if name == match || all || !hiddenFromEmbed(d.Name()) {
					files = append(files, filepath.Join(dir, filepath.FromSlash(name)))
				}
				return nil
			})
		}
	}
	return files
}

// hiddenFromEmbed is a name a go:embed folder pattern skips.
func hiddenFromEmbed(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}
