// Package webpush sends Web Push messages with the Go standard library only:
// payload encryption per RFC 8291 (aes128gcm, RFC 8188), the sender's
// identity per RFC 8292 (VAPID, an ES256 JWT), and the POST to the push
// service per RFC 8030.
//
// The push service is the browser's own (Apple, Google, Mozilla, Microsoft);
// AllowedEndpoint keeps a subscription from naming anything else, and the
// client follows no redirect, so the viewer can't be made to POST elsewhere.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// b64 is the unpadded base64url every Web Push value uses.
var b64 = base64.RawURLEncoding

// decodeB64 accepts base64url with or without padding (browsers differ).
func decodeB64(s string) ([]byte, error) {
	return b64.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
}

// Keys is the VAPID key pair: the application server's identity.
type Keys struct {
	priv *ecdsa.PrivateKey
}

// keysFile is vapid.json: the private scalar, base64url.
type keysFile struct {
	Private string    `json:"private"`
	Created time.Time `json:"created"`
}

// KeysFile is where LoadOrCreate keeps the pair under dir.
func KeysFile(dir string) string { return filepath.Join(dir, "vapid.json") }

// LoadOrCreate reads dir/vapid.json, or makes a new pair and writes it (dir
// 0700, file 0600, written aside and renamed into place).
func LoadOrCreate(dir string) (*Keys, error) {
	path := KeysFile(dir)
	data, err := os.ReadFile(path)
	if err == nil {
		var f keysFile
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, fmt.Errorf("vapid keys unreadable: %w", err)
		}
		raw, err := decodeB64(f.Private)
		if err != nil {
			return nil, fmt.Errorf("vapid keys unreadable: %w", err)
		}
		priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
		if err != nil {
			return nil, fmt.Errorf("vapid keys unreadable: %w", err)
		}
		return &Keys{priv: priv}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k, err := NewKeys()
	if err != nil {
		return nil, err
	}
	raw, err := k.priv.Bytes()
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(keysFile{Private: b64.EncodeToString(raw), Created: time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	if err := WriteFile(path, out); err != nil {
		return nil, err
	}
	return k, nil
}

// NewKeys makes a fresh pair (not saved).
func NewKeys() (*Keys, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Keys{priv: priv}, nil
}

// KeysFromPrivate builds the pair from a base64url private scalar (tests).
func KeysFromPrivate(s string) (*Keys, error) {
	raw, err := decodeB64(s)
	if err != nil {
		return nil, err
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return nil, err
	}
	return &Keys{priv: priv}, nil
}

// PublicKey is the uncompressed P-256 point, base64url: the browser's
// applicationServerKey and the k= of the Authorization header.
func (k *Keys) PublicKey() string {
	pub, _ := k.priv.PublicKey.Bytes()
	return b64.EncodeToString(pub)
}

// WriteFile writes data to path with mode 0600 (its folder 0700), aside
// first and renamed into place, so a reader never sees half a file.
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Subscription is what the browser's PushManager.subscribe gives: the
// endpoint and the user agent's keys.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// Validate checks the keys decode to a P-256 point and a 16-byte secret.
func (s Subscription) Validate() error {
	if _, err := url.Parse(s.Endpoint); err != nil || s.Endpoint == "" {
		return errors.New("subscription has no endpoint")
	}
	pub, err := decodeB64(s.P256dh)
	if err != nil {
		return errors.New("subscription p256dh is not base64url")
	}
	if _, err := ecdh.P256().NewPublicKey(pub); err != nil {
		return errors.New("subscription p256dh is not a P-256 point")
	}
	auth, err := decodeB64(s.Auth)
	if err != nil || len(auth) != 16 {
		return errors.New("subscription auth is not a 16-byte secret")
	}
	return nil
}

// recordSize is the one record's rs: the whole message must fit in it.
const recordSize = 4096

// MaxPlaintext is the largest payload one record holds (4096 minus the
// 86-byte header, the 16-byte tag and the delimiter).
const MaxPlaintext = recordSize - 86 - 16 - 1

// Encrypt is RFC 8291's message for sub: aes128gcm with one record.
func Encrypt(sub Subscription, plaintext []byte) ([]byte, error) {
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(sub, plaintext, eph, salt)
}

// encrypt is Encrypt with the ephemeral key and salt given (RFC 8291's
// Appendix A test vector fixes both).
func encrypt(sub Subscription, plaintext []byte, eph *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(plaintext) > MaxPlaintext {
		return nil, fmt.Errorf("payload is %d bytes, at most %d fit", len(plaintext), MaxPlaintext)
	}
	uaPubRaw, err := decodeB64(sub.P256dh)
	if err != nil {
		return nil, err
	}
	uaPub, err := ecdh.P256().NewPublicKey(uaPubRaw)
	if err != nil {
		return nil, err
	}
	auth, err := decodeB64(sub.Auth)
	if err != nil {
		return nil, err
	}
	shared, err := eph.ECDH(uaPub)
	if err != nil {
		return nil, err
	}
	asPub := eph.PublicKey().Bytes()

	prkKey, err := hkdf.Extract(sha256.New, shared, auth)
	if err != nil {
		return nil, err
	}
	keyInfo := "WebPush: info\x00" + string(uaPubRaw) + string(asPub)
	ikm, err := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// The last (only) record ends with the 0x02 delimiter, no padding.
	padded := append(append([]byte(nil), plaintext...), 0x02)

	var head bytes.Buffer
	head.Write(salt)
	binary.Write(&head, binary.BigEndian, uint32(recordSize))
	head.WriteByte(byte(len(asPub)))
	head.Write(asPub)
	return gcm.Seal(head.Bytes(), nonce, padded, nil), nil
}

// Decrypt reverses Encrypt with the user agent's private key: the stand-in
// push service in tests reads what the viewer sent with it.
func Decrypt(uaPriv *ecdh.PrivateKey, auth, body []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("short message")
	}
	salt := body[:16]
	idlen := int(body[20])
	if len(body) < 21+idlen {
		return nil, errors.New("short header")
	}
	asPubRaw := body[21 : 21+idlen]
	asPub, err := ecdh.P256().NewPublicKey(asPubRaw)
	if err != nil {
		return nil, err
	}
	shared, err := uaPriv.ECDH(asPub)
	if err != nil {
		return nil, err
	}
	prkKey, err := hkdf.Extract(sha256.New, shared, auth)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(uaPriv.PublicKey().Bytes())+string(asPubRaw), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		return nil, err
	}
	i := bytes.LastIndexByte(plain, 0x02)
	if i < 0 {
		return nil, errors.New("no record delimiter")
	}
	return plain[:i], nil
}

