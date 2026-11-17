package viewer

import (
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/recent"
)

// A git reading the Status page makes on every request (the release card's
// tags, branch tips, the branch's changelog, the commits since a candidate,
// the release config) depends on the refs and the config alone: a commit's
// content never changes. refsKept keeps each answer while the repository's
// refs stamp (gitdir.RefsStamp) is the one it was read under, so a warm page
// runs no git until a ref, a tag or the config changes, and the next request
// after one reads afresh. A new git reading on a page that depends only on
// refs and config goes through it too.
//
// It keeps only while machine readings are kept (recent.Window > 0: the
// viewer, from viewer.Run), so tests, issue-cli and a scenario viewer read
// git every time, as before. An answer read while the stamp is younger than
// refsKeptYoung is not kept: a second ref write in the same tick of a coarse
// filesystem clock could leave the stamp unchanged.

// refsKeptYoung is how old the newest ref, HEAD or config must be before an
// answer read under it is kept. A var so a test can keep at once.
var refsKeptYoung = 2 * time.Second

// maxRefsKept bounds the kept answers; past it, they start over.
const maxRefsKept = 4096

type refsAnswer struct {
	stamp string
	value any
}

var refsKeptAnswers = struct {
	sync.Mutex
	m map[string]refsAnswer
}{m: map[string]refsAnswer{}}

// refsKept is read's answer for key in the repository at root, kept until
// root's refs stamp changes.
func refsKept[T any](root string, key []string, read func() T) T {
	if recent.Window() <= 0 {
		return read()
	}
	stamp, newest, ok := gitdir.RefsStamp(root)
	if !ok {
		return read()
	}
	k := root + "\x00" + strings.Join(key, "\x00")
	refsKeptAnswers.Lock()
	a, found := refsKeptAnswers.m[k]
	refsKeptAnswers.Unlock()
	if found && a.stamp == stamp {
		if v, ok := a.value.(T); ok {
			return v
		}
	}
	// The stamp comes before the read: a ref changing during it leaves a
	// stamp older than the answer, so the next request reads again.
	v := read()
	if time.Since(newest) < refsKeptYoung {
		return v
	}
	refsKeptAnswers.Lock()
	if len(refsKeptAnswers.m) >= maxRefsKept {
		refsKeptAnswers.m = map[string]refsAnswer{}
	}
	refsKeptAnswers.m[k] = refsAnswer{stamp: stamp, value: v}
	refsKeptAnswers.Unlock()
	return v
}

// refsOutput is a kept git output and whether git succeeded.
type refsOutput struct {
	out string
	ok  bool
}

func (o refsOutput) get() (string, bool) { return o.out, o.ok }
