package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/payments"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "payments.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runKit(args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String() + errb.String()
}

const goodConfig = `provider: standin
mode: sandbox
listen: %LISTEN%
events_url: %EVENTS%
invoice: record
secrets:
  api_token: env:PK_TEST_API_TOKEN
  events_secret: env:PK_TEST_EVENTS_SECRET
provider_settings:
  webhook_secret: env:PK_TEST_WEBHOOK_SECRET
`

func config(listen, events string) string {
	return strings.NewReplacer("%LISTEN%", listen, "%EVENTS%", events).Replace(goodConfig)
}

func setSecrets(t *testing.T) {
	t.Setenv("PK_TEST_API_TOKEN", "tok-for-the-test")
	t.Setenv("PK_TEST_EVENTS_SECRET", "events-for-the-test")
	t.Setenv("PK_TEST_WEBHOOK_SECRET", "webhook-for-the-test")
}

func TestRawSecretRefused(t *testing.T) {
	setSecrets(t)
	raw := strings.Replace(config("127.0.0.1:8787", "http://127.0.0.1:3000/ev"), "webhook_secret: env:PK_TEST_WEBHOOK_SECRET", "webhook_secret: whsec_pasted-value-9f8e", 1)
	raw = strings.Replace(raw, "api_token: env:PK_TEST_API_TOKEN", "api_token: pasted-token-1234", 1)
	path := writeConfig(t, raw)
	code, out := runKit("check", "-config", path)
	if code != 1 {
		t.Fatalf("check on raw secrets: exit %d\n%s", code, out)
	}
	for _, want := range []string{"secrets.api_token: not env:<VAR> or keychain:<service>", "provider_settings.webhook_secret: not env:<VAR> or keychain:<service>"} {
		if !strings.Contains(out, want) {
			t.Errorf("check output lacks %q:\n%s", want, out)
		}
	}
	for _, value := range []string{"whsec_pasted-value-9f8e", "pasted-token-1234"} {
		if strings.Contains(out, value) {
			t.Errorf("check printed the raw value %q", value)
		}
	}
	if code, out := runKit("serve", "-config", path); code != 1 || !strings.Contains(out, "payments-kit check -config") {
		t.Errorf("serve on raw secrets: exit %d, want 1 naming check\n%s", code, out)
	}
}

func TestCheckPrintsSetOrMissingNeverTheValue(t *testing.T) {
	setSecrets(t)
	os.Unsetenv("PK_TEST_WEBHOOK_SECRET")
	path := writeConfig(t, config("127.0.0.1:8787", "http://127.0.0.1:3000/ev"))
	code, out := runKit("check", "-config", path)
	if code != 1 || !strings.Contains(out, "provider_settings.webhook_secret: env:PK_TEST_WEBHOOK_SECRET (missing)") || !strings.Contains(out, "secrets.api_token: env:PK_TEST_API_TOKEN (set)") {
		t.Errorf("check with one secret missing: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "tok-for-the-test") {
		t.Errorf("check printed a secret's value:\n%s", out)
	}
	t.Setenv("PK_TEST_WEBHOOK_SECRET", "webhook-for-the-test")
	if code, out := runKit("check", "-config", path); code != 0 || !strings.Contains(out, "ok (provider standin, mode sandbox") {
		t.Errorf("check with every secret set: exit %d\n%s", code, out)
	}
}

func TestConfigErrorsNameTheFix(t *testing.T) {
	setSecrets(t)
	cases := map[string]struct{ from, to, want string }{
		"unknown provider": {"provider: standin", "provider: strpie", `provider: "strpie" is not a registered adapter; registered: standin`},
		"live stand-in":    {"mode: sandbox", "mode: live", "mode: standin has no live side"},
		"typo key":         {"invoice: record", "invoce: record", "field invoce not found"},
		"bad invoice":      {"invoice: record", "invoice: ksef", "invoice: wants none or record"},
		"no events_url":    {"events_url: http://127.0.0.1:3000/ev", "events_url: ''", "events_url: wants the app's http(s) endpoint"},
	}
	for name, c := range cases {
		path := writeConfig(t, strings.Replace(config("127.0.0.1:8787", "http://127.0.0.1:3000/ev"), c.from, c.to, 1))
		code, out := runKit("check", "-config", path)
		if code == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%s: exit %d, want output naming %q\n%s", name, code, c.want, out)
		}
	}
}

func TestOnlyStandinRegistered(t *testing.T) {
	if got := payments.Registered(); !slices.Equal(got, []string{"standin"}) {
		t.Errorf("registered adapters = %v: no real provider is built in yet (lead rule: stand-in only)", got)
	}
	if code, out := runKit("providers"); code != 0 || out != "standin\n" {
		t.Errorf("providers: %d %q", code, out)
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"-h"}, {"help"}} {
		if code, out := runKit(args...); code != 0 || !strings.Contains(out, "payments-kit serve") || !strings.Contains(out, "docs/payments.md") {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
	}
	if code, out := runKit("refnud"); code != 2 || !strings.Contains(out, `unknown command "refnud"`) {
		t.Errorf("unknown command: exit %d\n%s", code, out)
	}
}

// TestConfigPicksProvider: a project's payments.yaml alone (provider:
// standin, secrets as references) gives a working kit: a checkout paid on
// the stand-in's hosted page reaches the app once, signed by the kit.
func TestConfigPicksProvider(t *testing.T) {
	setSecrets(t)
	var mu sync.Mutex
	var got []payments.Event
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := payments.Verify([]byte("events-for-the-test"), r.Header.Get(payments.SignatureHeader), body, time.Now()); err != nil {
			http.Error(w, err.Error(), 401)
			return
		}
		var ev payments.Event
		json.Unmarshal(body, &ev)
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}))
	defer app.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, config(ln.Addr().String(), app.URL+"/api/payments/events"))
	cfg, err := payments.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := runKit("check", "-config", path); code != 0 {
		t.Fatalf("check: %d\n%s", code, out)
	}
	svc, err := payments.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: svc.Handler()}
	go srv.Serve(ln)
	defer srv.Close()
	base := "http://" + ln.Addr().String()

	body, _ := json.Marshal(payments.CheckoutRequest{
		Mode: payments.ModeOneOff, Reference: "order-9",
		Items:      []payments.Item{{Name: "Plan", Quantity: 1, UnitPrice: payments.Money{Amount: 2500, Currency: "PLN"}}},
		SuccessURL: "https://app.example/ok", CancelURL: "https://app.example/no",
	})
	req, _ := http.NewRequest("POST", base+"/v1/checkouts", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok-for-the-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var co payments.Checkout
	json.NewDecoder(resp.Body).Decode(&co)
	resp.Body.Close()
	if resp.StatusCode != 201 || !strings.HasPrefix(co.URL, base+"/standin/checkout/") {
		t.Fatalf("checkout: %d %+v", resp.StatusCode, co)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	pay, err := noFollow.Post(base+"/standin/checkouts/"+co.ID+"/pay", "application/x-www-form-urlencoded", strings.NewReader("method=blik"))
	if err != nil {
		t.Fatal(err)
	}
	pay.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Type != payments.EventPaymentPaid || got[0].Reference != "order-9" || got[0].Invoice == nil {
		t.Fatalf("the app got %+v, want one payment.paid for order-9 with an invoice", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".payments", "handled.jsonl")); err != nil {
		t.Errorf("state_dir beside the config: %v", err)
	}
}
