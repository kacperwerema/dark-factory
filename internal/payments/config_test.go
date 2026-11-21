package payments

import (
	"strings"
	"testing"
)

// liveFake is a registered adapter that has a live side, so the live rules
// can be checked before a real provider exists (the stand-in refuses live).
func init() {
	Register(Registration{Name: "livefake-test", Secrets: []string{"secret_key"}, Live: true,
		New: func(Settings) (Provider, error) { return nil, nil }})
}

func liveConfig() Config {
	c := Config{
		Provider: "livefake-test", Mode: "live",
		PublicURL: "https://pay.example", EventsURL: "https://app.example/ev",
		Secrets: map[string]string{
			SecretAPIToken:     "keychain:orchard-payments-api-live",
			SecretReadToken:    "keychain:orchard-payments-read-live",
			SecretEventsSecret: "keychain:orchard-payments-events-live",
		},
		ProviderSettings: map[string]string{"secret_key": "keychain:orchard-provider-live"},
	}
	c.defaults()
	return c
}

func problems(c Config) string {
	var b strings.Builder
	for _, p := range c.Validate() {
		b.WriteString(p.String() + "\n")
	}
	return b.String()
}

// TestValidateLive: in live mode every key is a Keychain item (an env: one
// is a problem, by field), and in any mode the read token is a key of its
// own.
func TestValidateLive(t *testing.T) {
	if got := problems(liveConfig()); got != "" {
		t.Fatalf("live with Keychain keys: want no problem, got\n%s", got)
	}
	c := liveConfig()
	c.ProviderSettings["secret_key"] = "env:PROVIDER_LIVE_KEY"
	c.Secrets[SecretAPIToken] = "env:PAYMENTS_API_TOKEN"
	got := problems(c)
	for _, want := range []string{"provider_settings.secret_key: live wants a keychain: reference, got env:PROVIDER_LIVE_KEY", "secrets.api_token: live wants a keychain: reference"} {
		if !strings.Contains(got, want) {
			t.Errorf("live with env: keys: want %q in\n%s", want, got)
		}
	}
	c.Mode = "sandbox"
	if got := problems(c); got != "" {
		t.Errorf("sandbox may use env: keys; got\n%s", got)
	}
	c = liveConfig()
	c.Secrets[SecretReadToken] = c.Secrets[SecretAPIToken]
	for _, mode := range []string{"live", "sandbox"} {
		c.Mode = mode
		if got := problems(c); !strings.Contains(got, "secrets.read_token: is the same reference as api_token") {
			t.Errorf("%s, read_token = api_token: want a problem, got\n%s", mode, got)
		}
	}
	c = liveConfig()
	c.Secrets[SecretReadToken] = "rt_pasted-value"
	if got := problems(c); !strings.Contains(got, "secrets.read_token: not env:<VAR> or keychain:<service>") || strings.Contains(got, "rt_pasted-value") {
		t.Errorf("a pasted read token: want it refused without repeating it, got\n%s", got)
	}
	delete(c.Secrets, SecretReadToken)
	if got := problems(c); got != "" {
		t.Errorf("read_token is optional; got\n%s", got)
	}
}
