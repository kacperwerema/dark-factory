// Package paymentstest is the contract every payments provider adapter
// passes: the stand-in in the normal suite, a real adapter against its own
// sandbox (off by default: it needs the owner's sandbox keys).
package paymentstest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/payments"
)

// Driver does what the buyer and the provider's clock do on the provider's
// side: pay or decline on the hosted page, and charge a subscription's next
// period. A real adapter's driver works its sandbox.
type Driver interface {
	Pay(t testing.TB, co payments.Checkout)
	Decline(t testing.TB, co payments.Checkout)
	Renew(t testing.TB, subscriptionID string)
}

// Setup builds the adapter under test with webhookURL as where it sends
// webhooks, and its driver.
type Setup func(t testing.TB, webhookURL string) (payments.Provider, Driver)

// Webhook is one webhook the provider sent.
type Webhook struct {
	Header http.Header
	Body   []byte
}

type capture struct {
	mu   sync.Mutex
	hook []Webhook
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.hook = append(c.hook, Webhook{r.Header.Clone(), b})
	c.mu.Unlock()
}

// Wait is how long the contract waits for a webhook; a sandbox delivers
// in seconds, the stand-in at once.
var Wait = 60 * time.Second

// waitFor parses webhooks from index from on until one carries an event of
// type typ for the payment or subscription id; it returns that event and
// the webhook it came in.
func (c *capture) waitFor(t testing.TB, p payments.Provider, typ payments.EventType, id string) (payments.Event, Webhook) {
	t.Helper()
	deadline := time.Now().Add(Wait)
	seen := 0
	for {
		c.mu.Lock()
		hooks := slices.Clone(c.hook)
		c.mu.Unlock()
		for ; seen < len(hooks); seen++ {
			evs, err := p.ParseWebhook(hooks[seen].Header, hooks[seen].Body)
			if err != nil {
				t.Fatalf("ParseWebhook on the provider's own webhook: %v", err)
			}
			for _, ev := range evs {
				if ev.Type != typ {
					continue
				}
				if (ev.Payment != nil && ev.Payment.ID == id) || (ev.Subscription != nil && ev.Subscription.ID == id) {
					if ev.ID == "" {
						t.Fatalf("%s event has no id: the kit can't dedupe it", typ)
					}
					return ev, hooks[seen]
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s webhook for %s within %s", typ, id, Wait)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func oneOff(ref string, amount int64, currency string) payments.CheckoutRequest {
	return payments.CheckoutRequest{
		Mode:       payments.ModeOneOff,
		Items:      []payments.Item{{Name: "Contract item", Quantity: 1, UnitPrice: payments.Money{Amount: amount, Currency: currency}}},
		Customer:   payments.Customer{Email: "buyer@example.com", Country: "PL"},
		Reference:  ref,
		SuccessURL: "https://app.example/ok",
		CancelURL:  "https://app.example/cancel",
	}
}

// Contract runs the whole provider contract on the adapter setup builds.
func Contract(t *testing.T, setup Setup) {
	cap := &capture{}
	hooks := httptest.NewServer(cap)
	defer hooks.Close()
	p, d := setup(t, hooks.URL+"/v1/webhooks/x")
	ctx := context.Background()
	caps := p.Capabilities()
	if len(caps.Methods) == 0 || len(caps.Modes) == 0 || len(caps.Currencies) == 0 {
		t.Fatalf("Capabilities lists no method, mode or currency: %+v", caps)
	}
	for _, m := range caps.Methods {
		if !slices.Contains(payments.Methods, m) {
			t.Errorf("Capabilities names %q, not a neutral method", m)
		}
	}
	cur := caps.Currencies[0]

	t.Run("refuses what it doesn't offer", func(t *testing.T) {
		if _, err := p.CreateCheckout(ctx, oneOff("c-0", 100, "XXX")); err == nil {
			t.Error("a checkout in currency XXX was opened")
		}
		bad := oneOff("c-0", 100, cur)
		bad.Methods = []payments.Method{"cheque"}
		if _, err := p.CreateCheckout(ctx, bad); err == nil {
			t.Error("a checkout with method cheque was opened")
		}
	})

	t.Run("one-off: pay, look up, refund", func(t *testing.T) {
		req := oneOff("c-1", 4999, cur)
		req.IdempotencyKey = "contract-c-1"
		co, err := p.CreateCheckout(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(co.URL, "http://") && !strings.HasPrefix(co.URL, "https://") {
			t.Fatalf("checkout URL %q is not a hosted page", co.URL)
		}
		again, err := p.CreateCheckout(ctx, req)
		if err != nil || again.ID != co.ID {
			t.Fatalf("same idempotency key opened another checkout: %s vs %s (%v)", again.ID, co.ID, err)
		}
		d.Pay(t, co)
		ev, wh := cap.waitFor(t, p, payments.EventPaymentPaid, co.PaymentID)
		if ev.Payment.Amount != (payments.Money{Amount: 4999, Currency: cur}) || ev.Reference != "c-1" {
			t.Errorf("payment.paid carries %+v, ref %q", ev.Payment.Amount, ev.Reference)
		}
		if got, _ := p.Payment(ctx, co.PaymentID); got.State != payments.PaymentPaid {
			t.Errorf("Payment state after paying = %s", got.State)
		}

		tampered := append(slices.Clone(wh.Body[:len(wh.Body)-1]), ' ', wh.Body[len(wh.Body)-1])
		if _, err := p.ParseWebhook(wh.Header, tampered); !errors.Is(err, payments.ErrSignature) {
			t.Errorf("a tampered body parsed: %v", err)
		}
		if _, err := p.ParseWebhook(http.Header{}, wh.Body); !errors.Is(err, payments.ErrSignature) {
			t.Errorf("a webhook with no signature parsed: %v", err)
		}

		if _, err := p.Refund(ctx, payments.RefundRequest{PaymentID: co.PaymentID, Amount: 1000}); err != nil {
			t.Fatal(err)
		}
		cap.waitFor(t, p, payments.EventPaymentRefunded, co.PaymentID)
		if got, _ := p.Payment(ctx, co.PaymentID); got.State != payments.PaymentPartiallyRefunded || got.Refunded != 1000 {
			t.Errorf("after a partial refund: %s, refunded %d", got.State, got.Refunded)
		}
		if _, err := p.Refund(ctx, payments.RefundRequest{PaymentID: co.PaymentID, Amount: 5000}); err == nil {
			t.Error("a refund over what is left was accepted")
		}
		if _, err := p.Refund(ctx, payments.RefundRequest{PaymentID: co.PaymentID}); err != nil {
			t.Fatal(err)
		}
		if got, _ := p.Payment(ctx, co.PaymentID); got.State != payments.PaymentRefunded {
			t.Errorf("after refunding the rest: %s", got.State)
		}
		if _, err := p.Payment(ctx, "no-such-payment"); !errors.Is(err, payments.ErrNotFound) {
			t.Errorf("unknown payment: %v, want ErrNotFound", err)
		}
	})

	t.Run("declined", func(t *testing.T) {
		co, err := p.CreateCheckout(ctx, oneOff("c-2", 1500, cur))
		if err != nil {
			t.Fatal(err)
		}
		d.Decline(t, co)
		cap.waitFor(t, p, payments.EventPaymentFailed, co.PaymentID)
		if got, _ := p.Payment(ctx, co.PaymentID); got.State != payments.PaymentFailed {
			t.Errorf("declined payment state = %s", got.State)
		}
		if _, err := p.Refund(ctx, payments.RefundRequest{PaymentID: co.PaymentID}); err == nil {
			t.Error("a failed payment was refunded")
		}
	})

	if !slices.Contains(caps.Modes, payments.ModeSubscription) {
		return
	}
	t.Run("subscription: pay, renew, cancel", func(t *testing.T) {
		req := oneOff("c-3", 2900, cur)
		req.Mode, req.Interval = payments.ModeSubscription, payments.IntervalMonth
		co, err := p.CreateCheckout(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if co.SubscriptionID == "" {
			t.Fatal("a subscription checkout has no subscription id")
		}
		d.Pay(t, co)
		cap.waitFor(t, p, payments.EventPaymentPaid, co.PaymentID)
		d.Renew(t, co.SubscriptionID)
		ev, _ := cap.waitFor(t, p, payments.EventSubscriptionRenewed, co.SubscriptionID)
		if ev.Payment == nil || ev.Payment.State != payments.PaymentPaid || ev.Payment.ID == co.PaymentID {
			t.Errorf("subscription.renewed wants the period's new paid payment, got %+v", ev.Payment)
		}
		sub, err := p.CancelSubscription(ctx, co.SubscriptionID)
		if err != nil || sub.State != payments.SubscriptionCancelled {
			t.Fatalf("cancel: %+v, %v", sub, err)
		}
		cap.waitFor(t, p, payments.EventSubscriptionCancelled, co.SubscriptionID)
	})
}
