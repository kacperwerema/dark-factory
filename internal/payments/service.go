package payments

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Mounter is an adapter that serves pages of its own under the kit (the
// stand-in's hosted checkout). Real providers host theirs.
type Mounter interface {
	Mount(mux *http.ServeMux)
}

// maxBody caps any request body the kit reads.
const maxBody = 1 << 20

// Service is the kit: the app's API and the providers' webhook intake.
type Service struct {
	Provider  Provider
	Invoicer  Invoicer
	Store     Store
	EventsURL string
	APIToken  []byte
	// ReadToken, when set, is a second bearer that reaches only the read
	// routes (a payment's state, the provider): the one an agent's lookup
	// connector holds, so it can't refund, cancel or open a checkout.
	ReadToken    []byte
	EventsSecret []byte
	// Client sends events to the app; a 10 s timeout by default.
	Client *http.Client
	// Now is the clock for signatures; time.Now by default.
	Now func() time.Time
	Log *log.Logger
	// Consent is the consent flow (payments.yaml consent:); nil = off.
	Consent *ConsentFlow

	locks sync.Map // event key → *sync.Mutex
}

// Open builds the Service a config describes: it reads the secrets, builds
// the adapter and opens the state dir. Run Validate first for a list of
// problems; Open stops at the first.
func Open(c Config) (*Service, error) {
	if ps := c.Validate(); len(ps) > 0 {
		return nil, fmt.Errorf("payments.yaml: %s", ps[0])
	}
	reg, _ := lookup(c.Provider)
	st, err := c.settings(reg)
	if err != nil {
		return nil, err
	}
	p, err := reg.New(st)
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", c.Provider, err)
	}
	tok, err := c.secret(SecretAPIToken)
	if err != nil {
		return nil, err
	}
	evs, err := c.secret(SecretEventsSecret)
	if err != nil {
		return nil, err
	}
	var readTok []byte
	if _, have := c.Secrets[SecretReadToken]; have {
		rt, err := c.secret(SecretReadToken)
		if err != nil {
			return nil, err
		}
		readTok = []byte(rt)
	}
	store, err := OpenFileStore(filepath.Join(c.StatePath(), "handled.jsonl"))
	if err != nil {
		return nil, err
	}
	var inv Invoicer = NoInvoice{}
	if c.Invoice == "record" {
		inv = RecordInvoice{Dir: filepath.Join(c.StatePath(), "invoices")}
	}
	svc := &Service{
		Provider: p, Invoicer: inv, Store: store, EventsURL: c.EventsURL,
		APIToken: []byte(tok), ReadToken: readTok, EventsSecret: []byte(evs),
	}
	if c.Consent != nil {
		// The link key is derived from events_secret, so no new secret.
		svc.Consent, err = NewConsentFlow(*c.Consent, c.Dir, c.StatePath(), c.PublicURL, linkKey([]byte(evs)))
		if err != nil {
			return nil, err
		}
	}
	return svc, nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (s *Service) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log.Printf(format, a...)
	}
}

// Handler serves the kit's routes (docs/payments.md#the-api).
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/checkouts", s.auth(s.createCheckout))
	mux.HandleFunc("GET /v1/payments/{id}", s.authRead(s.getPayment))
	mux.HandleFunc("POST /v1/payments/{id}/refunds", s.auth(s.refund))
	mux.HandleFunc("POST /v1/subscriptions/{id}/cancel", s.auth(s.cancelSubscription))
	mux.HandleFunc("GET /v1/provider", s.authRead(s.provider))
	mux.HandleFunc("POST /v1/webhooks/{provider}", s.webhook)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "provider": s.Provider.Name()})
	})
	if s.Consent != nil {
		s.mountConsent(mux)
	}
	if m, ok := s.Provider.(Mounter); ok {
		m.Mount(mux)
	}
	return mux
}

// ErrReadToken is the message a write route answers the read token with.
const ErrReadToken = "this is the read token: checkouts, refunds and cancels need the api token"

func bearer(r *http.Request) (string, bool) {
	return strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func tokenIs(tok string, want []byte) bool {
	return len(want) > 0 && subtle.ConstantTimeCompare([]byte(tok), want) == 1
}

// auth lets only the api token through; the read token gets 403, anything
// else 401.
func (s *Service) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := bearer(r)
		switch {
		case ok && tokenIs(tok, s.APIToken):
			h(w, r)
		case ok && tokenIs(tok, s.ReadToken):
			writeErr(w, http.StatusForbidden, ErrReadToken)
		default:
			writeErr(w, http.StatusUnauthorized, "wants Authorization: Bearer <api_token>")
		}
	}
}

// authRead lets the api token or the read token through.
func (s *Service) authRead(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := bearer(r)
		if !ok || (!tokenIs(tok, s.APIToken) && !tokenIs(tok, s.ReadToken)) {
			writeErr(w, http.StatusUnauthorized, "wants Authorization: Bearer <api_token> or <read_token>")
			return
		}
		h(w, r)
	}
}

