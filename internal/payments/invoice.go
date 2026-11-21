package payments

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
)

// InvoiceRequest is what the invoice hook gets for each paid payment (a
// one-off payment.paid or a subscription.renewed).
type InvoiceRequest struct {
	EventID  string    `json:"event_id"`
	Provider string    `json:"provider"`
	Payment  Payment   `json:"payment"`
	IssuedAt time.Time `json:"issued_at"`
	// MerchantOfRecord: the provider is the seller and invoices itself.
	MerchantOfRecord bool `json:"merchant_of_record"`
}

// Invoice is what the hook issued. Ref is where it went: a file, a KSeF
// number, the merchant of record's invoice id.
type Invoice struct {
	ID        string `json:"id"`
	PaymentID string `json:"payment_id"`
	Issuer    string `json:"issuer"`
	Ref       string `json:"ref,omitempty"`
}

// Invoicer is the invoice hook: where our own PDF invoice or a KSeF FA(3)
// invoice attaches later. It returns nil when it issues nothing. The same
// payment issued twice returns the same invoice.
type Invoicer interface {
	Issue(ctx context.Context, r InvoiceRequest) (*Invoice, error)
}

// NoInvoice issues nothing: a merchant of record or the provider invoices.
type NoInvoice struct{}

func (NoInvoice) Issue(context.Context, InvoiceRequest) (*Invoice, error) { return nil, nil }

// RecordInvoice writes each request as <dir>/<payment id>.json: the stand-in
// for our own invoicing, so a test or a local run sees what an invoice
// would be made from.
type RecordInvoice struct{ Dir string }

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func (r RecordInvoice) Issue(_ context.Context, req InvoiceRequest) (*Invoice, error) {
	if req.Payment.ID == "" {
		return nil, fmt.Errorf("invoice: payment has no id")
	}
	name := unsafeName.ReplaceAllString(req.Payment.ID, "_") + ".json"
	path := filepath.Join(r.Dir, name)
	inv := &Invoice{ID: "inv_" + req.Payment.ID, PaymentID: req.Payment.ID, Issuer: "record", Ref: path}
	if _, err := os.Stat(path); err == nil {
		return inv, nil
	}
	if err := os.MkdirAll(r.Dir, 0o700); err != nil {
		return nil, err
	}
	if err := jsonfile.Write(path, req, 0o600); err != nil {
		return nil, err
	}
	return inv, nil
}
