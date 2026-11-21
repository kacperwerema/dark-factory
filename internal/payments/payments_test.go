package payments

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var allCaps = Capabilities{
	Methods:    Methods,
	Modes:      []Mode{ModeOneOff, ModeSubscription},
	Currencies: []string{"PLN", "EUR"},
}

func okRequest() CheckoutRequest {
	return CheckoutRequest{
		Mode:       ModeOneOff,
		Items:      []Item{{Name: "Plan", Quantity: 2, UnitPrice: Money{Amount: 1999, Currency: "PLN"}}},
		Reference:  "order-1",
		SuccessURL: "https://app.example/ok",
		CancelURL:  "https://app.example/no",
	}
}

func TestCheckoutRequestValidate(t *testing.T) {
	if err := okRequest().Validate(allCaps); err != nil {
		t.Fatalf("a good request: %v", err)
	}
	if got := okRequest().Total(); got != (Money{Amount: 3998, Currency: "PLN"}) {
		t.Errorf("Total = %v, want 3998 PLN (minor units)", got)
	}
	cases := []struct {
		name string
		edit func(*CheckoutRequest)
		caps Capabilities
		want string
	}{
		{"zero amount", func(r *CheckoutRequest) { r.Items[0].UnitPrice.Amount = 0 }, allCaps, "positive number of minor units"},
		{"negative amount", func(r *CheckoutRequest) { r.Items[0].UnitPrice.Amount = -5 }, allCaps, "positive"},
		{"lower-case currency", func(r *CheckoutRequest) { r.Items[0].UnitPrice.Currency = "pln" }, allCaps, "ISO 4217"},
		{"currency not offered", func(r *CheckoutRequest) { r.Items[0].UnitPrice.Currency = "USD" }, allCaps, "USD is not offered"},
		{"two currencies", func(r *CheckoutRequest) {
			r.Items = append(r.Items, Item{Name: "x", Quantity: 1, UnitPrice: Money{Amount: 1, Currency: "EUR"}})
		}, allCaps, "one checkout has one currency"},
		{"unknown method", func(r *CheckoutRequest) { r.Methods = []Method{"cheque"} }, allCaps, "not a method"},
		{"method not offered", func(r *CheckoutRequest) { r.Methods = []Method{MethodBLIK} },
			Capabilities{Methods: []Method{MethodCard}, Modes: allCaps.Modes, Currencies: allCaps.Currencies}, "blik is not offered"},
		{"subscription not offered", func(r *CheckoutRequest) { r.Mode, r.Interval = ModeSubscription, IntervalMonth },
			Capabilities{Methods: Methods, Modes: []Mode{ModeOneOff}, Currencies: allCaps.Currencies}, "subscription is not offered"},
		{"subscription without interval", func(r *CheckoutRequest) { r.Mode = ModeSubscription }, allCaps, "month or year"},
		{"one-off with interval", func(r *CheckoutRequest) { r.Interval = IntervalYear }, allCaps, "only a subscription"},
		{"no mode", func(r *CheckoutRequest) { r.Mode = "" }, allCaps, "one_off or subscription"},
		{"no items", func(r *CheckoutRequest) { r.Items = nil }, allCaps, "at least one item"},
		{"zero quantity", func(r *CheckoutRequest) { r.Items[0].Quantity = 0 }, allCaps, "quantity"},
		{"no reference", func(r *CheckoutRequest) { r.Reference = " " }, allCaps, "order id"},
		{"no return URLs", func(r *CheckoutRequest) { r.CancelURL = "" }, allCaps, "cancel_url"},
	}
	for _, c := range cases {
		r := okRequest()
		c.edit(&r)
		err := r.Validate(c.caps)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", c.name, err, c.want)
		}
	}
}

