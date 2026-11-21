// Package standin is the stand-in payment provider: a full adapter of the
// payments kit with no outside party, for tests and local runs. It keeps its
// state in memory, serves its own hosted checkout page under the kit
// (/standin/…), signs each webhook like a real provider and sends it to the
// kit's webhook URL. It takes no money and refuses mode: live.
//
// Not to be confused with internal/standin, which writes executable
// stand-ins for tests.
package standin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/payments"
)

// Name is the adapter's registered name.
const Name = "standin"

// SignatureHeader carries the stand-in's webhook signature, in the kit's
// t=<unix>,v1=<hex> form under the webhook_secret setting.
const SignatureHeader = "Standin-Signature"

func init() {
	payments.Register(payments.Registration{
		Name:     Name,
		Secrets:  []string{"webhook_secret"},
		Required: []string{"webhook_secret"},
		Live:     false,
		New: func(s payments.Settings) (payments.Provider, error) {
			if s.Mode == "live" {
				return nil, fmt.Errorf("the stand-in has no live side")
			}
			p := New(s.Values["webhook_secret"], s.WebhookURL)
			if v := s.Values["decline_amount"]; v != "" {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n <= 0 {
					return nil, fmt.Errorf("decline_amount: wants minor units, got %q", v)
				}
				p.DeclineAmount = n
			}
			p.PublicURL = s.PublicURL
			return p, nil
		},
	})
}

