package payments

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Config is a project's payments.yaml (docs/payments.md#config).
type Config struct {
	// Provider is the adapter's registered name (standin, later stripe, …).
	Provider string `yaml:"provider"`
	// Mode is sandbox (default) or live. Live is the owner's switch, and an
	// adapter that has no live side (the stand-in) refuses it.
	Mode string `yaml:"mode"`
	// Listen is the address serve listens on; 127.0.0.1:8787 by default.
	Listen string `yaml:"listen"`
	// PublicURL is where buyers and the provider's webhooks reach the kit.
	PublicURL string `yaml:"public_url"`
	// EventsURL is the app's endpoint every neutral event is POSTed to.
	EventsURL string `yaml:"events_url"`
	// StateDir holds handled.jsonl (and invoices/ under invoice: record);
	// relative to the config file.
	StateDir string `yaml:"state_dir"`
	// Invoice is the invoice hook: none (default) or record.
	Invoice string `yaml:"invoice"`
	// Secrets are the kit's own: api_token (the app's bearer token),
	// events_secret (signs what the kit sends the app) and, optionally,
	// read_token (a bearer for the read routes only). References only.
	Secrets map[string]string `yaml:"secrets"`
	// ProviderSettings are the adapter's; the keys its Registration names
	// in Secrets must be references.
	ProviderSettings map[string]string `yaml:"provider_settings"`
	// Consent turns on the consent flow: the waiver record, confirmation
	// messages and the two-step withdrawal and cancel pages.
	Consent *ConsentConfig `yaml:"consent"`

	// Dir is the folder of the config file; StateDir is resolved from it.
	Dir string `yaml:"-"`
}

// The kit's own secrets.
const (
	SecretAPIToken     = "api_token"
	SecretEventsSecret = "events_secret"
	// SecretReadToken is optional: a bearer that reaches only
	// GET /v1/payments/<id> and GET /v1/provider.
	SecretReadToken = "read_token"
)

// Settings is what an adapter's New gets: the resolved provider settings
// (secret values read from their references) and where the kit is reached.
type Settings struct {
	Mode   string
	Values map[string]string
	// PublicURL is the kit's public base URL; a hosted page of an
	// in-process adapter lives under it.
	PublicURL string
	// WebhookURL is PublicURL + /v1/webhooks/<provider>.
	WebhookURL string
}

// Registration is one provider adapter.
type Registration struct {
	Name string
	// Secrets are the provider_settings keys that must be references.
	Secrets []string
	// Required are the provider_settings keys that must be present.
	Required []string
	// Live: the adapter can take real money (mode: live).
	Live bool
	New  func(Settings) (Provider, error)
}

var (
	regMu    sync.Mutex
	registry = map[string]Registration{}
)

// Register adds an adapter; a second registration under one name panics.
// An adapter calls it from init.
func Register(r Registration) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[r.Name]; dup {
		panic("payments: provider " + r.Name + " registered twice")
	}
	registry[r.Name] = r
}

// Registered lists the adapters' names, sorted.
func Registered() []string {
	regMu.Lock()
	defer regMu.Unlock()
	var out []string
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func lookup(name string) (Registration, bool) {
	regMu.Lock()
	defer regMu.Unlock()
	r, ok := registry[name]
	return r, ok
}

// LoadConfig reads path; unknown keys are refused so a typo doesn't pass
// as a default.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	c.Dir = dirOf(path)
	c.defaults()
	return c, nil
}

func dirOf(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[:i]
	}
	return "."
}

