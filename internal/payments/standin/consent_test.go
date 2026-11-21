package standin_test

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/payments"
)

// consentKit is newKit with the consent flow on (the built-in placeholder
// pack, waiver required) and a clock the test moves.
func consentKit(t *testing.T) (*kit, *time.Time) {
	t.Helper()
	k := newKit(t)
	now := time.Now()
	f, err := payments.NewConsentFlow(payments.ConsentConfig{Required: true}, "", k.state, k.url, []byte("test-link-key"))
	if err != nil {
		t.Fatal(err)
	}
	k.svc.Consent = f
	k.svc.Now = func() time.Time { return now }
	k.sw.h = k.svc.Handler()
	return k, &now
}

func withConsent(r payments.CheckoutRequest, waiver bool) payments.CheckoutRequest {
	r.Consent = &payments.Consent{Waiver: waiver, TextVersion: payments.BuiltinTexts().Version, Lang: "pl"}
	return r
}

func (k *kit) apiErr(req payments.CheckoutRequest) (int, string) {
	k.t.Helper()
	var out map[string]string
	code := k.api("POST", "/v1/checkouts", req, &out)
	return code, out["error"]
}

func get(t *testing.T, u string) (int, string) {
	t.Helper()
	resp, err := noRedirect.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func post(t *testing.T, u string, form url.Values) (int, string) {
	t.Helper()
	resp, err := noRedirect.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

var hrefRe = regexp.MustCompile(`href="([^"]+)" data-step="1"`)

// TestConsentCheckoutRecord: with consent required, a checkout without a
// consent record or with a stale text version is refused naming the texts
// route; an accepted one keeps the record and answers the signed links.
func TestConsentCheckoutRecord(t *testing.T) {
	k, _ := consentKit(t)
	code, msg := k.apiErr(order("o-1", 2900))
	if code != http.StatusUnprocessableEntity || !strings.Contains(msg, "GET /v1/consent/texts") {
		t.Errorf("no consent: %d %q, want 422 naming GET /v1/consent/texts", code, msg)
	}
	stale := withConsent(order("o-1", 2900), true)
	stale.Consent.TextVersion = "v-old"
	if code, msg := k.apiErr(stale); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "GET /v1/consent/texts") {
		t.Errorf("stale text_version: %d %q", code, msg)
	}

	var texts map[string]any
	if code := k.api("GET", "/v1/consent/texts?lang=en", nil, &texts); code != 200 || texts["version"] != payments.BuiltinTexts().Version || texts["placeholder"] != true || texts["withdraw_button"] != "Withdraw from contract here" {
		t.Errorf("GET /v1/consent/texts = %d %v", code, texts)
	}

	co := k.checkout(withConsent(order("o-1", 2900), true))
	if !strings.HasPrefix(co.WithdrawURL, k.url+"/withdraw/"+co.PaymentID+"?t=") || co.CancelSubscriptionURL != "" {
		t.Errorf("one-off links: withdraw %q cancel %q", co.WithdrawURL, co.CancelSubscriptionURL)
	}
	var rec payments.ConsentRecord
	data, err := os.ReadFile(filepath.Join(k.state, "consents", "pay-"+co.PaymentID+".json"))
	if err != nil || json.Unmarshal(data, &rec) != nil {
		t.Fatalf("consent record: %v", err)
	}
	if !rec.Waiver || rec.Reference != "o-1" || !strings.Contains(rec.Text, "tracę prawo odstąpienia") || !rec.Placeholder {
		t.Errorf("record = %+v", rec)
	}

	sreq := withConsent(order("s-1", 2900), false)
	sreq.Mode, sreq.Interval = payments.ModeSubscription, payments.IntervalMonth
	sco := k.checkout(sreq)
	if !strings.HasPrefix(sco.CancelSubscriptionURL, k.url+"/cancel/"+sco.SubscriptionID+"?t=") {
		t.Errorf("subscription cancel link: %q", sco.CancelSubscriptionURL)
	}

	// Without a consent: block the kit refuses a consent record it can't keep.
	k.svc.Consent = nil
	k.sw.h = k.svc.Handler()
	if code, msg := k.apiErr(withConsent(order("o-2", 100), true)); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "no consent: block") {
		t.Errorf("consent while the flow is off: %d %q", code, msg)
	}
}