// Delivery is one webhook the stand-in sent, kept so it can be sent again.
type Delivery struct {
	Event     payments.Event `json:"event"`
	Delivered bool           `json:"delivered"`
	Status    int            `json:"status,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type checkout struct {
	co   payments.Checkout
	req  payments.CheckoutRequest
	done bool
}

// Provider is the stand-in.
type Provider struct {
	// WebhookURL is where webhooks go: the kit's /v1/webhooks/standin.
	WebhookURL string
	// PublicURL is the kit's base URL; checkout pages live under it.
	PublicURL string
	// DeclineAmount: a checkout whose total is this many minor units is
	// always declined, for a scripted failure.
	DeclineAmount int64
	// Now is the clock; time.Now by default.
	Now    func() time.Time
	Client *http.Client

	secret []byte

	mu         sync.Mutex
	n          int
	checkouts  map[string]*checkout
	byKey      map[string]string // idempotency key → checkout id
	paymentsBy map[string]*payments.Payment
	subs       map[string]*payments.Subscription
	refundKeys map[string]payments.Refund
	log        []*Delivery
}

// New makes a stand-in that signs with secret and sends to webhookURL.
func New(secret, webhookURL string) *Provider {
	return &Provider{
		WebhookURL: webhookURL,
		secret:     []byte(secret),
		checkouts:  map[string]*checkout{},
		byKey:      map[string]string{},
		paymentsBy: map[string]*payments.Payment{},
		subs:       map[string]*payments.Subscription{},
		refundKeys: map[string]payments.Refund{},
	}
}

func (p *Provider) Name() string { return Name }

func (p *Provider) Capabilities() payments.Capabilities {
	return payments.Capabilities{
		Methods:    payments.Methods,
		Modes:      []payments.Mode{payments.ModeOneOff, payments.ModeSubscription},
		Currencies: []string{"PLN", "EUR", "USD", "GBP"},
		Refunds:    true,
	}
}

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Provider) id(prefix string) string {
	p.n++
	return fmt.Sprintf("%s_%04d", prefix, p.n)
}

func (p *Provider) base() string {
	if p.PublicURL != "" {
		return p.PublicURL
	}
	u, err := url.Parse(p.WebhookURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// CreateCheckout opens a checkout page at <public_url>/standin/checkout/<id>.
func (p *Provider) CreateCheckout(_ context.Context, r payments.CheckoutRequest) (payments.Checkout, error) {
	if err := r.Validate(p.Capabilities()); err != nil {
		return payments.Checkout{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.IdempotencyKey != "" {
		if id, ok := p.byKey[r.IdempotencyKey]; ok {
			return p.checkouts[id].co, nil
		}
	}
	co := payments.Checkout{ID: p.id("cs"), ExpiresAt: p.now().Add(time.Hour).UTC()}
	co.URL = p.base() + "/standin/checkout/" + co.ID
	pay := &payments.Payment{ID: p.id("pay"), State: payments.PaymentPending, Amount: r.Total(), Reference: r.Reference}
	co.PaymentID = pay.ID
	if r.Mode == payments.ModeSubscription {
		sub := &payments.Subscription{ID: p.id("sub"), State: payments.SubscriptionPending, Price: r.Total(), Interval: r.Interval, Reference: r.Reference}
		p.subs[sub.ID] = sub
		co.SubscriptionID = sub.ID
		pay.SubscriptionID = sub.ID
	}
	p.paymentsBy[pay.ID] = pay
	p.checkouts[co.ID] = &checkout{co: co, req: r}
	if r.IdempotencyKey != "" {
		p.byKey[r.IdempotencyKey] = co.ID
	}
	return co, nil
}

// Pay is the buyer paying on the hosted page with method m: payment.paid,
// or payment.failed when the total is DeclineAmount. It returns where the
// page sends the buyer.
func (p *Provider) Pay(id string, m payments.Method) (string, error) {
	return p.finish(id, m, false)
}

// Decline is the buyer's bank declining: payment.failed.
func (p *Provider) Decline(id string, m payments.Method) (string, error) {
	return p.finish(id, m, true)
}

func (p *Provider) finish(id string, m payments.Method, decline bool) (string, error) {
	p.mu.Lock()
	c, ok := p.checkouts[id]
	if !ok {
		p.mu.Unlock()
		return "", payments.ErrNotFound
	}
	if c.done {
		p.mu.Unlock()
		return "", fmt.Errorf("checkout %s is already finished", id)
	}
	if m == "" {
		m = payments.MethodCard
	}
	pay := p.paymentsBy[c.co.PaymentID]
	if p.DeclineAmount > 0 && pay.Amount.Amount == p.DeclineAmount {
		decline = true
	}
	c.done = true
	pay.Method = m
	ev := payments.Event{ID: p.id("evt"), Provider: Name, Occurred: p.now().UTC(), Reference: c.req.Reference}
	back := c.req.SuccessURL
	if decline {
		pay.State = payments.PaymentFailed
		pay.FailureReason = "declined by the stand-in"
		ev.Type = payments.EventPaymentFailed
		back = c.req.CancelURL
	} else {
		pay.State = payments.PaymentPaid
		ev.Type = payments.EventPaymentPaid
		if sub := p.subs[c.co.SubscriptionID]; sub != nil {
			sub.State = payments.SubscriptionActive
			s := *sub
			ev.Subscription = &s
		}
	}
	cp := *pay
	ev.Payment = &cp
	d := p.record(ev)
	p.mu.Unlock()
	p.send(d)
	return back, nil
}

// Renew charges an active subscription's next period: subscription.renewed
// with the new payment.
func (p *Provider) Renew(subID string) (payments.Event, error) {
	p.mu.Lock()
	sub, ok := p.subs[subID]
	if !ok {
		p.mu.Unlock()
		return payments.Event{}, payments.ErrNotFound
	}
	if sub.State != payments.SubscriptionActive {
		p.mu.Unlock()
		return payments.Event{}, fmt.Errorf("subscription %s is %s, not active", subID, sub.State)
	}
	pay := &payments.Payment{ID: p.id("pay"), State: payments.PaymentPaid, Amount: sub.Price, Method: payments.MethodCard, Reference: sub.Reference, SubscriptionID: sub.ID}
	p.paymentsBy[pay.ID] = pay
	cp, s := *pay, *sub
	ev := payments.Event{ID: p.id("evt"), Type: payments.EventSubscriptionRenewed, Provider: Name, Occurred: p.now().UTC(), Reference: sub.Reference, Payment: &cp, Subscription: &s}
	d := p.record(ev)
	p.mu.Unlock()
	p.send(d)
	return ev, nil
}

// ParseWebhook checks the Standin-Signature header, then reads the event.
func (p *Provider) ParseWebhook(h http.Header, body []byte) ([]payments.Event, error) {
	if err := payments.Verify(p.secret, h.Get(SignatureHeader), body, p.now()); err != nil {
		return nil, err
	}
	var ev payments.Event
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("stand-in webhook: %w", err)
	}
	return []payments.Event{ev}, nil
}

func (p *Provider) Payment(_ context.Context, id string) (payments.Payment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pay, ok := p.paymentsBy[id]
	if !ok {
		return payments.Payment{}, fmt.Errorf("payment %s: %w", id, payments.ErrNotFound)
	}
	return *pay, nil
}

// Refund refunds what is left (Amount 0) or part of it; more is refused.
func (p *Provider) Refund(_ context.Context, r payments.RefundRequest) (payments.Refund, error) {
	p.mu.Lock()
	if r.IdempotencyKey != "" {
		if ref, ok := p.refundKeys[r.IdempotencyKey]; ok {
			p.mu.Unlock()
			return ref, nil
		}
	}
	pay, ok := p.paymentsBy[r.PaymentID]
	if !ok {
		p.mu.Unlock()
		return payments.Refund{}, fmt.Errorf("payment %s: %w", r.PaymentID, payments.ErrNotFound)
	}
	if pay.State != payments.PaymentPaid && pay.State != payments.PaymentPartiallyRefunded {
		p.mu.Unlock()
		return payments.Refund{}, fmt.Errorf("payment %s is %s: only a paid payment is refunded", pay.ID, pay.State)
	}
	left := pay.Amount.Amount - pay.Refunded
	amt := r.Amount
	if amt == 0 {
		amt = left
	}
	if amt > left {
		p.mu.Unlock()
		return payments.Refund{}, fmt.Errorf("refund of %d is more than the %d %s left on %s", amt, left, pay.Amount.Currency, pay.ID)
	}
	pay.Refunded += amt
	pay.State = payments.PaymentPartiallyRefunded
	if pay.Refunded == pay.Amount.Amount {
		pay.State = payments.PaymentRefunded
	}
	ref := payments.Refund{ID: p.id("re"), PaymentID: pay.ID, Amount: payments.Money{Amount: amt, Currency: pay.Amount.Currency}}
	if r.IdempotencyKey != "" {
		p.refundKeys[r.IdempotencyKey] = ref
	}
	cp, rc := *pay, ref
	d := p.record(payments.Event{ID: p.id("evt"), Type: payments.EventPaymentRefunded, Provider: Name, Occurred: p.now().UTC(), Reference: pay.Reference, Payment: &cp, Refund: &rc})
	p.mu.Unlock()
	p.send(d)
	return ref, nil
}

// CancelSubscription stops a subscription: subscription.cancelled.
func (p *Provider) CancelSubscription(_ context.Context, id string) (payments.Subscription, error) {
	p.mu.Lock()
	sub, ok := p.subs[id]
	if !ok {
		p.mu.Unlock()
		return payments.Subscription{}, fmt.Errorf("subscription %s: %w", id, payments.ErrNotFound)
	}
	if sub.State == payments.SubscriptionCancelled {
		s := *sub
		p.mu.Unlock()
		return s, nil
	}
	sub.State = payments.SubscriptionCancelled
	s := *sub
	sc := s
	d := p.record(payments.Event{ID: p.id("evt"), Type: payments.EventSubscriptionCancelled, Provider: Name, Occurred: p.now().UTC(), Reference: sub.Reference, Subscription: &sc})
	p.mu.Unlock()
	p.send(d)
	return s, nil
}

// record adds ev to the delivery log; the caller holds p.mu.
func (p *Provider) record(ev payments.Event) *Delivery {
	d := &Delivery{Event: ev}
	p.log = append(p.log, d)
	return d
}

// Deliveries is the delivery log, oldest first.
func (p *Provider) Deliveries() []Delivery {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Delivery, len(p.log))
	for i, d := range p.log {
		out[i] = *d
	}
	return out
}

// Redeliver sends event id's webhook again, freshly signed, as a provider
// retrying would; it returns the kit's status code.
func (p *Provider) Redeliver(id string) (int, error) {
	p.mu.Lock()
	var d *Delivery
	for _, x := range p.log {
		if x.Event.ID == id {
			d = x
		}
	}
	p.mu.Unlock()
	if d == nil {
		return 0, payments.ErrNotFound
	}
	p.send(d)
	p.mu.Lock()
	defer p.mu.Unlock()
	if d.Error != "" {
		return d.Status, fmt.Errorf("%s", d.Error)
	}
	return d.Status, nil
}

// Signed returns ev's webhook body and its signature header value, as the
// stand-in would send it now: for tests that replay or tamper with one.
func (p *Provider) Signed(ev payments.Event) ([]byte, string) {
	body, _ := json.Marshal(ev)
	return body, payments.Sign(p.secret, p.now(), body)
}

func (p *Provider) send(d *Delivery) {
	body, sig := p.Signed(d.Event)
	status, err := p.post(body, sig)
	p.mu.Lock()
	defer p.mu.Unlock()
	d.Status = status
	d.Delivered = err == nil && status/100 == 2
	d.Error = ""
	if err != nil {
		d.Error = err.Error()
	} else if !d.Delivered {
		d.Error = fmt.Sprintf("kit answered %d", status)
	}
}

func (p *Provider) post(body []byte, sig string) (int, error) {
	req, err := http.NewRequest(http.MethodPost, p.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, sig)
	c := p.Client
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

var page = template.Must(template.New("checkout").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Stand-in checkout</title>
<meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body style="font-family:sans-serif;max-width:32rem;margin:2rem auto">
<p><strong>Stand-in provider</strong>: no money moves here. A real provider's hosted page takes the card or BLIK code; the kit never sees it.</p>
<h1>{{.Total}}</h1>
<ul>{{range .Items}}<li>{{.Quantity}} × {{.Name}}</li>{{end}}</ul>
{{if .Done}}<p>This checkout is finished.</p>{{else}}
<form method="post" action="/standin/checkouts/{{.ID}}/pay">
<label>Method <select name="method">{{range .Methods}}<option>{{.}}</option>{{end}}</select></label>
<button type="submit">Pay</button></form>
<form method="post" action="/standin/checkouts/{{.ID}}/decline"><button type="submit">Decline</button></form>{{end}}
</body></html>`))

