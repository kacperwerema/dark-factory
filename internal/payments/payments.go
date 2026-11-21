// Package payments is the provider-neutral payments kit a project runs
// beside its app (docs/payments.md). It holds the interface every provider
// adapter implements, the neutral types and events, the secret references,
// the config file and the service (Handler) that the app calls over HTTP and
// providers send webhooks to.
//
// The kit is self-contained: the standard library, yaml and one leaf helper
// (internal/jsonfile, the repo's atomic JSON write), so it can move to a
// repository of its own with that one helper copied along.
//
// Card data never reaches the kit: a checkout is always a hosted page on
// the provider's side (PCI SAQ A). No request type has a card field
// (TestNoCardFields).
package payments

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Money is an amount in the currency's minor units (grosze, cents) with its
// ISO 4217 code. There is no float anywhere on the path.
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// Validate refuses a zero or negative amount and a currency that isn't a
// three-letter upper-case ISO 4217 code.
func (m Money) Validate() error {
	if !currencyRe.MatchString(m.Currency) {
		return fmt.Errorf("currency: wants an ISO 4217 code such as PLN or EUR, got %q", m.Currency)
	}
	if m.Amount <= 0 {
		return fmt.Errorf("amount: wants a positive number of minor units (1999 for 19.99 %s), got %d", m.Currency, m.Amount)
	}
	return nil
}

func (m Money) String() string { return fmt.Sprintf("%d %s", m.Amount, m.Currency) }

// Method is a payment method, named the same for every provider.
type Method string

const (
	MethodCard         Method = "card"
	MethodBLIK         Method = "blik"
	MethodPayByLink    Method = "pay_by_link"
	MethodBankTransfer Method = "bank_transfer"
	MethodWallet       Method = "wallet" // Apple Pay, Google Pay
)

// Methods lists every neutral method, in docs order.
var Methods = []Method{MethodCard, MethodBLIK, MethodPayByLink, MethodBankTransfer, MethodWallet}

// Mode is what a checkout opens: one payment, or a subscription whose
// schedule the provider holds.
type Mode string

const (
	ModeOneOff       Mode = "one_off"
	ModeSubscription Mode = "subscription"
)

// Interval is a subscription's billing period.
type Interval string

const (
	IntervalMonth Interval = "month"
	IntervalYear  Interval = "year"
)

// Customer is the buyer as far as the provider needs to know them.
type Customer struct {
	Email   string `json:"email,omitempty"`
	Country string `json:"country,omitempty"` // ISO 3166-1 alpha-2, for VAT
}

// Item is one line of a checkout. Name and price come from the app: the kit
// holds no product catalogue.
type Item struct {
	Name      string `json:"name"`
	Quantity  int    `json:"quantity"`
	UnitPrice Money  `json:"unit_price"`
}

// CheckoutRequest opens a hosted checkout.
type CheckoutRequest struct {
	Mode     Mode     `json:"mode"`
	Items    []Item   `json:"items"`
	Interval Interval `json:"interval,omitempty"` // subscription only
	// Methods the buyer may pick from; empty means every method the
	// provider offers.
	Methods  []Method `json:"methods,omitempty"`
	Customer Customer `json:"customer"`
	// Reference is the app's own order id; every event carries it back.
	Reference  string `json:"reference"`
	SuccessURL string `json:"success_url"`
	CancelURL  string `json:"cancel_url"`
	// IdempotencyKey makes a retried request open the same checkout.
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	// Consent is the waiver checkbox the buyer saw and their answer; the
	// kit's consent: block decides whether it is required.
	Consent *Consent `json:"consent,omitempty"`
}

// Total is the sum of the items; Validate has checked they share a currency.
func (r CheckoutRequest) Total() Money {
	var t Money
	for _, it := range r.Items {
		t.Currency = it.UnitPrice.Currency
		t.Amount += it.UnitPrice.Amount * int64(it.Quantity)
	}
	return t
}

