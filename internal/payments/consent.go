package payments

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
)

// The consent flow (docs/payments.md#consent-withdrawal-and-cancel): the
// 14-day withdrawal waiver recorded with each checkout, the trader's
// confirmation message on payment.paid, the two-step withdrawal button
// (Directive 2023/2673, art. 11a) and a two-step subscription cancel. The
// kit builds the messages; the app sends them (the kit has no mailer), and
// the kit never refunds by itself.

// ConsentConfig is payments.yaml's consent: block; absent, the flow is off.
type ConsentConfig struct {
	// Required: a checkout without a consent record is refused.
	Required bool `yaml:"required"`
	// Texts is the project's reviewed pack, relative to payments.yaml; empty
	// means the kit's built-in placeholder pack.
	Texts string `yaml:"texts"`
	// Lang is the default language (a key of the pack's langs); pl by default.
	Lang string `yaml:"lang"`
	// WithdrawalDays is the withdrawal period; 14 by default, never less.
	WithdrawalDays int `yaml:"withdrawal_days"`
}

// ConsentTexts is a text pack: every label, page line and message the flow
// shows, per language.
type ConsentTexts struct {
	Version string `yaml:"version" json:"version"`
	// Placeholder marks wording that no lawyer has reviewed: mode: live
	// refuses it, and every message built from it says so.
	Placeholder bool                         `yaml:"placeholder" json:"placeholder"`
	ReviewedBy  string                       `yaml:"reviewed_by" json:"reviewed_by,omitempty"`
	ReviewedOn  string                       `yaml:"reviewed_on" json:"reviewed_on,omitempty"`
	Langs       map[string]map[string]string `yaml:"langs" json:"-"`
}

// TextKeys are the keys every language of a pack has.
var TextKeys = []string{
	"waiver_checkbox",
	"confirmation_subject", "confirmation_body", "confirmation_waived", "confirmation_kept", "confirmation_subscription",
	"withdraw_title", "withdraw_intro", "withdraw_button", "withdraw_form_intro", "withdraw_confirm_button",
	"withdraw_done", "withdraw_expired", "withdraw_receipt_subject", "withdraw_receipt_body",
	"cancel_title", "cancel_intro", "cancel_button", "cancel_form_intro", "cancel_confirm_button",
	"cancel_done", "cancel_receipt_subject", "cancel_receipt_body",
	"field_name", "field_email", "field_contract",
}

//go:embed consent_texts.yaml
var builtinTexts []byte

// BuiltinTexts is the kit's own pack: placeholder wording, pending the
// owner's lawyer's review.
func BuiltinTexts() ConsentTexts {
	t, err := ParseConsentTexts(builtinTexts)
	if err != nil {
		panic("payments: built-in consent texts: " + err.Error())
	}
	return t
}

// ParseConsentTexts reads a pack and checks every language has every key.
func ParseConsentTexts(data []byte) (ConsentTexts, error) {
	var t ConsentTexts
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&t); err != nil {
		return t, err
	}
	if strings.TrimSpace(t.Version) == "" {
		return t, fmt.Errorf("version: missing; the checkout's consent.text_version must name it")
	}
	if len(t.Langs) == 0 {
		return t, fmt.Errorf("langs: wants at least one language")
	}
	if !t.Placeholder && (strings.TrimSpace(t.ReviewedBy) == "" || strings.TrimSpace(t.ReviewedOn) == "") {
		return t, fmt.Errorf("placeholder: false wants reviewed_by and reviewed_on (who reviewed the wording, and when)")
	}
	for _, l := range sortedLangs(t.Langs) {
		for _, k := range TextKeys {
			if strings.TrimSpace(t.Langs[l][k]) == "" {
				return t, fmt.Errorf("langs.%s.%s: missing", l, k)
			}
		}
		for k := range t.Langs[l] {
			if !slices.Contains(TextKeys, k) {
				return t, fmt.Errorf("langs.%s.%s: unknown key", l, k)
			}
		}
	}
	return t, nil
}

func sortedLangs(m map[string]map[string]string) []string {
	var ls []string
	for l := range m {
		ls = append(ls, l)
	}
	sort.Strings(ls)
	return ls
}