func (s *Service) provider(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"name": s.Provider.Name(), "capabilities": s.Provider.Capabilities()})
}

func (s *Service) createCheckout(w http.ResponseWriter, r *http.Request) {
	var req CheckoutRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := req.Validate(s.Provider.Capabilities()); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	switch {
	case s.Consent == nil && req.Consent != nil:
		writeErr(w, http.StatusUnprocessableEntity, "consent: this kit has no consent: block in payments.yaml, so it can't keep the record; add one or leave consent out")
		return
	case s.Consent != nil:
		if msg := s.Consent.check(req.Consent); msg != "" {
			writeErr(w, http.StatusUnprocessableEntity, msg)
			return
		}
	}
	co, err := s.Provider.CreateCheckout(r.Context(), req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if s.Consent != nil && req.Consent != nil {
		if _, err := s.Consent.record(req, co, s.now()); err != nil {
			writeErr(w, http.StatusInternalServerError, "consent record: "+err.Error())
			return
		}
		co.WithdrawURL = s.Consent.WithdrawURL(co.PaymentID)
		if co.SubscriptionID != "" {
			co.CancelSubscriptionURL = s.Consent.CancelURL(co.SubscriptionID)
		}
	}
	writeJSON(w, http.StatusCreated, co)
}

func (s *Service) getPayment(w http.ResponseWriter, r *http.Request) {
	p, err := s.Provider.Payment(r.Context(), r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Service) refund(w http.ResponseWriter, r *http.Request) {
	var req RefundRequest
	if !readJSON(w, r, &req) {
		return
	}
	req.PaymentID = r.PathValue("id")
	if req.Amount < 0 {
		writeErr(w, http.StatusUnprocessableEntity, "amount: wants minor units, 0 for all that is left")
		return
	}
	ref, err := s.Provider.Refund(r.Context(), req)
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeJSON(w, http.StatusCreated, ref)
	}
}

func (s *Service) cancelSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := s.Provider.CancelSubscription(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeJSON(w, http.StatusOK, sub)
	}
}

// webhook verifies a provider's webhook, and hands each event in it the app
// has not accepted yet to the app. It answers 200 only when every event is
// handled, so a provider retries the rest; an app that is down costs a
// retry, never an event.
func (s *Service) webhook(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("provider") != s.Provider.Name() {
		writeErr(w, http.StatusNotFound, "no provider "+r.PathValue("provider")+" here")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		writeErr(w, http.StatusBadRequest, "body unreadable or over 1 MB")
		return
	}
	events, err := s.Provider.ParseWebhook(r.Header, body)
	if errors.Is(err, ErrSignature) {
		s.logf("webhook refused: %v", err)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	handled, dup := 0, 0
	for _, ev := range events {
		ok, err := s.handle(r.Context(), ev)
		if err != nil {
			s.logf("event %s/%s not handled: %v", ev.Provider, ev.ID, err)
			writeErr(w, http.StatusBadGateway, fmt.Sprintf("event %s: %v", ev.ID, err))
			return
		}
		if ok {
			handled++
		} else {
			dup++
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"handled": handled, "duplicates": dup})
}

func (s *Service) lock(key string) func() {
	m, _ := s.locks.LoadOrStore(key, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// handle sends ev to the app once: false when it was already handled.
func (s *Service) handle(ctx context.Context, ev Event) (bool, error) {
	if ev.ID == "" {
		return false, fmt.Errorf("event without an id")
	}
	ev.Provider = s.Provider.Name()
	key := ev.Provider + "/" + ev.ID
	defer s.lock(key)()
	if s.Store.Handled(key) {
		return false, nil
	}
	if p := ev.Payment; p != nil && p.State == PaymentPaid && (ev.Type == EventPaymentPaid || ev.Type == EventSubscriptionRenewed) {
		inv, err := s.Invoicer.Issue(ctx, InvoiceRequest{
			EventID: ev.ID, Provider: ev.Provider, Payment: *p, IssuedAt: s.now().UTC(),
			MerchantOfRecord: s.Provider.Capabilities().MerchantOfRecord,
		})
		if err != nil {
			return false, fmt.Errorf("invoice hook: %w", err)
		}
		ev.Invoice = inv
	}
	if err := s.enrich(&ev); err != nil {
		return false, fmt.Errorf("consent flow: %w", err)
	}
	if err := s.deliver(ctx, ev); err != nil {
		return false, err
	}
	return true, s.Store.MarkHandled(key)
}

func (s *Service) deliver(ctx context.Context, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.EventsURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, Sign(s.EventsSecret, s.now(), body))
	req.Header.Set(EventIDHeader, ev.Provider+"/"+ev.ID)
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("app at events_url: %w", err)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("app at events_url answered %d", resp.StatusCode)
	}
	return nil
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