// TestNoCardFields: hosted checkout only. No type the app or a provider
// hands the kit has a field that could hold card data, and the API refuses
// unknown fields (readJSON), so none can be smuggled in either.
func TestNoCardFields(t *testing.T) {
	banned := []string{"card_number", "cardnumber", "pan", "cvc", "cvv", "cvv2", "expiry", "exp_month", "exp_year", "track", "pin"}
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type, string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name := strings.ToLower(strings.Split(f.Tag.Get("json"), ",")[0])
			if name == "" {
				name = strings.ToLower(f.Name)
			}
			for _, b := range banned {
				if name == b || strings.Contains(name, "card_") {
					t.Errorf("%s.%s (json %q) looks like card data: the kit takes hosted checkout only", path, f.Name, name)
				}
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	for _, v := range []any{CheckoutRequest{}, Checkout{}, Payment{}, RefundRequest{}, Refund{}, Subscription{}, Event{}, InvoiceRequest{}, Invoice{}} {
		walk(reflect.TypeOf(v), reflect.TypeOf(v).Name())
	}
	if len(seen) < 10 {
		t.Fatalf("walked only %d types: the walk is broken", len(seen))
	}
}

func TestSignVerify(t *testing.T) {
	secret := []byte("s3")
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"id":"evt_1"}`)
	h := Sign(secret, now, body)
	if err := Verify(secret, h, body, now.Add(time.Minute)); err != nil {
		t.Fatalf("own signature: %v", err)
	}
	bad := map[string]struct {
		secret []byte
		header string
		body   []byte
		now    time.Time
	}{
		"other secret": {[]byte("x"), h, body, now},
		"other body":   {secret, h, []byte(`{"id":"evt_2"}`), now},
		"too old":      {secret, h, body, now.Add(Tolerance + time.Second)},
		"from future":  {secret, h, body, now.Add(-Tolerance - time.Second)},
		"empty":        {secret, "", body, now},
		"no v1":        {secret, "t=1800000000", body, now},
		"not hex":      {secret, "t=1800000000,v1=zz", body, now},
		"no secret":    {nil, h, body, now},
	}
	for name, c := range bad {
		if err := Verify(c.secret, c.header, c.body, c.now); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: %v, want ErrSignature", name, err)
		}
	}
}

func TestFileStoreSurvivesARestartAndATornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handled.jsonl")
	s, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkHandled("standin/evt_1"); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"key":"standin/evt_2"`) // a crash mid-write
	f.Close()
	s2, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Handled("standin/evt_1") || s2.Handled("standin/evt_2") {
		t.Errorf("after a restart: evt_1 %v (want true), torn evt_2 %v (want false)", s2.Handled("standin/evt_1"), s2.Handled("standin/evt_2"))
	}
}

func TestRecordInvoiceIsIdempotent(t *testing.T) {
	r := RecordInvoice{Dir: t.TempDir()}
	req := InvoiceRequest{EventID: "evt_1", Payment: Payment{ID: "pay/../1", Amount: Money{Amount: 100, Currency: "PLN"}}}
	a, err := r.Issue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := r.Issue(context.Background(), req)
	if a.ID != b.ID || a.Ref != b.Ref || filepath.Dir(a.Ref) != r.Dir {
		t.Errorf("issued twice: %+v vs %+v (or outside %s)", a, b, r.Dir)
	}
	if inv, _ := (NoInvoice{}).Issue(context.Background(), req); inv != nil {
		t.Errorf("invoice: none issued %+v", inv)
	}
}

func TestParseSecretRef(t *testing.T) {
	for raw, want := range map[string]string{"env:PAY_TOKEN": "env:PAY_TOKEN", "keychain:orchard-pay": "keychain:orchard-pay", "keychain:svc/acct": "keychain:svc/acct"} {
		r, err := ParseSecretRef(raw)
		if err != nil || r.String() != want {
			t.Errorf("%s: %v %v", raw, r, err)
		}
	}
	for _, raw := range []string{"plain-s3cr3t-value", "", "env:1BAD", "keychain:", "vault:x"} {
		_, err := ParseSecretRef(raw)
		if err == nil {
			t.Errorf("%q parsed as a reference", raw)
			continue
		}
		if raw == "plain-s3cr3t-value" && strings.Contains(err.Error(), raw) {
			t.Errorf("the error repeats the value %q: %v", raw, err)
		}
	}
}