// TestConsentConfirmationMessage: payment.paid carries the record and an
// order confirmation whose text depends on the waiver, with the link; a
// copy is kept under messages/.
func TestConsentConfirmationMessage(t *testing.T) {
	k, _ := consentKit(t)
	texts := payments.BuiltinTexts().Langs["pl"]
	for _, waiver := range []bool{true, false} {
		ref := map[bool]string{true: "w-yes", false: "w-no"}[waiver]
		co := k.checkout(withConsent(order(ref, 4999), waiver))
		k.buyer(co, "pay", payments.MethodBLIK)
		e := lastEvent(t, k.app, payments.EventPaymentPaid)
		if e.Consent == nil || e.Consent.Waiver != waiver || e.Consent.PaidAt == nil || e.Consent.Deadline == nil {
			t.Fatalf("waiver %v: consent = %+v", waiver, e.Consent)
		}
		m := e.Confirmation
		if m == nil || m.Kind != "order_confirmation" || m.To != "buyer@example.com" || !m.Placeholder {
			t.Fatalf("waiver %v: confirmation = %+v", waiver, m)
		}
		if !strings.Contains(m.Text, co.WithdrawURL) || !strings.Contains(m.Text, "49.99 PLN") || !strings.Contains(m.Subject, ref) {
			t.Errorf("waiver %v: confirmation text lacks the link, amount or reference:\n%s", waiver, m.Text)
		}
		yes, no := strings.Contains(m.Text, texts["confirmation_waived"]), strings.Contains(m.Text, "Przysługuje Panu/Pani prawo odstąpienia")
		if yes != waiver || no == waiver {
			t.Errorf("waiver %v: waived part %v, kept part %v:\n%s", waiver, yes, no, m.Text)
		}
		if _, err := os.Stat(filepath.Join(k.state, "messages", "standin-"+e.ID+".json")); err != nil {
			t.Errorf("waiver %v: message copy: %v", waiver, err)
		}
	}
}

