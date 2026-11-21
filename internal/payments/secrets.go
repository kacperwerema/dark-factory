package payments

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// SecretRef names where a secret's value lives: an environment variable or
// a macOS Keychain item the project's owner created. The config holds only
// these references, never a value (the same rule as department connectors,
// docs/departments.md#secrets).
type SecretRef struct {
	Scheme  string // "env" or "keychain"
	Name    string // the variable, or the Keychain service
	Account string // Keychain account; optional
}

func (r SecretRef) String() string {
	if r.Account != "" {
		return r.Scheme + ":" + r.Name + "/" + r.Account
	}
	return r.Scheme + ":" + r.Name
}

var envVarRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseSecretRef reads env:<VAR> or keychain:<service>[/<account>]. Anything
// else is refused as a value that looks like a secret; the error never
// repeats it.
func ParseSecretRef(raw string) (SecretRef, error) {
	raw = strings.TrimSpace(raw)
	scheme, rest, ok := strings.Cut(raw, ":")
	switch {
	case raw == "":
		return SecretRef{}, fmt.Errorf("empty: wants env:<VAR> or keychain:<service>")
	case !ok || (scheme != "env" && scheme != "keychain"):
		return SecretRef{}, fmt.Errorf("not env:<VAR> or keychain:<service>: it looks like a secret value, keep the value in env: or keychain:")
	case scheme == "env":
		if !envVarRe.MatchString(rest) {
			return SecretRef{}, fmt.Errorf("env: wants a variable name ([A-Za-z_][A-Za-z0-9_]*)")
		}
		return SecretRef{Scheme: "env", Name: rest}, nil
	}
	svc, acct, _ := strings.Cut(rest, "/")
	if strings.TrimSpace(svc) == "" {
		return SecretRef{}, fmt.Errorf("keychain: wants a service name")
	}
	return SecretRef{Scheme: "keychain", Name: svc, Account: acct}, nil
}

// SecretValue reads ref's value; ok is false when it isn't set. Tests
// replace it.
var SecretValue = func(ref SecretRef) (string, bool) {
	switch ref.Scheme {
	case "env":
		v, ok := os.LookupEnv(ref.Name)
		return v, ok && v != ""
	case "keychain":
		if runtime.GOOS != "darwin" {
			return "", false
		}
		args := []string{"find-generic-password", "-s", ref.Name}
		if ref.Account != "" {
			args = append(args, "-a", ref.Account)
		}
		out, err := exec.Command("security", append(args, "-w")...).Output()
		if err != nil {
			return "", false
		}
		v := strings.TrimRight(string(out), "\n")
		return v, v != ""
	}
	return "", false
}