// Mount serves the hosted page and the stand-in's controls under /standin/.
func (p *Provider) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /standin/checkout/{id}", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		c, ok := p.checkouts[r.PathValue("id")]
		var data map[string]any
		if ok {
			ms := c.req.Methods
			if len(ms) == 0 {
				ms = p.Capabilities().Methods
			}
			data = map[string]any{"ID": c.co.ID, "Total": c.req.Total().String(), "Items": c.req.Items, "Methods": ms, "Done": c.done}
		}
		p.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page.Execute(w, data)
	})
	finish := func(decline bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			m := payments.Method(r.FormValue("method"))
			back, err := p.finish(r.PathValue("id"), m, decline)
			if err == payments.ErrNotFound {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Redirect(w, r, back, http.StatusSeeOther)
		}
	}
	mux.HandleFunc("POST /standin/checkouts/{id}/pay", finish(false))
	mux.HandleFunc("POST /standin/checkouts/{id}/decline", finish(true))
	mux.HandleFunc("POST /standin/subscriptions/{id}/renew", func(w http.ResponseWriter, r *http.Request) {
		ev, err := p.Renew(r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, ev)
	})
	mux.HandleFunc("POST /standin/subscriptions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		sub, err := p.CancelSubscription(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, sub)
	})
	mux.HandleFunc("GET /standin/events", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, p.Deliveries()) })
	mux.HandleFunc("POST /standin/events/{id}/redeliver", func(w http.ResponseWriter, r *http.Request) {
		status, err := p.Redeliver(r.PathValue("id"))
		res := map[string]any{"kit_status": status}
		if err != nil {
			res["error"] = err.Error()
		}
		writeJSON(w, res)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