// TestWithdrawalTwoStep: the first step only shows the button, the form's
// button is the second step, and only its POST records the statement and
// sends withdrawal.requested, once. No refund is made. A forged link is
// 404; after the deadline the POST is refused.
func TestWithdrawalTwoStep(t *testing.T) {
	k, now := consentKit(t)
	co := k.checkout(withConsent(order("w-1", 2900), false))

	// Not paid yet: nothing to withdraw from.
	if code, _ := get(t, co.WithdrawURL); code != http.StatusConflict {
		t.Errorf("withdraw page before payment: %d, want 409", code)
	}
	k.buyer(co, "pay", payments.MethodCard)

	if code, _ := get(t, k.url+"/withdraw/"+co.PaymentID+"?t=0000"); code != http.StatusNotFound {
		t.Errorf("forged link: %d, want 404", code)
	}
	if code, _ := post(t, k.url+"/withdraw/"+co.PaymentID, url.Values{"t": {"0000"}, "name": {"A"}, "email": {"a@example.com"}}); code != http.StatusNotFound {
		t.Errorf("forged POST: %d, want 404", code)
	}

	code, step1 := get(t, co.WithdrawURL)
	if code != 200 || !strings.Contains(step1, "Odstąp od umowy tutaj") || strings.Contains(step1, "<form") || !strings.Contains(step1, "data-placeholder") {
		t.Fatalf("step 1: %d, wants the first button only and the placeholder banner:\n%s", code, step1)
	}
	m := hrefRe.FindStringSubmatch(step1)
	if m == nil {
		t.Fatalf("step 1 has no link to step 2:\n%s", step1)
	}
	code, step2 := get(t, k.url+strings.ReplaceAll(m[1], "&amp;", "&"))
	if code != 200 || !strings.Contains(step2, "Potwierdź odstąpienie od umowy") || !strings.Contains(step2, `method="post"`) {
		t.Fatalf("step 2: %d, wants the confirm form:\n%s", code, step2)
	}
	if n := k.app.count(payments.EventWithdrawalRequested); n != 0 {
		t.Fatalf("the two GET steps sent %d withdrawal event(s): only the POST may", n)
	}
	if _, err := os.Stat(filepath.Join(k.state, "withdrawals")); err == nil {
		t.Fatal("the GET steps recorded a withdrawal")
	}

	sig := regexp.MustCompile(`name="t" value="([^"]+)"`).FindStringSubmatch(step2)[1]
	form := url.Values{"t": {sig}, "name": {"Anna Kowalska"}, "email": {"anna@example.com"}, "contract": {"w-1"}}
	if code, body := post(t, k.url+"/withdraw/"+co.PaymentID, form); code != 200 || !strings.Contains(body, "Otrzymaliśmy") {
		t.Fatalf("confirm: %d\n%s", code, body)
	}
	e := lastEvent(t, k.app, payments.EventWithdrawalRequested)
	if e.Withdrawal == nil || e.Withdrawal.Name != "Anna Kowalska" || e.Withdrawal.Waiver || e.Reference != "w-1" || e.Payment == nil {
		t.Errorf("withdrawal.requested = %+v / %+v", e, e.Withdrawal)
	}
	if c := e.Confirmation; c == nil || c.Kind != "withdrawal_receipt" || c.To != "anna@example.com" || !strings.Contains(c.Text, "w-1") {
		t.Errorf("receipt = %+v", c)
	}
	first := e.Withdrawal.RequestedAt

	// A second press shows the first receipt and sends nothing again.
	*now = now.Add(time.Hour)
	if code, body := post(t, k.url+"/withdraw/"+co.PaymentID, form); code != 200 || !strings.Contains(body, first.Format("2006-01-02 15:04")) {
		t.Errorf("second press: %d\n%s", code, body)
	}
	if n := k.app.count(payments.EventWithdrawalRequested); n != 1 {
		t.Errorf("withdrawal.requested sent %d times, want once", n)
	}
	var pay payments.Payment
	if k.api("GET", "/v1/payments/"+co.PaymentID, nil, &pay); pay.State != payments.PaymentPaid || pay.Refunded != 0 {
		t.Errorf("the kit refunded by itself: %s, %d refunded", pay.State, pay.Refunded)
	}

	// After the deadline: the page says so and the POST is refused.
	late := k.checkout(withConsent(order("w-2", 2900), false))
	k.buyer(late, "pay", payments.MethodCard)
	*now = now.Add(15 * 24 * time.Hour)
	if code, body := get(t, late.WithdrawURL); code != http.StatusGone || !strings.Contains(body, "minął") {
		t.Errorf("late page: %d\n%s", code, body)
	}
	lsig := strings.SplitN(late.WithdrawURL, "?t=", 2)[1]
	if code, _ := post(t, k.url+"/withdraw/"+late.PaymentID, url.Values{"t": {lsig}, "name": {"A"}, "email": {"a@example.com"}}); code != http.StatusGone {
		t.Errorf("late POST: %d, want 410", code)
	}
}

// TestWithdrawalAppDown: the statement's time is kept when the app is down,
// and the next press delivers it once with that time.
func TestWithdrawalAppDown(t *testing.T) {
	k, now := consentKit(t)
	k.svc.Log = log.New(testWriter{t}, "", 0)
	co := k.checkout(withConsent(order("d-1", 2900), true))
	k.buyer(co, "pay", payments.MethodCard)
	sig := strings.SplitN(co.WithdrawURL, "?t=", 2)[1]
	form := url.Values{"t": {sig}, "name": {"B"}, "email": {"b@example.com"}}
	k.app.mu.Lock()
	k.app.fail = 1
	k.app.mu.Unlock()
	if code, _ := post(t, k.url+"/withdraw/"+co.PaymentID, form); code != http.StatusServiceUnavailable {
		t.Errorf("app down: %d, want 503", code)
	}
	first := *now
	*now = now.Add(2 * time.Minute) // within the app's 5-minute signature window
	if code, _ := post(t, k.url+"/withdraw/"+co.PaymentID, form); code != 200 {
		t.Errorf("second press: %d", code)
	}
	e := lastEvent(t, k.app, payments.EventWithdrawalRequested)
	if !e.Withdrawal.RequestedAt.Equal(first.UTC()) || !e.Withdrawal.Waiver {
		t.Errorf("withdrawal = %+v, want requested_at %s and waiver true", e.Withdrawal, first.UTC())
	}
	if n := k.app.count(payments.EventWithdrawalRequested); n != 1 {
		t.Errorf("sent %d times", n)
	}
}

