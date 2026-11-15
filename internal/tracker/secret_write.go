package tracker

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ErrNoKeychain is SecretWrite off macOS: there is no Keychain to write to.
var ErrNoKeychain = errors.New("no Keychain on this machine (macOS only): keep the value in an env: reference instead")

// SecretWrite stores value in the Keychain item ref names, replacing what
// was there. It is the engine's one writer of a secret, used only by the
// owner's Connect socials page (the owner types the value; nothing reads it
// back to a page). The value goes to `security -i` on stdin, never in argv
// where ps would show it, and the tool's output is dropped: an error names
// the item, never the value. Tests replace it.
var SecretWrite = func(ref SecretRef, value string) error {
	if ref.Scheme != "keychain" {
		return fmt.Errorf("%s is not a keychain: reference", ref)
	}
	if runtime.GOOS != "darwin" {
		return ErrNoKeychain
	}
	line, err := keychainAddLine(ref, value)
	if err != nil {
		return err
	}
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(line + "\nquit\n")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("security could not write Keychain item %s", ref)
	}
	// security -i exits 0 even when a command inside it failed.
	if !SecretPresent(ref) {
		return fmt.Errorf("Keychain item %s is not there after the write: Keychain may be locked", ref)
	}
	return nil
}

// keychainAddLine is the add-generic-password command for security -i,
// with the value double-quoted (its parser takes \" and \\ inside quotes).
func keychainAddLine(ref SecretRef, value string) (string, error) {
	if err := CheckSecretValue(value); err != nil {
		return "", err
	}
	for _, s := range []string{ref.Name, ref.Account} {
		if strings.ContainsAny(s, "\"\\ \t\r\n") {
			return "", fmt.Errorf("Keychain item %s: the name holds a space, quote or backslash", ref)
		}
	}
	args := []string{"add-generic-password", "-U", "-s", ref.Name}
	if ref.Account != "" {
		args = append(args, "-a", ref.Account)
	}
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return strings.Join(args, " ") + ` -w "` + esc + `"`, nil
}

// CheckSecretValue refuses a value that can't be one secret: empty, too
// long, or holding a line break or another control character. The error
// never quotes the value.
func CheckSecretValue(value string) error {
	switch {
	case strings.TrimSpace(value) == "":
		return errors.New("the value is empty")
	case len(value) > 4096:
		return errors.New("the value is longer than 4096 bytes")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return errors.New("the value holds a line break or another control character")
		}
	}
	return nil
}