// Validate checks r against what the provider offers: a mode, method or
// currency it doesn't offer is refused, never dropped.
func (r CheckoutRequest) Validate(c Capabilities) error {
	switch r.Mode {
	case ModeOneOff:
		if r.Interval != "" {
			return fmt.Errorf("interval: only a subscription has one")
		}
	case ModeSubscription:
		if r.Interval != IntervalMonth && r.Interval != IntervalYear {
			return fmt.Errorf("interval: a subscription wants month or year, got %q", r.Interval)
		}
	default:
		return fmt.Errorf("mode: wants one_off or subscription, got %q", r.Mode)
	}
	if !slices.Contains(c.Modes, r.Mode) {
		return fmt.Errorf("mode: %s is not offered by this provider (offers %v)", r.Mode, c.Modes)
	}
	if len(r.Items) == 0 {
		return fmt.Errorf("items: wants at least one item")
	}
	cur := r.Items[0].UnitPrice.Currency
	for i, it := range r.Items {
		if strings.TrimSpace(it.Name) == "" {
			return fmt.Errorf("items[%d].name: empty", i)
		}
		if it.Quantity <= 0 {
			return fmt.Errorf("items[%d].quantity: wants 1 or more, got %d", i, it.Quantity)
		}
		if err := it.UnitPrice.Validate(); err != nil {
			return fmt.Errorf("items[%d].unit_price.%v", i, err)
		}
		if it.UnitPrice.Currency != cur {
			return fmt.Errorf("items[%d].unit_price.currency: %s differs from %s; one checkout has one currency", i, it.UnitPrice.Currency, cur)
		}
	}
	if !slices.Contains(c.Currencies, cur) {
		return fmt.Errorf("currency: %s is not offered by this provider (offers %v)", cur, c.Currencies)
	}
	for _, m := range r.Methods {
		if !slices.Contains(Methods, m) {
			return fmt.Errorf("methods: %q is not a method (card, blik, pay_by_link, bank_transfer, wallet)", m)
		}
		if !slices.Contains(c.Methods, m) {
			return fmt.Errorf("methods: %s is not offered by this provider (offers %v)", m, c.Methods)
		}
	}
	if strings.TrimSpace(r.Reference) == "" {
		return fmt.Errorf("reference: wants the app's order id, so events can name it")
	}
	if r.SuccessURL == "" || r.CancelURL == "" {
		return fmt.Errorf("success_url and cancel_url: both are needed for the hosted page to send the buyer back")
	}
	return nil
}

// Checkout is an opened hosted checkout: send the buyer to URL.
type Checkout struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	PaymentID string `json:"payment_id"`
	// SubscriptionID is set for a subscription checkout.
	SubscriptionID string    `json:"subscription_id,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	// WithdrawURL and CancelSubscriptionURL are the buyer's signed links to
	// the kit's two-step pages, set when the consent flow is on.
	WithdrawURL           string `json:"withdraw_url,omitempty"`
	CancelSubscriptionURL string `json:"cancel_subscription_url,omitempty"`
}

// PaymentState is where a payment stands.
type PaymentState string

const (
	PaymentPending           PaymentState = "pending"
	PaymentPaid              PaymentState = "paid"
	PaymentFailed            PaymentState = "failed"
	PaymentRefunded          PaymentState = "refunded"
	PaymentPartiallyRefunded PaymentState = "partially_refunded"
)

// Payment is one payment as the provider knows it.
type Payment struct {
	ID             string       `json:"id"`
	State          PaymentState `json:"state"`
	Amount         Money        `json:"amount"`
	Refunded       int64        `json:"refunded"` // minor units refunded so far
	Method         Method       `json:"method,omitempty"`
	Reference      string       `json:"reference"`
	SubscriptionID string       `json:"subscription_id,omitempty"`
	// FailureReason is the provider's reason, in words, for a failed one.
	FailureReason string `json:"failure_reason,omitempty"`
}

// RefundRequest refunds all of a payment (Amount 0) or part of it.
type RefundRequest struct {
	PaymentID      string `json:"payment_id"`
	Amount         int64  `json:"amount,omitempty"` // minor units; 0 = what is left
	Reason         string `json:"reason,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// Refund is a refund the provider accepted.
type Refund struct {
	ID        string `json:"id"`
	PaymentID string `json:"payment_id"`
	Amount    Money  `json:"amount"`
}