// LoadTexts is the pack the block names: the project's file, or the
// built-in placeholder one.
func (cc ConsentConfig) LoadTexts(dir string) (ConsentTexts, error) {
	if cc.Texts == "" {
		return BuiltinTexts(), nil
	}
	p := cc.Texts
	if !filepath.IsAbs(p) && dir != "" {
		p = filepath.Join(dir, p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ConsentTexts{}, err
	}
	t, err := ParseConsentTexts(data)
	if err != nil {
		return t, fmt.Errorf("%s: %w", cc.Texts, err)
	}
	return t, nil
}

// Consent is what the app sends with a checkout: whether the buyer ticked
// the waiver checkbox, and which pack version (and language) they saw.
type Consent struct {
	Waiver      bool   `json:"waiver"`
	TextVersion string `json:"text_version"`
	Lang        string `json:"lang,omitempty"`
}

// ConsentRecord is what the kit keeps per checkout: the exact checkbox text
// the buyer saw, their answer and when; PaidAt starts the withdrawal period.
type ConsentRecord struct {
	CheckoutID     string     `json:"checkout_id"`
	PaymentID      string     `json:"payment_id"`
	SubscriptionID string     `json:"subscription_id,omitempty"`
	Reference      string     `json:"reference"`
	Email          string     `json:"email,omitempty"`
	Amount         Money      `json:"amount"`
	Lang           string     `json:"lang"`
	Waiver         bool       `json:"waiver"`
	TextVersion    string     `json:"text_version"`
	Text           string     `json:"text"`
	Placeholder    bool       `json:"placeholder"`
	RecordedAt     time.Time  `json:"recorded_at"`
	PaidAt         *time.Time `json:"paid_at,omitempty"`
	Deadline       *time.Time `json:"deadline,omitempty"`
}

// Message is a mail the app sends the buyer: the kit builds it, the app's
// mailer sends it.
type Message struct {
	Kind    string `json:"kind"` // order_confirmation | withdrawal_receipt | cancellation_receipt
	To      string `json:"to,omitempty"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	Lang    string `json:"lang"`
	// Placeholder: built from wording no lawyer has reviewed.
	Placeholder bool `json:"placeholder"`
}

// Withdrawal is a buyer's withdrawal statement, as the second step of the
// withdrawal page took it.
type Withdrawal struct {
	PaymentID   string    `json:"payment_id"`
	Reference   string    `json:"reference"`
	RequestedAt time.Time `json:"requested_at"`
	Name        string    `json:"name"`
	Email       string    `json:"email"`
	Deadline    time.Time `json:"deadline"`
	// Waiver: the buyer ticked the waiver at checkout; the app (and its
	// lawyer's rule) decides what that means for this statement.
	Waiver bool `json:"waiver"`
}

// EventWithdrawalRequested is sent when a buyer confirmed a withdrawal on
// the kit's page. The kit sends it itself (the provider has no such fact).
const EventWithdrawalRequested EventType = "withdrawal.requested"

// ConsentFlow is the running flow: the pack, the period and where records go.
type ConsentFlow struct {
	Texts    ConsentTexts
	Lang     string
	Days     int
	Required bool
	// Dir is the state dir: consents/, messages/ and withdrawals/ go under it.
	Dir string
	// Key signs the withdrawal and cancel links.
	Key []byte
	// PublicURL is where buyers reach the kit's pages.
	PublicURL string

	mu sync.Mutex
}

// NewConsentFlow builds the flow a config's consent: block describes.
func NewConsentFlow(cc ConsentConfig, dir, stateDir, publicURL string, key []byte) (*ConsentFlow, error) {
	t, err := cc.LoadTexts(dir)
	if err != nil {
		return nil, err
	}
	cc.defaults()
	if _, ok := t.Langs[cc.Lang]; !ok {
		return nil, fmt.Errorf("consent.lang: %q is not in the texts' langs (%s)", cc.Lang, strings.Join(sortedLangs(t.Langs), ", "))
	}
	return &ConsentFlow{Texts: t, Lang: cc.Lang, Days: cc.WithdrawalDays, Required: cc.Required, Dir: stateDir, Key: key, PublicURL: strings.TrimRight(publicURL, "/")}, nil
}

func (cc *ConsentConfig) defaults() {
	if cc.Lang == "" {
		cc.Lang = "pl"
	}
	if cc.WithdrawalDays == 0 {
		cc.WithdrawalDays = 14
	}
}

// problems lists what is wrong with the block; live adds the placeholder rule.
func (cc ConsentConfig) problems(dir string, live bool) []Problem {
	var ps []Problem
	add := func(f, format string, a ...any) { ps = append(ps, Problem{f, fmt.Sprintf(format, a...)}) }
	cc.defaults()
	if cc.WithdrawalDays < 14 {
		add("consent.withdrawal_days", "wants 14 or more (the EU minimum), got %d", cc.WithdrawalDays)
	}
	t, err := cc.LoadTexts(dir)
	if err != nil {
		add("consent.texts", "%v", err)
		return ps
	}
	if _, ok := t.Langs[cc.Lang]; !ok {
		add("consent.lang", "%q is not in the texts' langs (%s)", cc.Lang, strings.Join(sortedLangs(t.Langs), ", "))
	}
	if live && t.Placeholder {
		add("consent.texts", "%s is a placeholder pending the lawyer's review (placeholder: true): live wants a reviewed pack named in texts:", t.Version)
	}
	return ps
}

// TextsFor is the public part of the pack for one language: what the app's
// checkout page shows.
func (f *ConsentFlow) TextsFor(lang string) map[string]any {
	l := f.lang(lang)
	s := f.Texts.Langs[l]
	return map[string]any{
		"version": f.Texts.Version, "placeholder": f.Texts.Placeholder, "lang": l,
		"waiver_checkbox": s["waiver_checkbox"], "withdraw_button": s["withdraw_button"], "cancel_button": s["cancel_button"],
		"withdrawal_days": f.Days, "langs": sortedLangs(f.Texts.Langs),
	}
}

func (f *ConsentFlow) lang(l string) string {
	if _, ok := f.Texts.Langs[l]; ok {
		return l
	}
	return f.Lang
}

// check is the checkout's consent against the flow; "" when fine.
func (f *ConsentFlow) check(c *Consent) string {
	if c == nil {
		if f.Required {
			return "consent: required (payments.yaml consent: required): send {waiver, text_version, lang} with the checkbox the buyer saw; GET /v1/consent/texts gives the texts and their version"
		}
		return ""
	}
	if c.TextVersion != f.Texts.Version {
		return fmt.Sprintf("consent.text_version: %q is not the current texts (%s): show the buyer GET /v1/consent/texts again", c.TextVersion, f.Texts.Version)
	}
	if c.Lang != "" {
		if _, ok := f.Texts.Langs[c.Lang]; !ok {
			return fmt.Sprintf("consent.lang: %q is not in the texts' langs (%s)", c.Lang, strings.Join(sortedLangs(f.Texts.Langs), ", "))
		}
	}
	return ""
}

// Link kinds.
const (
	linkWithdraw = "withdraw"
	linkCancel   = "cancel"
)

func (f *ConsentFlow) sig(kind, id string) string {
	m := hmac.New(sha256.New, f.Key)
	m.Write([]byte(kind + "\x00" + id))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func (f *ConsentFlow) sigOK(kind, id, got string) bool {
	return len(f.Key) > 0 && hmac.Equal([]byte(got), []byte(f.sig(kind, id)))
}

// WithdrawURL is the signed link to a payment's withdrawal page.
func (f *ConsentFlow) WithdrawURL(paymentID string) string {
	return f.PublicURL + "/withdraw/" + paymentID + "?t=" + f.sig(linkWithdraw, paymentID)
}

// CancelURL is the signed link to a subscription's cancel page.
func (f *ConsentFlow) CancelURL(subID string) string {
	return f.PublicURL + "/cancel/" + subID + "?t=" + f.sig(linkCancel, subID)
}

func (f *ConsentFlow) path(sub, prefix, id string) string {
	return filepath.Join(f.Dir, sub, prefix+unsafeName.ReplaceAllString(id, "_")+".json")
}

func readJSONFile(path string, v any) bool {
	data, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(data, v) == nil
}

func (f *ConsentFlow) writeFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return jsonfile.Write(path, v, 0o600)
}

// record stores a new checkout's consent, under its payment and (for a
// subscription) its subscription.
func (f *ConsentFlow) record(r CheckoutRequest, co Checkout, now time.Time) (ConsentRecord, error) {
	lang := f.lang(r.Consent.Lang)
	rec := ConsentRecord{
		CheckoutID: co.ID, PaymentID: co.PaymentID, SubscriptionID: co.SubscriptionID,
		Reference: r.Reference, Email: r.Customer.Email, Amount: r.Total(), Lang: lang,
		Waiver: r.Consent.Waiver, TextVersion: f.Texts.Version, Text: f.Texts.Langs[lang]["waiver_checkbox"],
		Placeholder: f.Texts.Placeholder, RecordedAt: now.UTC(),
	}
	return rec, f.save(rec)
}

func (f *ConsentFlow) save(rec ConsentRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.writeFile(f.path("consents", "pay-", rec.PaymentID), rec); err != nil {
		return err
	}
	if rec.SubscriptionID != "" {
		return f.writeFile(f.path("consents", "sub-", rec.SubscriptionID), rec)
	}
	return nil
}

func (f *ConsentFlow) byPayment(id string) (ConsentRecord, bool) {
	var rec ConsentRecord
	f.mu.Lock()
	defer f.mu.Unlock()
	return rec, readJSONFile(f.path("consents", "pay-", id), &rec)
}

func (f *ConsentFlow) bySubscription(id string) (ConsentRecord, bool) {
	var rec ConsentRecord
	f.mu.Lock()
	defer f.mu.Unlock()
	return rec, readJSONFile(f.path("consents", "sub-", id), &rec)
}

// paid starts the withdrawal period on the first payment.paid; a repeat
// keeps the first time.
func (f *ConsentFlow) paid(rec ConsentRecord, at time.Time) (ConsentRecord, error) {
	if rec.PaidAt != nil {
		return rec, nil
	}
	p := at.UTC()
	d := p.AddDate(0, 0, f.Days) // calendar days from the payment
	rec.PaidAt, rec.Deadline = &p, &d
	return rec, f.save(rec)
}

// fill replaces the {placeholders} in s.
func fill(s string, vals map[string]string) string {
	var pairs []string
	for k, v := range vals {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

// formatMoney shows minor units with two decimals (PLN, EUR).
func formatMoney(m Money) string {
	return fmt.Sprintf("%d.%02d %s", m.Amount/100, m.Amount%100, m.Currency)
}

func day(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func (f *ConsentFlow) vals(rec ConsentRecord) map[string]string {
	v := map[string]string{
		"reference": rec.Reference, "amount": formatMoney(rec.Amount), "date": day(rec.PaidAt),
		"deadline": day(rec.Deadline), "days": fmt.Sprint(f.Days), "withdraw_url": f.WithdrawURL(rec.PaymentID),
		"email": rec.Email,
	}
	if rec.SubscriptionID != "" {
		v["cancel_url"] = f.CancelURL(rec.SubscriptionID)
	}
	return v
}

// Confirmation is the order confirmation for a paid checkout: the waiver
// confirmation, or the withdrawal right with its deadline, and the link.
func (f *ConsentFlow) Confirmation(rec ConsentRecord) *Message {
	s := f.Texts.Langs[f.lang(rec.Lang)]
	v := f.vals(rec)
	part := s["confirmation_kept"]
	if rec.Waiver {
		part = s["confirmation_waived"]
	}
	v["waiver_part"] = fill(part, v)
	text := fill(s["confirmation_body"], v)
	if rec.SubscriptionID != "" {
		text += "\n\n" + fill(s["confirmation_subscription"], v)
	}
	return &Message{Kind: "order_confirmation", To: rec.Email, Subject: fill(s["confirmation_subject"], v), Text: text, Lang: f.lang(rec.Lang), Placeholder: f.Texts.Placeholder}
}

func (f *ConsentFlow) withdrawalReceipt(rec ConsentRecord, w Withdrawal) *Message {
	s := f.Texts.Langs[f.lang(rec.Lang)]
	v := f.vals(rec)
	v["name"], v["email"], v["requested_at"] = w.Name, w.Email, w.RequestedAt.UTC().Format("2006-01-02 15:04 MST")
	return &Message{Kind: "withdrawal_receipt", To: w.Email, Subject: fill(s["withdraw_receipt_subject"], v), Text: fill(s["withdraw_receipt_body"], v), Lang: f.lang(rec.Lang), Placeholder: f.Texts.Placeholder}
}

func (f *ConsentFlow) cancelReceipt(rec ConsentRecord, at time.Time) *Message {
	s := f.Texts.Langs[f.lang(rec.Lang)]
	v := f.vals(rec)
	v["requested_at"] = at.UTC().Format("2006-01-02 15:04 MST")
	return &Message{Kind: "cancellation_receipt", To: rec.Email, Subject: fill(s["cancel_receipt_subject"], v), Text: fill(s["cancel_receipt_body"], v), Lang: f.lang(rec.Lang), Placeholder: f.Texts.Placeholder}
}

// keepMessage writes a copy of each message the kit hands the app.
func (f *ConsentFlow) keepMessage(eventID string, m *Message) error {
	return f.writeFile(f.path("messages", "", eventID), m)
}

// withdrawalFile is a withdrawal on disk; Delivered once the app took it.
type withdrawalFile struct {
	Withdrawal
	Delivered bool `json:"delivered"`
}

func (f *ConsentFlow) withdrawal(paymentID string) (withdrawalFile, bool) {
	var w withdrawalFile
	f.mu.Lock()
	defer f.mu.Unlock()
	return w, readJSONFile(f.path("withdrawals", "", paymentID), &w)
}

func (f *ConsentFlow) saveWithdrawal(w withdrawalFile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writeFile(f.path("withdrawals", "", w.PaymentID), w)
}
