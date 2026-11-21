package standin_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/payments"
	"github.com/kacperwerema/dark-factory/internal/payments/paymentstest"
	"github.com/kacperwerema/dark-factory/internal/payments/standin"
)

// app is the project's app as the kit sees it: it checks the kit's
// signature on every event and keeps what it got.
type app struct {
	secret []byte
	mu     sync.Mutex
	events []payments.Event
	fail   int // answer 500 this many times
	bad    int // deliveries whose signature didn't verify
}

func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := payments.Verify(a.secret, r.Header.Get(payments.SignatureHeader), body, time.Now()); err != nil {
		a.bad++
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if a.fail > 0 {
		a.fail--
		http.Error(w, "down", http.StatusInternalServerError)
		return
	}
	var ev payments.Event
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.events = append(a.events, ev)
}

func (a *app) got() []payments.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]payments.Event(nil), a.events...)
}

func (a *app) count(typ payments.EventType) int {
	n := 0
	for _, e := range a.got() {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// swap lets the kit's server start before its handler exists, so the
// stand-in can be told the kit's URL.
type swap struct{ h http.Handler }

func (s *swap) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.h.ServeHTTP(w, r) }

type kit struct {
	t      *testing.T
	app    *app
	p      *standin.Provider
	svc    *payments.Service
	sw     *swap
	url    string
	state  string
	appURL string
}

const token = "test-api-token"

func newKit(t *testing.T) *kit {
	t.Helper()
	a := &app{secret: []byte("test-events-secret")}
	appSrv := httptest.NewServer(a)
	t.Cleanup(appSrv.Close)
	sw := &swap{}
	kitSrv := httptest.NewServer(sw)
	t.Cleanup(kitSrv.Close)
	p := standin.New("test-webhook-secret", kitSrv.URL+"/v1/webhooks/standin")
	p.PublicURL = kitSrv.URL
	k := &kit{t: t, app: a, p: p, sw: sw, url: kitSrv.URL, state: t.TempDir(), appURL: appSrv.URL}
	k.restart()
	return k
}

// restart builds a fresh Service on the same state dir, as a new process
// would.
func (k *kit) restart() {
	k.t.Helper()
	store, err := payments.OpenFileStore(filepath.Join(k.state, "handled.jsonl"))
	if err != nil {
		k.t.Fatal(err)
	}
	k.svc = &payments.Service{
		Provider: k.p, Store: store, EventsURL: k.appURL,
		Invoicer: payments.RecordInvoice{Dir: filepath.Join(k.state, "invoices")},
		APIToken: []byte(token), EventsSecret: k.app.secret,
	}
	k.sw.h = k.svc.Handler()
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (k *kit) api(method, path string, body any, out any) int {
	k.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, k.url+path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		k.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (k *kit) checkout(req payments.CheckoutRequest) payments.Checkout {
	k.t.Helper()
	var co payments.Checkout
	if code := k.api("POST", "/v1/checkouts", req, &co); code != http.StatusCreated {
		k.t.Fatalf("POST /v1/checkouts = %d", code)
	}
	return co
}

// buyer posts the hosted page's form (pay or decline) and returns where it
// was sent back to.
func (k *kit) buyer(co payments.Checkout, action string, m payments.Method) string {
	k.t.Helper()
	page, err := http.Get(co.URL)
	if err != nil {
		k.t.Fatal(err)
	}
	html, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != 200 || !strings.Contains(string(html), "/standin/checkouts/"+co.ID+"/"+action) {
		k.t.Fatalf("hosted page %s: %d, no %s form", co.URL, page.StatusCode, action)
	}
	resp, err := noRedirect.PostForm(k.url+"/standin/checkouts/"+co.ID+"/"+action, url.Values{"method": {string(m)}})
	if err != nil {
		k.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		k.t.Fatalf("%s: %d, want 303 back to the app", action, resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func order(ref string, amount int64) payments.CheckoutRequest {
	return payments.CheckoutRequest{
		Mode:       payments.ModeOneOff,
		Items:      []payments.Item{{Name: "Meal plan", Quantity: 1, UnitPrice: payments.Money{Amount: amount, Currency: "PLN"}}},
		Customer:   payments.Customer{Email: "buyer@example.com", Country: "PL"},
		Reference:  ref,
		SuccessURL: "https://app.example/paid",
		CancelURL:  "https://app.example/cancelled",
	}
}

func lastEvent(t *testing.T, a *app, typ payments.EventType) payments.Event {
	t.Helper()
	evs := a.got()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == typ {
			return evs[i]
		}
	}
	t.Fatalf("the app got no %s; it got %d event(s)", typ, len(evs))
	return payments.Event{}
}

func TestKitFlowEndToEnd(t *testing.T) {
	k := newKit(t)

	if code := (&kit{t: t, url: k.url}).apiNoToken("POST", "/v1/checkouts"); code != http.StatusUnauthorized {
		t.Errorf("a checkout without the api token: %d, want 401", code)
	}

	// One-off, paid with BLIK, invoiced, refunded in two parts.
	req := order("order-1", 4999)
	req.Methods = []payments.Method{payments.MethodBLIK, payments.MethodCard}
	co := k.checkout(req)
	if back := k.buyer(co, "pay", payments.MethodBLIK); back != req.SuccessURL {
		t.Errorf("after paying the buyer goes to %q, want %q", back, req.SuccessURL)
	}
	paid := lastEvent(t, k.app, payments.EventPaymentPaid)
	if paid.Reference != "order-1" || paid.Payment.Method != payments.MethodBLIK || paid.Payment.Amount != (payments.Money{Amount: 4999, Currency: "PLN"}) {
		t.Errorf("payment.paid = %+v / %+v", paid, paid.Payment)
	}
	if paid.Invoice == nil {
		t.Fatal("payment.paid carries no invoice: the invoice hook didn't run")
	}
	if _, err := os.Stat(paid.Invoice.Ref); err != nil {
		t.Errorf("recorded invoice: %v", err)
	}
	var got payments.Payment
	if code := k.api("GET", "/v1/payments/"+co.PaymentID, nil, &got); code != 200 || got.State != payments.PaymentPaid {
		t.Errorf("GET payment: %d, %s", code, got.State)
	}
	if code := k.api("POST", "/v1/payments/"+co.PaymentID+"/refunds", payments.RefundRequest{Amount: 1000, Reason: "late delivery"}, nil); code != http.StatusCreated {
		t.Fatalf("partial refund: %d", code)
	}
	if e := lastEvent(t, k.app, payments.EventPaymentRefunded); e.Refund.Amount.Amount != 1000 || e.Payment.State != payments.PaymentPartiallyRefunded {
		t.Errorf("payment.refunded = %+v, payment %s", e.Refund, e.Payment.State)
	}
	if code := k.api("POST", "/v1/payments/"+co.PaymentID+"/refunds", payments.RefundRequest{Amount: 9999}, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("over-refund: %d, want 422", code)
	}
	k.api("POST", "/v1/payments/"+co.PaymentID+"/refunds", payments.RefundRequest{}, nil)
	if k.api("GET", "/v1/payments/"+co.PaymentID, nil, &got); got.State != payments.PaymentRefunded {
		t.Errorf("after refunding the rest: %s", got.State)
	}

	// Subscription: paid, renewed (invoiced again), cancelled from the app.
	sreq := order("sub-1", 2900)
	sreq.Mode, sreq.Interval = payments.ModeSubscription, payments.IntervalMonth
	sco := k.checkout(sreq)
	k.buyer(sco, "pay", payments.MethodCard)
	if e := lastEvent(t, k.app, payments.EventPaymentPaid); e.Subscription == nil || e.Subscription.State != payments.SubscriptionActive {
		t.Errorf("first payment of a subscription: %+v", e.Subscription)
	}
	resp, err := http.Post(k.url+"/standin/subscriptions/"+sco.SubscriptionID+"/renew", "", nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("renew: %v %v", resp, err)
	}
	resp.Body.Close()
	if e := lastEvent(t, k.app, payments.EventSubscriptionRenewed); e.Payment == nil || e.Invoice == nil {
		t.Errorf("subscription.renewed wants the new payment and its invoice: %+v", e)
	}
	var sub payments.Subscription
	if code := k.api("POST", "/v1/subscriptions/"+sco.SubscriptionID+"/cancel", nil, &sub); code != 200 || sub.State != payments.SubscriptionCancelled {
		t.Errorf("cancel: %d %s", code, sub.State)
	}
	if e := lastEvent(t, k.app, payments.EventSubscriptionCancelled); e.Reference != "sub-1" {
		t.Errorf("subscription.cancelled ref = %q", e.Reference)
	}

	// Failure modes: the Decline button, and decline_amount.
	fco := k.checkout(order("order-2", 1500))
	if back := k.buyer(fco, "decline", payments.MethodCard); back != "https://app.example/cancelled" {
		t.Errorf("after a decline the buyer goes to %q", back)
	}
	if e := lastEvent(t, k.app, payments.EventPaymentFailed); e.Payment.ID != fco.PaymentID || e.Invoice != nil {
		t.Errorf("payment.failed = %+v (an invoice on a failed payment?)", e)
	}
	k.p.DeclineAmount = 666
	dco := k.checkout(order("order-3", 666))
	k.buyer(dco, "pay", payments.MethodCard)
	if e := lastEvent(t, k.app, payments.EventPaymentFailed); e.Payment.ID != dco.PaymentID {
		t.Errorf("decline_amount: last payment.failed is %s, want %s", e.Payment.ID, dco.PaymentID)
	}

	// A method the provider doesn't name is refused, not dropped.
	bad := order("order-4", 100)
	bad.Methods = []payments.Method{"cheque"}
	if code := k.api("POST", "/v1/checkouts", bad, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("method cheque: %d, want 422", code)
	}
	if k.app.bad != 0 {
		t.Errorf("%d delivery(ies) to the app failed the kit's signature", k.app.bad)
	}
}

func (k *kit) apiNoToken(method, path string) int {
	req, _ := http.NewRequest(method, k.url+path, strings.NewReader("{}"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		k.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func paidOnce(t *testing.T) (*kit, payments.Event) {
	k := newKit(t)
	k.buyer(k.checkout(order("order-1", 4999)), "pay", payments.MethodCard)
	return k, lastEvent(t, k.app, payments.EventPaymentPaid)
}

func TestWebhookOnce(t *testing.T) {
	t.Run("redelivered", func(t *testing.T) {
		k, ev := paidOnce(t)
		status, err := k.p.Redeliver(ev.ID)
		if err != nil || status != 200 {
			t.Fatalf("redelivery: %d %v, want 200 so the provider stops", status, err)
		}
		if n := k.app.count(payments.EventPaymentPaid); n != 1 {
			t.Errorf("the app got payment.paid %d times", n)
		}
	})
	t.Run("ten at once", func(t *testing.T) {
		k, ev := paidOnce(t)
		var wg sync.WaitGroup
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				k.p.Redeliver(ev.ID)
			}()
		}
		wg.Wait()
		if n := k.app.count(payments.EventPaymentPaid); n != 1 {
			t.Errorf("the app got payment.paid %d times from 11 deliveries", n)
		}
	})
	t.Run("after a restart", func(t *testing.T) {
		k, ev := paidOnce(t)
		k.restart()
		k.p.Redeliver(ev.ID)
		if n := k.app.count(payments.EventPaymentPaid); n != 1 {
			t.Errorf("after a restart the app got payment.paid %d times", n)
		}
	})
	t.Run("app down, then up", func(t *testing.T) {
		k := newKit(t)
		k.app.fail = 1
		k.buyer(k.checkout(order("order-1", 4999)), "pay", payments.MethodCard)
		if n := len(k.app.got()); n != 0 {
			t.Fatalf("the app answered 500 but holds %d event(s)", n)
		}
		ds := k.p.Deliveries()
		if ds[len(ds)-1].Delivered || ds[len(ds)-1].Status != http.StatusBadGateway {
			t.Fatalf("the kit acknowledged an event the app refused: %+v", ds[len(ds)-1])
		}
		if status, _ := k.p.Redeliver(ds[len(ds)-1].Event.ID); status != 200 {
			t.Fatalf("retry: %d", status)
		}
		if n := k.app.count(payments.EventPaymentPaid); n != 1 {
			t.Errorf("after the retry the app got payment.paid %d times", n)
		}
	})
}

func TestWebhookRefused(t *testing.T) {
	k, ev := paidOnce(t)
	post := func(path, sig string, body []byte) int {
		req, _ := http.NewRequest("POST", k.url+path, bytes.NewReader(body))
		if sig != "" {
			req.Header.Set(standin.SignatureHeader, sig)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	fresh := ev
	fresh.ID = "evt_forged"
	body, _ := json.Marshal(fresh)
	wrong := payments.Sign([]byte("not-the-secret"), time.Now(), body)
	stale := standin.New("test-webhook-secret", "")
	stale.Now = func() time.Time { return time.Now().Add(-10 * time.Minute) }
	staleBody, staleSig := stale.Signed(fresh)
	_, goodSig := k.p.Signed(fresh)
	cases := []struct {
		name, path, sig string
		body            []byte
		want            int
	}{
		{"wrong secret", "/v1/webhooks/standin", wrong, body, 400},
		{"no signature", "/v1/webhooks/standin", "", body, 400},
		{"stale timestamp", "/v1/webhooks/standin", staleSig, staleBody, 400},
		{"tampered body", "/v1/webhooks/standin", goodSig, bytes.Replace(body, []byte("4999"), []byte("1"), 1), 400},
		{"another provider", "/v1/webhooks/stripe", goodSig, body, 404},
	}
	for _, c := range cases {
		if got := post(c.path, c.sig, c.body); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	if n := len(k.app.got()); n != 1 {
		t.Errorf("the app got %d events; only the real payment.paid should reach it", n)
	}
}

// stDriver works the stand-in for the contract, as a buyer and its clock.
type stDriver struct{ p *standin.Provider }

func (d stDriver) Pay(t testing.TB, co payments.Checkout) {
	if _, err := d.p.Pay(co.ID, payments.MethodCard); err != nil {
		t.Fatal(err)
	}
}

func (d stDriver) Decline(t testing.TB, co payments.Checkout) {
	if _, err := d.p.Decline(co.ID, payments.MethodCard); err != nil {
		t.Fatal(err)
	}
}

func (d stDriver) Renew(t testing.TB, id string) {
	if _, err := d.p.Renew(id); err != nil {
		t.Fatal(err)
	}
}

func TestStandinContract(t *testing.T) {
	paymentstest.Contract(t, func(_ testing.TB, webhookURL string) (payments.Provider, paymentstest.Driver) {
		p := standin.New("contract-secret", webhookURL)
		return p, stDriver{p}
	})
}

func TestStandinRefusesLive(t *testing.T) {
	c := payments.Config{Provider: "standin", Mode: "live", Listen: "127.0.0.1:1", PublicURL: "https://pay.example", EventsURL: "https://app.example/ev",
		Invoice: "none", Secrets: map[string]string{"api_token": "env:A", "events_secret": "env:B"}, ProviderSettings: map[string]string{"webhook_secret": "env:C"}}
	ps := c.Validate()
	if len(ps) != 1 || ps[0].Field != "mode" {
		t.Errorf("mode: live on the stand-in: %v, want one mode problem", ps)
	}
}

// TestPaymentsDocCoversTheKit: docs/payments.md says where the kit lives and
// why, what stays per project, how to enable it, every neutral event and
// method, how to test with the stand-in, the adapter checklist, and lists
// every registered provider.
func TestPaymentsDocCoversTheKit(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "payments.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"## Where it lives and why", "A service, not a library per stack", "**Per project, never in the kit:**",
		"products, prices", "terms (regulamin)", "## Enable it in a project", "### Config", "## The API",
		"## The events", "Payments-Kit-Signature", "## The invoice hook", "## Testing with the stand-in",
		"## Adding a provider", "paymentstest.Contract", "keychain:",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/payments.md lacks %q", want)
		}
	}
	for _, e := range payments.EventTypes {
		if !strings.Contains(doc, "`"+string(e)+"`") {
			t.Errorf("docs/payments.md doesn't name the event %s", e)
		}
	}
	for _, m := range payments.Methods {
		if !strings.Contains(doc, "`"+string(m)+"`") {
			t.Errorf("docs/payments.md doesn't name the method %s", m)
		}
	}
	for _, n := range payments.Registered() {
		if !strings.Contains(doc, "| `"+n+"` |") {
			t.Errorf("docs/payments.md's provider table has no row for %s", n)
		}
	}
}