// jwtTTL is how long a VAPID token is good for (RFC 8292: at most 24 h).
const jwtTTL = 12 * time.Hour

// VAPID is the Authorization header value for a push to endpoint: an ES256
// JWT for the endpoint's origin, and the public key.
func (k *Keys) VAPID(endpoint, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("endpoint is not a URL")
	}
	head, _ := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	claims, _ := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(jwtTTL).Unix(),
		"sub": subject,
	})
	signing := b64.EncodeToString(head) + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ", k=" + k.PublicKey(), nil
}

// Claims are a verified VAPID token's claims.
type Claims struct {
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
	Sub string `json:"sub"`
}

// VerifyVAPID checks an Authorization header the way a push service does:
// the ES256 signature against k=, and returns the claims. Tests' stand-in
// push service calls it.
func VerifyVAPID(header string) (Claims, string, error) {
	var c Claims
	rest, ok := strings.CutPrefix(header, "vapid ")
	if !ok {
		return c, "", errors.New("not a vapid header")
	}
	var token, key string
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "t="); ok {
			token = v
		} else if v, ok := strings.CutPrefix(part, "k="); ok {
			key = v
		}
	}
	segs := strings.Split(token, ".")
	if len(segs) != 3 || key == "" {
		return c, "", errors.New("vapid header needs t= and k=")
	}
	pubRaw, err := decodeB64(key)
	if err != nil {
		return c, "", err
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pubRaw)
	if err != nil {
		return c, "", err
	}
	sig, err := decodeB64(segs[2])
	if err != nil || len(sig) != 64 {
		return c, "", errors.New("bad signature encoding")
	}
	sum := sha256.Sum256([]byte(segs[0] + "." + segs[1]))
	if !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return c, "", errors.New("signature does not verify")
	}
	raw, err := decodeB64(segs[1])
	if err != nil {
		return c, "", err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, "", err
	}
	return c, key, nil
}