// SubscriptionState is where a subscription stands.
type SubscriptionState string

const (
	SubscriptionActive    SubscriptionState = "active"
	SubscriptionCancelled SubscriptionState = "cancelled"
	SubscriptionPending   SubscriptionState = "pending"
)

// Subscription is one subscription; the provider holds its schedule.
type Subscription struct {
	ID        string            `json:"id"`
	State     SubscriptionState `json:"state"`
	Price     Money             `json:"price"`
	Interval  Interval          `json:"interval"`
	Reference string            `json:"reference"`
}

// EventType is a neutral event the kit sends the app.
type EventType string

const (
	EventPaymentPaid           EventType = "payment.paid"
	EventPaymentFailed         EventType = "payment.failed"
	EventPaymentRefunded       EventType = "payment.refunded"
	EventSubscriptionRenewed   EventType = "subscription.renewed"
	EventSubscriptionCancelled EventType = "subscription.cancelled"
)

// EventTypes lists every neutral event, in docs order.
var EventTypes = []EventType{EventPaymentPaid, EventPaymentFailed, EventPaymentRefunded, EventSubscriptionRenewed, EventSubscriptionCancelled, EventWithdrawalRequested}

// Event is one fact a provider reported, in neutral form. ID is the
// provider's event id: the kit hands each (provider, ID) to the app once.
type Event struct {
	ID           string        `json:"id"`
	Type         EventType     `json:"type"`
	Provider     string        `json:"provider"`
	Occurred     time.Time     `json:"occurred"`
	Reference    string        `json:"reference,omitempty"`
	Payment      *Payment      `json:"payment,omitempty"`
	Subscription *Subscription `json:"subscription,omitempty"`
	Refund       *Refund       `json:"refund,omitempty"`
	// Invoice is set on payment.paid when the invoice hook issued one.
	Invoice *Invoice `json:"invoice,omitempty"`
	// Consent is the checkout's consent record (payment.paid, consent flow on).
	Consent *ConsentRecord `json:"consent,omitempty"`
	// Confirmation is a message for the app to send the buyer: the order
	// confirmation, a withdrawal receipt or a cancellation receipt.
	Confirmation *Message `json:"confirmation,omitempty"`
	// Withdrawal is set on withdrawal.requested.
	Withdrawal *Withdrawal `json:"withdrawal,omitempty"`
}

// Capabilities is what a provider offers; a checkout asking for more is
// refused (CheckoutRequest.Validate).
type Capabilities struct {
	Methods    []Method `json:"methods"`
	Modes      []Mode   `json:"modes"`
	Currencies []string `json:"currencies"`
	Refunds    bool     `json:"refunds"`
	// MerchantOfRecord: the provider is the seller and issues the invoice
	// itself (Paddle); the invoice hook is then usually none.
	MerchantOfRecord bool `json:"merchant_of_record"`
}

// Provider is what every provider adapter implements. A real provider is one
// package that implements it, registers itself (Register) and passes
// paymentstest.Contract.
type Provider interface {
	// Name is the adapter's registered name (the config's provider:).
	Name() string
	Capabilities() Capabilities
	// CreateCheckout opens a hosted checkout and returns its URL. The same
	// IdempotencyKey returns the same checkout.
	CreateCheckout(ctx context.Context, r CheckoutRequest) (Checkout, error)
	// ParseWebhook verifies the request's signature before anything else
	// and returns the neutral events it carries. A bad, missing or stale
	// signature is ErrSignature.
	ParseWebhook(h http.Header, body []byte) ([]Event, error)
	// Payment looks up a payment's current state.
	Payment(ctx context.Context, id string) (Payment, error)
	// Refund refunds all or part of a paid payment; more than what is left
	// is refused.
	Refund(ctx context.Context, r RefundRequest) (Refund, error)
	// CancelSubscription stops a subscription at the provider.
	CancelSubscription(ctx context.Context, id string) (Subscription, error)
}

// ErrSignature is a webhook whose signature is bad, missing or stale.
var ErrSignature = fmt.Errorf("webhook signature: bad, missing or stale")

// ErrNotFound is a payment or subscription the provider doesn't know.
var ErrNotFound = fmt.Errorf("not found")