func (c *Config) defaults() {
	if c.Mode == "" {
		c.Mode = "sandbox"
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8787"
	}
	if c.PublicURL == "" {
		c.PublicURL = "http://" + c.Listen
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if c.StateDir == "" {
		c.StateDir = ".payments"
	}
	if c.Invoice == "" {
		c.Invoice = "none"
	}
}

// StatePath is StateDir resolved against the config's folder.
func (c Config) StatePath() string {
	if strings.HasPrefix(c.StateDir, "/") || c.Dir == "" {
		return c.StateDir
	}
	return c.Dir + "/" + c.StateDir
}

// Problem is one thing wrong with a config, by field.
type Problem struct {
	Field string
	Msg   string
}

func (p Problem) String() string { return p.Field + ": " + p.Msg }

// Validate lists every problem; none means Open can start the kit. It never
// reads a secret's value.
func (c Config) Validate() []Problem {
	var ps []Problem
	add := func(f, format string, a ...any) { ps = append(ps, Problem{f, fmt.Sprintf(format, a...)}) }
	reg, ok := lookup(c.Provider)
	switch {
	case c.Provider == "":
		add("provider", "missing; registered: %s", strings.Join(Registered(), ", "))
	case !ok:
		add("provider", "%q is not a registered adapter; registered: %s", c.Provider, strings.Join(Registered(), ", "))
	}
	switch c.Mode {
	case "sandbox":
	case "live":
		if ok && !reg.Live {
			add("mode", "%s has no live side; use mode: sandbox", c.Provider)
		}
	default:
		add("mode", "wants sandbox or live, got %q", c.Mode)
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		add("listen", "wants host:port, got %q", c.Listen)
	}
	if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		add("public_url", "wants an http(s) URL, got %q", c.PublicURL)
	}
	if u, err := url.Parse(c.EventsURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		add("events_url", "wants the app's http(s) endpoint for events, got %q", c.EventsURL)
	}
	if c.Mode == "live" {
		for _, f := range []struct{ name, v string }{{"public_url", c.PublicURL}, {"events_url", c.EventsURL}} {
			if u, err := url.Parse(f.v); err == nil && u.Scheme == "http" && !isLoopback(u.Hostname()) {
				add(f.name, "live wants https off this machine, got %q", f.v)
			}
		}
	}
	if c.Invoice != "none" && c.Invoice != "record" {
		add("invoice", "wants none or record, got %q", c.Invoice)
	}
	for _, k := range sortedKeys(c.Secrets) {
		switch k {
		case SecretAPIToken, SecretEventsSecret:
		case SecretReadToken:
			if _, err := ParseSecretRef(c.Secrets[k]); err != nil {
				add("secrets."+k, "%v", err)
			}
		default:
			add("secrets."+k, "unknown; the kit's secrets are %s, %s and (optional) %s", SecretAPIToken, SecretEventsSecret, SecretReadToken)
		}
	}
	for _, k := range []string{SecretAPIToken, SecretEventsSecret} {
		raw, have := c.Secrets[k]
		if !have {
			add("secrets."+k, "missing; wants env:<VAR> or keychain:<service>")
			continue
		}
		if _, err := ParseSecretRef(raw); err != nil {
			add("secrets."+k, "%v", err)
		}
	}
	if rt, have := c.Secrets[SecretReadToken]; have {
		a, errA := ParseSecretRef(c.Secrets[SecretAPIToken])
		b, errB := ParseSecretRef(rt)
		if errA == nil && errB == nil && a == b {
			add("secrets."+SecretReadToken, "is the same reference as api_token: the read token must be a key of its own")
		}
	}
	if c.Mode == "live" && ok && reg.Live {
		ps = append(ps, c.liveProblems(reg)...)
	}
	if c.Consent != nil {
		ps = append(ps, c.Consent.problems(c.Dir, c.Mode == "live")...)
	}
	if ok {
		for _, k := range reg.Required {
			if _, have := c.ProviderSettings[k]; !have {
				add("provider_settings."+k, "missing (%s needs it)", c.Provider)
			}
		}
		for _, k := range reg.Secrets {
			raw, have := c.ProviderSettings[k]
			if !have {
				continue
			}
			if _, err := ParseSecretRef(raw); err != nil {
				add("provider_settings."+k, "%v", err)
			}
		}
	}
	return ps
}

// liveProblems are the rules mode: live adds for the keys: each one a
// Keychain item the owner created: an env: variable is whatever the shell
// that started the kit says, an agent's included.
func (c Config) liveProblems(reg Registration) []Problem {
	var ps []Problem
	keychainOnly := func(field, raw string) {
		if ref, err := ParseSecretRef(raw); err == nil && ref.Scheme != "keychain" {
			ps = append(ps, Problem{field, fmt.Sprintf("live wants a keychain: reference, got %s: live keys are Keychain items the owner created, separate from the sandbox ones", ref)})
		}
	}
	for _, k := range sortedKeys(c.Secrets) {
		keychainOnly("secrets."+k, c.Secrets[k])
	}
	for _, k := range sortedKeys(c.ProviderSettings) {
		if slices.Contains(reg.Secrets, k) {
			keychainOnly("provider_settings."+k, c.ProviderSettings[k])
		}
	}
	return ps
}

func isLoopback(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func sortedKeys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// SecretState is one secret reference as the owner sees it: set or missing,
// never its value.
type SecretState struct {
	Field string
	Ref   string
	Set   bool
}

// SecretStates lists every reference in c with whether it is set.
func (c Config) SecretStates() []SecretState {
	var out []SecretState
	for _, k := range sortedKeys(c.Secrets) {
		if ref, err := ParseSecretRef(c.Secrets[k]); err == nil {
			_, set := SecretValue(ref)
			out = append(out, SecretState{"secrets." + k, ref.String(), set})
		}
	}
	if reg, ok := lookup(c.Provider); ok {
		for _, k := range sortedKeys(c.ProviderSettings) {
			if !slices.Contains(reg.Secrets, k) {
				continue
			}
			if ref, err := ParseSecretRef(c.ProviderSettings[k]); err == nil {
				_, set := SecretValue(ref)
				out = append(out, SecretState{"provider_settings." + k, ref.String(), set})
			}
		}
	}
	return out
}

// settings resolves the provider's settings: a secret key's reference is
// read, the rest are passed as written.
func (c Config) settings(reg Registration) (Settings, error) {
	vals := map[string]string{}
	for k, v := range c.ProviderSettings {
		if !slices.Contains(reg.Secrets, k) {
			vals[k] = v
			continue
		}
		ref, err := ParseSecretRef(v)
		if err != nil {
			return Settings{}, fmt.Errorf("provider_settings.%s: %v", k, err)
		}
		val, ok := SecretValue(ref)
		if !ok {
			return Settings{}, fmt.Errorf("provider_settings.%s: %s is not set on this machine", k, ref)
		}
		vals[k] = val
	}
	return Settings{
		Mode:       c.Mode,
		Values:     vals,
		PublicURL:  c.PublicURL,
		WebhookURL: c.PublicURL + "/v1/webhooks/" + c.Provider,
	}, nil
}

func (c Config) secret(k string) (string, error) {
	ref, err := ParseSecretRef(c.Secrets[k])
	if err != nil {
		return "", fmt.Errorf("secrets.%s: %v", k, err)
	}
	v, ok := SecretValue(ref)
	if !ok {
		return "", fmt.Errorf("secrets.%s: %s is not set on this machine", k, ref)
	}
	return v, nil
}
