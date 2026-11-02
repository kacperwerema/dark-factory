package webpush

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// RFC 8291 Appendix A: the worked example.
const (
	rfcPlain   = "When I grow up, I want to be a watermelon"
	rfcASPriv  = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcASPub   = "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"
	rfcUAPriv  = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcUAPub   = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcAuth    = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcSalt    = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcMessage = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := decodeB64(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncryptMatchesRFC8291Vector(t *testing.T) {
	// The vector's keys agree with each other.
	ua, err := ecdh.P256().NewPrivateKey(mustB64(t, rfcUAPriv))
	if err != nil {
		t.Fatal(err)
	}
	if got := b64.EncodeToString(ua.PublicKey().Bytes()); got != rfcUAPub {
		t.Fatalf("UA public key from the vector's private key = %s", got)
	}
	as, err := ecdh.P256().NewPrivateKey(mustB64(t, rfcASPriv))
	if err != nil {
		t.Fatal(err)
	}
	if got := b64.EncodeToString(as.PublicKey().Bytes()); got != rfcASPub {
		t.Fatalf("AS public key from the vector's private key = %s", got)
	}
	sub := Subscription{Endpoint: "https://push.example.net/x", P256dh: rfcUAPub, Auth: rfcAuth}
	got, err := encrypt(sub, []byte(rfcPlain), as, mustB64(t, rfcSalt))
	if err != nil {
		t.Fatal(err)
	}
	if b64.EncodeToString(got) != rfcMessage {
		t.Fatalf("message differs from RFC 8291 Appendix A:\n got %s\nwant %s", b64.EncodeToString(got), rfcMessage)
	}
	plain, err := Decrypt(ua, mustB64(t, rfcAuth), got)
	if err != nil || string(plain) != rfcPlain {
		t.Fatalf("Decrypt = %q, %v", plain, err)
	}
}

func TestEncryptRoundTripAndLimit(t *testing.T) {
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := bytes.Repeat([]byte{7}, 16)
	sub := Subscription{Endpoint: "https://e", P256dh: b64.EncodeToString(ua.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
	if err := sub.Validate(); err != nil {
		t.Fatal(err)
	}
	msg, err := Encrypt(sub, []byte(`{"title":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := Decrypt(ua, auth, msg); err != nil || string(plain) != `{"title":"x"}` {
		t.Fatalf("round trip = %q, %v", plain, err)
	}
	if _, err := Encrypt(sub, make([]byte, MaxPlaintext+1)); err == nil {
		t.Fatal("a payload over one record was encrypted")
	}
	if msg, err := Encrypt(sub, make([]byte, MaxPlaintext)); err != nil || len(msg) > recordSize {
		t.Fatalf("largest payload: %d bytes, %v", len(msg), err)
	}
}

func TestVAPIDVerifies(t *testing.T) {
	k, err := NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	h, err := k.VAPID("https://web.push.apple.com/QGf/abc", "mailto:o@example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	c, key, err := VerifyVAPID(h)
	if err != nil {
		t.Fatal(err)
	}
	if key != k.PublicKey() || c.Aud != "https://web.push.apple.com" || c.Sub != "mailto:o@example.com" || c.Exp != now.Add(12*time.Hour).Unix() {
		t.Fatalf("claims %+v key %s", c, key)
	}
	other, _ := NewKeys()
	forged := strings.Replace(h, "k="+k.PublicKey(), "k="+other.PublicKey(), 1)
	if _, _, err := VerifyVAPID(forged); err == nil {
		t.Fatal("a token verified against another key")
	}
}

func TestLoadOrCreateKeepsKeys0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "push")
	k1, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k1.PublicKey() != k2.PublicKey() {
		t.Fatal("second load made new keys")
	}
	fi, err := os.Stat(KeysFile(dir))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("vapid.json mode %v, %v", fi.Mode(), err)
	}
	di, _ := os.Stat(dir)
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("push dir mode %v", di.Mode())
	}
}

func TestAllowedEndpoint(t *testing.T) {
	for u, want := range map[string]bool{
		"https://web.push.apple.com/abc":                   true,
		"https://fcm.googleapis.com/fcm/send/abc":          true,
		"https://updates.push.services.mozilla.com/wpush/": true,
		"https://wns2-by3p.notify.windows.com/w/?token=x":  true,
		"http://web.push.apple.com/abc":                    false,
		"https://evilpush.apple.com/abc":                   false,
		"https://web.push.apple.com.example/abc":           false,
		"https://web.push.apple.com:8443/abc":              false,
		"https://user@web.push.apple.com/abc":              false,
		"https://127.0.0.1/abc":                            false,
		"not a url":                                        false,
	} {
		if got := AllowedEndpoint(u); got != want {
			t.Errorf("AllowedEndpoint(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestTopicFitsRFC8030(t *testing.T) {
	if n := len(TopicFor(strings.Repeat("k", 500))); n != 22 {
		t.Fatalf("topic is %d characters", n)
	}
}

func TestSendOutcomes(t *testing.T) {
	k, _ := NewKeys()
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := bytes.Repeat([]byte{1}, 16)
	var status int
	var got []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status == http.StatusTemporaryRedirect {
			http.Redirect(w, r, "https://127.0.0.1:1/elsewhere", status)
			return
		}
		if _, _, err := VerifyVAPID(r.Header.Get("Authorization")); err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
		body := new(bytes.Buffer)
		body.ReadFrom(r.Body)
		got, _ = Decrypt(ua, auth, body.Bytes())
		w.WriteHeader(status)
	}))
	defer srv.Close()
	old := AllowedEndpoint
	AllowedEndpoint = func(u string) bool { return strings.HasPrefix(u, srv.URL) }
	defer func() { AllowedEndpoint = old }()
	client := srv.Client()
	client.CheckRedirect = Client().CheckRedirect
	sub := Subscription{Endpoint: srv.URL + "/push/1", P256dh: b64.EncodeToString(ua.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
	for st, want := range map[int]Outcome{201: Sent, 410: Gone, 404: Gone, 429: Retry, 503: Retry, 400: Refused, 307: Refused} {
		status, got = st, nil
		r := k.Send(context.Background(), client, sub, "https://factory.example", Message{Payload: []byte("hi"), Topic: TopicFor("a")}, time.Now())
		if r.Outcome != want {
			t.Errorf("status %d: outcome %s (%s), want %s", st, r.Outcome, r.Why, want)
		}
		if st == 201 && string(got) != "hi" {
			t.Errorf("stand-in decrypted %q", got)
		}
		if strings.Contains(r.Why, srv.URL) {
			t.Errorf("result names the endpoint: %s", r.Why)
		}
	}
	AllowedEndpoint = old
	if r := k.Send(context.Background(), client, sub, "x", Message{Payload: []byte("hi")}, time.Now()); r.Outcome != Refused {
		t.Fatalf("a disallowed endpoint was sent to: %s", r.Outcome)
	}
}