// pushHosts are the browsers' push services: a host equal to one, or
// ending in "."+one.
var pushHosts = []string{
	"push.apple.com",            // Safari, iOS (web.push.apple.com)
	"fcm.googleapis.com",        // Chrome, Edge on Android, Opera
	"push.services.mozilla.com", // Firefox (updates.push.services.mozilla.com)
	"notify.windows.com",        // Edge on Windows (*.notify.windows.com)
	"android.googleapis.com",    // older Chrome endpoints
}

// AllowedEndpoint reports whether a subscription may name u: https, on a
// browser push service. Tests swap it to allow their httptest server.
var AllowedEndpoint = func(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.User != nil || p.Port() != "" && p.Port() != "443" {
		return false
	}
	host := strings.ToLower(p.Hostname())
	for _, h := range pushHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// Outcome is what the push service said.
type Outcome string

const (
	Sent    Outcome = "sent"    // 201 (or any 2xx)
	Gone    Outcome = "gone"    // 404, 410: the subscription is dead
	Retry   Outcome = "retry"   // 429, 5xx, a network error
	Refused Outcome = "refused" // any other answer, a redirect, a disallowed endpoint
)

// Result is one push's outcome and a short reason for the log (never the
// endpoint).
type Result struct {
	Outcome Outcome
	Status  int
	Why     string
}

// Message is one push.
type Message struct {
	Payload []byte
	// Urgent sends Urgency: high (CI red); normal otherwise.
	Urgent bool
	// Topic replaces an undelivered message with the same topic.
	Topic string
	TTL   time.Duration
}

// TopicFor is a Topic header value for key: base64url of the first 16
// bytes of its SHA-256 (22 characters; RFC 8030 allows 32).
func TopicFor(key string) string {
	sum := sha256.Sum256([]byte(key))
	return b64.EncodeToString(sum[:16])
}

// Client is the HTTP client pushes go through: a timeout per push and no
// redirects, so an allowed host can't bounce the POST elsewhere.
func Client() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errRedirect
		},
	}
}

var errRedirect = errors.New("push services don't redirect: refused")

// Send encrypts m for sub and POSTs it with the VAPID header.
func (k *Keys) Send(ctx context.Context, client *http.Client, sub Subscription, subject string, m Message, now time.Time) Result {
	if !AllowedEndpoint(sub.Endpoint) {
		return Result{Outcome: Refused, Why: "endpoint is not a browser push service"}
	}
	body, err := Encrypt(sub, m.Payload)
	if err != nil {
		return Result{Outcome: Refused, Why: err.Error()}
	}
	auth, err := k.VAPID(sub.Endpoint, subject, now)
	if err != nil {
		return Result{Outcome: Refused, Why: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{Outcome: Refused, Why: "bad endpoint"}
	}
	ttl := m.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", fmt.Sprint(int(ttl.Seconds())))
	req.Header.Set("Authorization", auth)
	if m.Urgent {
		req.Header.Set("Urgency", "high")
	} else {
		req.Header.Set("Urgency", "normal")
	}
	if m.Topic != "" {
		req.Header.Set("Topic", m.Topic)
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errRedirect) {
			return Result{Outcome: Refused, Why: "the push service answered a redirect"}
		}
		return Result{Outcome: Retry, Why: "network: " + shortErr(err)}
	}
	defer resp.Body.Close()
	reason, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	r := Result{Status: resp.StatusCode, Why: strings.TrimSpace(string(reason))}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		r.Outcome = Sent
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		r.Outcome = Gone
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		r.Outcome = Retry
	default:
		r.Outcome = Refused
	}
	if len(r.Why) > 120 {
		r.Why = r.Why[:120]
	}
	return r
}

// shortErr is an error without the URL net/http puts in it (the endpoint
// is a capability and stays out of logs).
func shortErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}
