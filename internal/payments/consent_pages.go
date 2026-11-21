package payments

import (
	"errors"
	"html/template"
	"net/http"
	"strings"
)

// The buyer's pages: two steps each. The first step only shows a button
// (a GET that records nothing); the second is a form whose button POSTs,
// and only that POST records the withdrawal or cancels.

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>body{font-family:system-ui,sans-serif;max-width:36rem;margin:2rem auto;padding:0 1rem;line-height:1.5}
.ph{background:#fff3cd;border:1px solid #e0c36c;padding:.5rem .75rem}button{font-size:1rem;padding:.6rem 1rem}
label{display:block;margin:.75rem 0 .25rem}input{font-size:1rem;padding:.4rem;width:100%;box-sizing:border-box}</style>
</head><body>
{{if .Placeholder}}<p class="ph" data-placeholder>PLACEHOLDER wording, pending the lawyer's review: not for live use.</p>{{end}}
<h1>{{.Title}}</h1>
{{range .Lines}}<p>{{.}}</p>{{end}}
{{if eq .Step "button"}}<p><a href="{{.Next}}" data-step="1"><button type="button">{{.Button}}</button></a></p>{{end}}
{{if eq .Step "form"}}<form method="post" action="{{.Action}}" data-step="2">
<input type="hidden" name="t" value="{{.Sig}}">
{{if .AskName}}<label for="name">{{.FieldName}}</label><input id="name" name="name" required autocomplete="name">
<label for="email">{{.FieldEmail}}</label><input id="email" name="email" type="email" required value="{{.Email}}" autocomplete="email">
<label for="contract">{{.FieldContract}}</label><input id="contract" name="contract" value="{{.Reference}}" readonly>{{end}}
<p><button type="submit">{{.Button}}</button></p></form>{{end}}
</body></html>
`))

// notPaid is shown for an order whose payment hasn't gone through.
const notPaid = "The payment for this order has not gone through, so there is nothing to withdraw from yet."

type page struct {
	Lang, Title, Step, Next, Action, Sig, Button string
	Lines                                        []string
	Placeholder, AskName                         bool
	FieldName, FieldEmail, FieldContract         string
	Email, Reference                             string
}

func (f *ConsentFlow) render(w http.ResponseWriter, code int, p page) {
	p.Placeholder = f.Texts.Placeholder
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(code)
	pageTmpl.Execute(w, p)
}

func (s *Service) mountConsent(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/consent/texts", s.authRead(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.Consent.TextsFor(r.URL.Query().Get("lang")))
	}))
	mux.HandleFunc("GET /withdraw/{id}", s.withdrawPage(false))
	mux.HandleFunc("GET /withdraw/{id}/form", s.withdrawPage(true))
	mux.HandleFunc("POST /withdraw/{id}", s.withdrawSubmit)
	mux.HandleFunc("GET /cancel/{id}", s.cancelPage(false))
	mux.HandleFunc("GET /cancel/{id}/form", s.cancelPage(true))
	mux.HandleFunc("POST /cancel/{id}", s.cancelSubmit)
}

func linkSig(r *http.Request) string {
	if t := r.URL.Query().Get("t"); t != "" {
		return t
	}
	return r.PostFormValue("t")
}

// withdrawRecord finds a payment's consent record behind a signed link; a
// wrong signature looks like an unknown payment.
func (s *Service) withdrawRecord(w http.ResponseWriter, r *http.Request) (ConsentRecord, string, bool) {
	id := r.PathValue("id")
	sig := linkSig(r)
	rec, ok := s.Consent.byPayment(id)
	if !s.Consent.sigOK(linkWithdraw, id, sig) || !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return rec, "", false
	}
	return rec, sig, true
}

func (s *Service) withdrawPage(form bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec, sig, ok := s.withdrawRecord(w, r)
		if !ok {
			return
		}
		f := s.Consent
		t := f.Texts.Langs[f.lang(rec.Lang)]
		v := f.vals(rec)
		p := page{Lang: f.lang(rec.Lang), Title: t["withdraw_title"], Lines: []string{fill(t["withdraw_intro"], v)}}
		if prev, done := f.withdrawal(rec.PaymentID); done {
			v["requested_at"], v["email"] = prev.RequestedAt.Format("2006-01-02 15:04 MST"), prev.Email
			p.Lines = []string{fill(t["withdraw_done"], v)}
			f.render(w, http.StatusOK, p)
			return
		}
		if rec.PaidAt == nil {
			p.Lines = append(p.Lines, notPaid)
			f.render(w, http.StatusConflict, p)
			return
		}
		if s.now().After(*rec.Deadline) {
			p.Lines = []string{fill(t["withdraw_expired"], v)}
			f.render(w, http.StatusGone, p)
			return
		}
		if !form {
			p.Step, p.Button, p.Next = "button", t["withdraw_button"], "/withdraw/"+rec.PaymentID+"/form?t="+sig
			f.render(w, http.StatusOK, p)
			return
		}
		p.Step, p.Button, p.Action, p.Sig, p.AskName = "form", t["withdraw_confirm_button"], "/withdraw/"+rec.PaymentID, sig, true
		p.Lines = []string{fill(t["withdraw_form_intro"], v)}
		p.FieldName, p.FieldEmail, p.FieldContract, p.Email, p.Reference = t["field_name"], t["field_email"], t["field_contract"], rec.Email, rec.Reference
		f.render(w, http.StatusOK, p)
	}
}

// withdrawSubmit is the second step: it records the statement first (its
// time is the legal fact), then sends withdrawal.requested once. An app
// that is down gets it on the buyer's next press; the time stays the first.
func (s *Service) withdrawSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form unreadable", http.StatusBadRequest)
		return
	}
	rec, _, ok := s.withdrawRecord(w, r)
	if !ok {
		return
	}
	f := s.Consent
	t := f.Texts.Langs[f.lang(rec.Lang)]
	v := f.vals(rec)
	p := page{Lang: f.lang(rec.Lang), Title: t["withdraw_title"]}
	wf, have := f.withdrawal(rec.PaymentID)
	if !have {
		if rec.PaidAt == nil {
			p.Lines = []string{fill(t["withdraw_intro"], v), notPaid}
			f.render(w, http.StatusConflict, p)
			return
		}
		now := s.now().UTC()
		if now.After(*rec.Deadline) {
			p.Lines = []string{fill(t["withdraw_expired"], v)}
			f.render(w, http.StatusGone, p)
			return
		}
		name, email := strings.TrimSpace(r.PostFormValue("name")), strings.TrimSpace(r.PostFormValue("email"))
		if name == "" || !strings.Contains(email, "@") {
			http.Error(w, "name and email are needed", http.StatusUnprocessableEntity)
			return
		}
		wf = withdrawalFile{Withdrawal: Withdrawal{PaymentID: rec.PaymentID, Reference: rec.Reference, RequestedAt: now, Name: name, Email: email, Deadline: *rec.Deadline, Waiver: rec.Waiver}}
		if err := f.saveWithdrawal(wf); err != nil {
			s.logf("withdrawal %s not recorded: %v", rec.PaymentID, err)
			http.Error(w, "could not record the statement: try again", http.StatusInternalServerError)
			return
		}
	}
	v["requested_at"], v["email"] = wf.RequestedAt.Format("2006-01-02 15:04 MST"), wf.Email
	p.Lines = []string{fill(t["withdraw_done"], v)}
	if !wf.Delivered {
		if err := s.sendWithdrawal(r, rec, wf.Withdrawal); err != nil {
			s.logf("withdrawal %s recorded, not delivered: %v", rec.PaymentID, err)
			p.Lines = append(p.Lines, "The shop did not answer; press the confirm button again to pass it on. Your statement's time stays "+v["requested_at"]+".")
			p.Step, p.Button, p.Action, p.Sig = "form", t["withdraw_confirm_button"], "/withdraw/"+rec.PaymentID, linkSig(r)
			f.render(w, http.StatusServiceUnavailable, p)
			return
		}
		wf.Delivered = true
		if err := f.saveWithdrawal(wf); err != nil {
			s.logf("withdrawal %s delivered, not marked: %v", rec.PaymentID, err)
		}
	}
	f.render(w, http.StatusOK, p)
}

func (s *Service) sendWithdrawal(r *http.Request, rec ConsentRecord, wd Withdrawal) error {
	pay, err := s.Provider.Payment(r.Context(), rec.PaymentID)
	if err != nil {
		pay = Payment{ID: rec.PaymentID, Amount: rec.Amount, Reference: rec.Reference, State: PaymentPaid}
	}
	ev := Event{
		ID: "kit-withdrawal-" + rec.PaymentID, Type: EventWithdrawalRequested, Occurred: wd.RequestedAt,
		Reference: rec.Reference, Payment: &pay, Withdrawal: &wd, Confirmation: s.Consent.withdrawalReceipt(rec, wd),
	}
	_, err = s.handle(r.Context(), ev)
	return err
}

func (s *Service) cancelRecord(w http.ResponseWriter, r *http.Request) (ConsentRecord, string, bool) {
	id := r.PathValue("id")
	sig := linkSig(r)
	rec, ok := s.Consent.bySubscription(id)
	if !s.Consent.sigOK(linkCancel, id, sig) || !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return rec, "", false
	}
	return rec, sig, true
}

func (s *Service) cancelPage(form bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec, sig, ok := s.cancelRecord(w, r)
		if !ok {
			return
		}
		f := s.Consent
		t := f.Texts.Langs[f.lang(rec.Lang)]
		v := f.vals(rec)
		p := page{Lang: f.lang(rec.Lang), Title: t["cancel_title"], Lines: []string{fill(t["cancel_intro"], v)}}
		if !form {
			p.Step, p.Button, p.Next = "button", t["cancel_button"], "/cancel/"+rec.SubscriptionID+"/form?t="+sig
		} else {
			p.Step, p.Button, p.Action, p.Sig = "form", t["cancel_confirm_button"], "/cancel/"+rec.SubscriptionID, sig
			p.Lines = append(p.Lines, fill(t["cancel_form_intro"], v))
		}
		f.render(w, http.StatusOK, p)
	}
}

// cancelSubmit cancels at the provider; the provider's
// subscription.cancelled event then carries the receipt (handle).
func (s *Service) cancelSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form unreadable", http.StatusBadRequest)
		return
	}
	rec, _, ok := s.cancelRecord(w, r)
	if !ok {
		return
	}
	f := s.Consent
	t := f.Texts.Langs[f.lang(rec.Lang)]
	v := f.vals(rec)
	if _, err := s.Provider.CancelSubscription(r.Context(), rec.SubscriptionID); err != nil && !errors.Is(err, ErrNotFound) {
		s.logf("cancel %s: %v", rec.SubscriptionID, err)
		http.Error(w, "the provider did not cancel it: try again", http.StatusBadGateway)
		return
	}
	v["requested_at"] = s.now().UTC().Format("2006-01-02 15:04 MST")
	f.render(w, http.StatusOK, page{Lang: f.lang(rec.Lang), Title: t["cancel_title"], Lines: []string{fill(t["cancel_done"], v)}})
}

// enrich adds the consent flow's parts to an event before it goes to the
// app: the order confirmation on payment.paid, the receipt on
// subscription.cancelled. A copy of each message is kept.
func (s *Service) enrich(ev *Event) error {
	f := s.Consent
	if f == nil {
		return nil
	}
	switch {
	case ev.Type == EventPaymentPaid && ev.Payment != nil:
		rec, ok := f.byPayment(ev.Payment.ID)
		if !ok {
			return nil
		}
		at := ev.Occurred
		if at.IsZero() {
			at = s.now()
		}
		rec, err := f.paid(rec, at)
		if err != nil {
			return err
		}
		ev.Consent, ev.Confirmation = &rec, f.Confirmation(rec)
	case ev.Type == EventSubscriptionCancelled && ev.Subscription != nil:
		rec, ok := f.bySubscription(ev.Subscription.ID)
		if !ok {
			return nil
		}
		at := ev.Occurred
		if at.IsZero() {
			at = s.now()
		}
		ev.Confirmation = f.cancelReceipt(rec, at)
	}
	if ev.Confirmation != nil {
		return f.keepMessage(ev.Provider+"-"+ev.ID, ev.Confirmation)
	}
	return nil
}
