package gitdir

import (
	"os"
	"path/filepath"
	"strings"
)

// Outside reports that git run in dir would find no repository, known from
// the files with no git call: GIT_DIR is unset and neither dir nor any
// folder above it holds a .git (folder or file) or is a bare repository
// (HEAD, objects/ and refs/). Fixture boards are temp folders with no
// repository: every git asked there failed after ~18ms (exit 128). Nothing
// is kept, so a .git made later is seen on the next call. Only a sure "no"
// is true: a folder git would refuse for another reason (a ceiling, another
// filesystem, a broken .git) is false, and git is asked as before.
func Outside(dir string) bool {
	if os.Getenv("GIT_DIR") != "" {
		return false
	}
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return false
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	if _, err := os.Stat(abs); err != nil {
		// git -C on a missing folder fails too, but say nothing sure.
		return false
	}
	for d := abs; ; {
		if exists(filepath.Join(d, ".git")) || bare(d) {
			return false
		}
		up := filepath.Dir(d)
		if up == d {
			return true
		}
		d = up
	}
}

// NoRepo is Outside for one git call: true only when its args need a
// repository (not init, clone, ls-remote, version, help, config, and no
// --git-dir, --work-tree or --no-index naming one elsewhere) and dir is
// outside any. A runner then answers "not a git repository" without starting
// git.
func NoRepo(dir string, args ...string) bool {
	for _, a := range args {
		if a == "--git-dir" || a == "--work-tree" || a == "--no-index" ||
			strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree=") {
			return false
		}
	}
	sub := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-C" || a == "-c" {
			if a == "-C" && i+1 < len(args) {
				dir = filepath.Join(dir, args[i+1])
				if filepath.IsAbs(args[i+1]) {
					dir = args[i+1]
				}
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		sub = a
		break
	}
	switch sub {
	case "", "init", "clone", "ls-remote", "version", "help", "config":
		return false
	}
	return Outside(dir)
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func bare(d string) bool {
	if fi, err := os.Stat(filepath.Join(d, "HEAD")); err != nil || fi.IsDir() {
		return false
	}
	for _, sub := range []string{"objects", "refs"} {
		if fi, err := os.Stat(filepath.Join(d, sub)); err != nil || !fi.IsDir() {
			return false
		}
	}
	return true
}
