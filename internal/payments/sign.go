package payments

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader carries the kit's signature on each event it sends the
// app: "t=<unix seconds>,v1=<hex HMAC-SHA256(events_secret, "<t>.<body>")>".
const SignatureHeader = "Payments-Kit-Signature"

// EventIDHeader names the event on each delivery, so an app can log it
// before it parses the body.
const EventIDHeader = "Payments-Kit-Event-Id"

// Tolerance is how old a signed timestamp may be (either way) before the
// signature counts as stale: a captured request replayed later is refused.
const Tolerance = 5 * time.Minute

// Sign returns the signature header value for body at t.
func Sign(secret []byte, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac(secret, ts, body))
}

func mac(secret []byte, ts string, body []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return m.Sum(nil)
}

// Verify checks a header made by Sign: the HMAC in constant time, and the
// timestamp within Tolerance of now. Any failure wraps ErrSignature.
// An app written in Go calls it on each delivery with its events_secret;
// docs/payments.md has the same check in TypeScript.
func Verify(secret []byte, header string, body []byte, now time.Time) error {
	if len(secret) == 0 {
		return fmt.Errorf("%w: no secret to check against", ErrSignature)
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == "" || len(sigs) == 0 {
		return fmt.Errorf("%w: header wants t=<unix>,v1=<hex>", ErrSignature)
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: t is not a unix time", ErrSignature)
	}
	if d := now.Sub(time.Unix(sec, 0)); d > Tolerance || d < -Tolerance {
		return fmt.Errorf("%w: timestamp is %s away from now (allowed %s)", ErrSignature, d.Round(time.Second), Tolerance)
	}
	want := mac(secret, ts, body)
	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return fmt.Errorf("%w: no v1 matches", ErrSignature)
}

// linkKey derives the key that signs the buyer's withdrawal and cancel
// links from events_secret, so the links need no secret of their own and
// a link's signature is never an event signature.
func linkKey(eventsSecret []byte) []byte {
	m := hmac.New(sha256.New, eventsSecret)
	m.Write([]byte("payments-kit buyer links"))
	return m.Sum(nil)
}
