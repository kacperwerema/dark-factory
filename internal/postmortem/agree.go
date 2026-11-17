package postmortem

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
)

// The queue's Agree answers one action, not a whole post-mortem: AgreeOne
// applies it as the round would, and AgreeAs ticks a copy of it, applying
// nothing, so a group of copies makes one memory, commit or experiment.

// AgreeOne applies the open action id of env.RetroPath and ticks it with
// "Applied: <what it made> · agreed <date> (<owner>)[: note]". It returns
// what the action made and the whole line. An action that fails to apply
// stays open and nothing is written.
func AgreeOne(env Env, id, note string) (made, line string, err error) {
	if env.Now.IsZero() {
		env.Now = time.Now()
	}
	mu.Lock()
	defer mu.Unlock()
	raw, err := os.ReadFile(env.RetroPath)
	if err != nil {
		return "", "", err
	}
	a, ok := openAction(string(raw), id)
	if !ok {
		return "", "", fmt.Errorf("%s is not an open action of this post-mortem", strings.ToUpper(id))
	}
	made, err = env.apply(a)
	if err != nil {
		return "", "", err
	}
	line = made + " · " + agreedLine(env.Owner, note, env.Now)
	next, err := MarkAs(string(raw), a.ID, OutcomeApplied, line)
	if err != nil {
		return made, "", err
	}
	if err := gitfile.WriteAtomic(env.RetroPath, []byte(next), 0o644); err != nil {
		return made, "", fmt.Errorf("%s applied (%s) but the retro could not be written: %w", a.ID, line, err)
	}
	return made, line, nil
}

// AgreeAs ticks the open action id of the retro at path as a copy of the
// one agreed at as ("<file>#<id>"), which made made: "Applied: as <as>:
// <made> · agreed <date> (<owner>)[: note]". Nothing is applied.
func AgreeAs(path, id, as, made, owner, note string, now time.Time) (string, error) {
	line := "as " + as + ": " + made + " · " + agreedLine(owner, note, now)
	return settle(path, id, OutcomeApplied, line)
}

func agreedLine(owner, note string, now time.Time) string {
	return "agreed " + outcomeLine("", owner, note, now)
}

func openAction(raw, id string) (Action, bool) {
	for _, a := range Pending(Actions(raw)) {
		if a.ID == strings.ToUpper(strings.TrimSpace(id)) {
			return a, true
		}
	}
	return Action{}, false
}
