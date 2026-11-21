package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/payments"
)

const readConfig = `provider: standin
mode: sandbox
listen: 127.0.0.1:8787
public_url: %PUBLIC%
events_url: %EVENTS%
state_dir: %STATE%
secrets:
  api_token: env:PK_TEST_API_TOKEN
  read_token: env:PK_TEST_READ_TOKEN
  events_secret: env:PK_TEST_EVENTS_SECRET
provider_settings:
  webhook_secret: env:PK_TEST_WEBHOOK_SECRET
`

// paidKit starts a kit with a read token on the stand-in, pays one 4999 PLN
// checkout and returns the kit's URL and the payment's id.
func paidKit(t *testing.T) (string, string) {
	t.Helper()
	setSecrets(t)
	t.Setenv("PK_TEST_READ_TOKEN", "read-for-the-test")
	app := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(app.Close)
	var h http.Handler
	kit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	t.Cleanup(kit.Close)
	path := writeConfig(t, strings.NewReplacer("%PUBLIC%", kit.URL, "%EVENTS%", app.URL+"/ev", "%STATE%", t.TempDir()).Replace(readConfig))
	cfg, err := payments.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := payments.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h = svc.Handler()
	body, _ := json.Marshal(payments.CheckoutRequest{
		Mode: payments.ModeOneOff, Reference: "order-7",
		Items:      []payments.Item{{Name: "Plan", Quantity: 1, UnitPrice: payments.Money{Amount: 4999, Currency: "PLN"}}},
		SuccessURL: "https://app.example/ok", CancelURL: "https://app.example/no",
	})
	req, _ := http.NewRequest("POST", kit.URL+"/v1/checkouts", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok-for-the-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var co payments.Checkout
	json.NewDecoder(resp.Body).Decode(&co)
	resp.Body.Close()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	pay, err := noFollow.Post(kit.URL+"/standin/checkouts/"+co.ID+"/pay", "application/x-www-form-urlencoded", strings.NewReader("method=blik"))
	if err != nil {
		t.Fatal(err)
	}
	pay.Body.Close()
	t.Setenv(envURL, kit.URL)
	return kit.URL, co.PaymentID
}

func runSplit(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// TestClientReadTokenReadsOnly: the read token (the sales/payments
// connector's) looks a payment and the provider up, and a refund with it is
// refused with the connector to use; the api token (sales/payments-refund's)
// refunds, once per idempotency key.
func TestClientReadTokenReadsOnly(t *testing.T) {
	_, pid := paidKit(t)

	t.Setenv(envToken, "read-for-the-test")
	code, out, errs := runSplit("payment", pid)
	var p payments.Payment
	if code != 0 || json.Unmarshal([]byte(out), &p) != nil || p.State != payments.PaymentPaid || p.Amount.Amount != 4999 {
		t.Fatalf("payment with the read token: exit %d\n%s%s", code, out, errs)
	}
	if code, out, errs := runSplit("provider"); code != 0 || !strings.Contains(out, `"name":"standin"`) {
		t.Errorf("provider with the read token: exit %d\n%s%s", code, out, errs)
	}
	code, out, errs = runSplit("refund", pid, "--idempotency-key", "sales/refund-1")
	if code != 1 || !strings.Contains(errs, "403") || !strings.Contains(errs, payments.ErrReadToken) || !strings.Contains(errs, "sales/payments-refund") {
		t.Errorf("refund with the read token: exit %d, want 403 naming sales/payments-refund\n%s%s", code, out, errs)
	}

	t.Setenv(envToken, "tok-for-the-test")
	for i := range 2 {
		code, out, errs = runSplit("refund", "--amount", "1000", "--reason", "late", pid, "--idempotency-key", "sales/refund-1")
		if code != 0 || !strings.Contains(out, `"amount":1000`) {
			t.Fatalf("refund %d with the api token: exit %d\n%s%s", i+1, code, out, errs)
		}
	}
	_, out, _ = runSplit("payment", pid)
	json.Unmarshal([]byte(out), &p)
	if p.State != payments.PaymentPartiallyRefunded || p.Refunded != 1000 {
		t.Errorf("after the same refund twice: %s, refunded %d; want partially_refunded, 1000 (one refund per key)", p.State, p.Refunded)
	}
}

func TestClientNeedsTokenAndKey(t *testing.T) {
	t.Setenv(envToken, "")
	if code, _, errs := runSplit("payment", "pay_1"); code != 2 || !strings.Contains(errs, "connector exec sales/payments") {
		t.Errorf("no token: exit %d, want 2 naming the connector\n%s", code, errs)
	}
	t.Setenv(envToken, "x")
	if code, _, errs := runSplit("refund", "pay_1"); code != 2 || !strings.Contains(errs, "--idempotency-key is required") {
		t.Errorf("refund without a key: exit %d\n%s", code, errs)
	}
	if code, _, errs := runSplit("payment"); code != 2 || !strings.Contains(errs, "wants <payment-id>") {
		t.Errorf("payment without an id: exit %d\n%s", code, errs)
	}
}

// TestCheckPrintsMode: check's first line is the mode, so the connector's
// check (and its log) says whether money moves.
func TestCheckPrintsMode(t *testing.T) {
	setSecrets(t)
	path := writeConfig(t, config("127.0.0.1:8787", "http://127.0.0.1:3000/ev"))
	if _, out := runKit("check", "-config", path); !strings.HasPrefix(out, "mode: sandbox (no money moves)\n") {
		t.Errorf("check, sandbox: first line\n%s", out)
	}
	path = writeConfig(t, strings.Replace(config("127.0.0.1:8787", "http://127.0.0.1:3000/ev"), "mode: sandbox", "mode: live", 1))
	if _, out := runKit("check", "-config", path); !strings.HasPrefix(out, "mode: live (real money)\n") {
		t.Errorf("check, live: first line\n%s", out)
	}
}
