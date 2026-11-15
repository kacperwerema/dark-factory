// Package shell holds the one way a value is written into a shell command.
package shell

import (
	"regexp"
	"strings"
)

// safeWord is a word that means the same quoted or not, to sh, bash and zsh.
// "=" stays out: zsh expands a leading =word to a command's path, and a first
// word holding one is an assignment. Empty stays out: it would vanish.
var safeWord = regexp.MustCompile(`^[A-Za-z0-9._/:@+-]+$`)

// Quote writes s as one shell word: bare when that is plainly safe, so a
// printed command stays readable, else in single quotes. Every value put into
// a command goes through it (TestOneShellQuote).
func Quote(s string) string {
	if safeWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