// TestCancelTwoStep: the cancel page has the same two steps, cancels at the
// provider, and subscription.cancelled carries the cancellation receipt.
func TestCancelTwoStep(t *testing.T) {
	k, _ := consentKit(t)
	sreq := withConsent(order("s-1", 2900), false)
	sreq.Mode, sreq.Interval = payments.ModeSubscription, payments.IntervalMonth
	sco := k.checkout(sreq)
	k.buyer(sco, "pay", payments.MethodCard)
	if e := lastEvent(t, k.app, payments.EventPaymentPaid); e.Confirmation == nil || !strings.Contains(e.Confirmation.Text, sco.CancelSubscriptionURL) {
		t.Errorf("a subscription's confirmation lacks the cancel link: %+v", e.Confirmation)
	}

	code, step1 := get(t, sco.CancelSubscriptionURL)
	if code != 200 || !strings.Contains(step1, "Anuluj subskrypcję") || strings.Contains(step1, "<form") {
		t.Fatalf("cancel step 1: %d\n%s", code, step1)
	}
	m := hrefRe.FindStringSubmatch(step1)
	code, step2 := get(t, k.url+strings.ReplaceAll(m[1], "&amp;", "&"))
	if code != 200 || !strings.Contains(step2, "Potwierdź anulowanie subskrypcji") {
		t.Fatalf("cancel step 2: %d\n%s", code, step2)
	}
	if n := k.app.count(payments.EventSubscriptionCancelled); n != 0 {
		t.Fatalf("the GET steps cancelled (%d events)", n)
	}
	sig := strings.SplitN(sco.CancelSubscriptionURL, "?t=", 2)[1]
	if code, _ := post(t, k.url+"/cancel/"+sco.SubscriptionID, url.Values{"t": {"bad"}}); code != http.StatusNotFound {
		t.Errorf("forged cancel: %d", code)
	}
	if code, body := post(t, k.url+"/cancel/"+sco.SubscriptionID, url.Values{"t": {sig}}); code != 200 || !strings.Contains(body, "anulowana") {
		t.Fatalf("cancel confirm: %d\n%s", code, body)
	}
	e := lastEvent(t, k.app, payments.EventSubscriptionCancelled)
	if e.Subscription.State != payments.SubscriptionCancelled || e.Confirmation == nil || e.Confirmation.Kind != "cancellation_receipt" || e.Confirmation.To != "buyer@example.com" {
		t.Errorf("subscription.cancelled = %+v / %+v", e.Subscription, e.Confirmation)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(b)))
	return len(b), nil
}

// TestConsentDocNamesTheLawAndReCheck: docs/payments.md says the withdrawal
// button is two-step under Directive 2023/2673 although Poland has not
// transposed it, names the bills and the date to re-check, and that the
// wording is a placeholder pending the lawyer's review.
func TestConsentDocNamesTheLawAndReCheck(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "payments.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"## Consent, withdrawal and cancel", "2023/2673", "two steps", "UC169", "print 2799",
		"Re-check on 2027-04-01", "Odstąp od umowy tutaj", "Potwierdź odstąpienie od umowy",
		"placeholder", "lawyer", "`withdrawal.requested`", "never refunds by itself",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/payments.md lacks %q", want)
		}
	}
}
