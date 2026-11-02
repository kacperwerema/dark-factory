package viewer

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The identity gate (docs/API/security.md): a request is public when its
// Host is not a loopback name or it came through a proxy (any forwarding
// header). A public request is answered only when projects.yaml's
// identity: is configured and the request carries a Cloudflare Access token
// that verifies for one of identity.owners. Anything else gets 403: the
// viewer fails closed, so a tunnel or -host pointed at it by mistake
// exposes nothing.

// proxyHeaders mark a request that a proxy or tunnel forwarded: cloudflared
// sends Cf-Ray and Cf-Connecting-Ip, other proxies X-Forwarded-* or
// Forwarded. A local browser, curl or issue-cli sends none of them, so a
// tunnel that rewrites Host to localhost is still treated as public.
var proxyHeaders = []string{
	"Cf-Ray", "Cf-Connecting-Ip", "Cf-Access-Jwt-Assertion", "Cf-Ipcountry",
	"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip",
	"Forwarded", "True-Client-Ip",
}

// cfAccessHeader is the header Cloudflare Access adds to every request it
// lets through to the origin: a JWT signed by the team's keys.
const cfAccessHeader = "Cf-Access-Jwt-Assertion"

// publicRequest reports whether r came from outside this machine's own
// clients: a Host that isn't loopback, or any proxy header.
func publicRequest(r *http.Request) bool {
	if !isLoopbackName(normalizeHost(r.Host)) {
		return true
	}
	for _, h := range proxyHeaders {
		if _, ok := r.Header[http.CanonicalHeaderKey(h)]; ok {
			return true
		}
	}
	return false
}

type identityKey struct{}

// verifiedIdentity is the owner email the identity gate verified for r, ""
// for a local request.
func verifiedIdentity(r *http.Request) string {
	email, _ := r.Context().Value(identityKey{}).(string)
	return email
}

// noIdentityMsg is the 403 a public request gets from a viewer without an
// identity check.
const noIdentityMsg = "refused: this request came from a public name or through a proxy, and this viewer has no identity check (identity: in projects.yaml). The viewer answers only loopback until one is configured; see docs/API/security.md"

// identityGate refuses a public request unless it carries a verified owner
// identity; local requests pass untouched.
func identityGate(id *identityChecker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !publicRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		if id == nil {
			http.Error(w, noIdentityMsg, http.StatusForbidden)
			return
		}
		email, err := id.Verify(r.Context(), r.Header.Get(cfAccessHeader))
		if err != nil {
			http.Error(w, "refused: identity check failed: "+err.Error(), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, email)))
	})
}

// identityChecker verifies Cloudflare Access tokens: RS256, signed by a key
// from the team's certs URL, issuer the team domain, audience the
// application's AUD tag, unexpired, and an email in owners.
type identityChecker struct {
	issuer   string
	audience string
	owners   []string
	certsURL string
	client   *http.Client
	now      func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetched   time.Time
	lastFetch time.Time
}

const (
	// keysTTL is how long fetched signing keys are trusted before a refetch.
	keysTTL = time.Hour
	// refetchEvery bounds refetches on an unknown key id, so tokens with
	// made-up kids can't turn the viewer into a request amplifier.
	refetchEvery = time.Minute
	// clockLeeway forgives small clock skew on exp and nbf.
	clockLeeway = 30 * time.Second
)

// newIdentityChecker builds the checker from projects.yaml's identity:;
// nil config means no checker (public requests refused). An incomplete
// config is an error: the viewer refuses to start rather than guess.
func newIdentityChecker(cfg *tracker.IdentityConfig) (*identityChecker, error) {
	if cfg == nil {
		return nil, nil
	}
	ca := cfg.CloudflareAccess
	team := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(ca.TeamDomain), "https://"), "/")
	var missing []string
	if team == "" {
		missing = append(missing, "cloudflare_access.team_domain")
	}
	if strings.TrimSpace(ca.Audience) == "" {
		missing = append(missing, "cloudflare_access.audience")
	}
	var owners []string
	for _, o := range cfg.Owners {
		if o = strings.ToLower(strings.TrimSpace(o)); o != "" {
			owners = append(owners, o)
		}
	}
	if len(owners) == 0 {
		missing = append(missing, "owners")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("identity: is set but incomplete, missing %s (docs/API/security.md)", strings.Join(missing, ", "))
	}
	certs := ca.CertsURL
	if certs == "" {
		certs = "https://" + team + "/cdn-cgi/access/certs"
	}
	return &identityChecker{
		issuer:   "https://" + team,
		audience: strings.TrimSpace(ca.Audience),
		owners:   owners,
		certsURL: certs,
		client:   &http.Client{Timeout: 10 * time.Second},
		now:      time.Now,
	}, nil
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwtClaims struct {
	Iss   string          `json:"iss"`
	Aud   json.RawMessage `json:"aud"`
	Exp   int64           `json:"exp"`
	Nbf   int64           `json:"nbf"`
	Email string          `json:"email"`
}

// Verify checks token and returns the owner's email.
func (c *identityChecker) Verify(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", errors.New("no " + cfAccessHeader + " header (is Cloudflare Access in front of this name?)")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("malformed token")
	}
	var hdr jwtHeader
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return "", errors.New("malformed token header")
	}
	if hdr.Alg != "RS256" {
		return "", fmt.Errorf("token algorithm %q not accepted", hdr.Alg)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("malformed token signature")
	}
	key, err := c.key(ctx, hdr.Kid)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return "", errors.New("token signature does not verify")
	}
	var cl jwtClaims
	if err := decodeSegment(parts[1], &cl); err != nil {
		return "", errors.New("malformed token claims")
	}
	if cl.Iss != c.issuer {
		return "", errors.New("token issuer is not this team")
	}
	if !audienceHas(cl.Aud, c.audience) {
		return "", errors.New("token audience is not this application")
	}
	now := c.now()
	if cl.Exp == 0 || now.After(time.Unix(cl.Exp, 0).Add(clockLeeway)) {
		return "", errors.New("token expired")
	}
	if cl.Nbf != 0 && now.Add(clockLeeway).Before(time.Unix(cl.Nbf, 0)) {
		return "", errors.New("token not valid yet")
	}
	email := strings.ToLower(strings.TrimSpace(cl.Email))
	if email == "" || !slices.Contains(c.owners, email) {
		return "", errors.New("signed in, but not as an owner of this viewer")
	}
	return email, nil
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// audienceHas reads aud as a string or a list of strings.
func audienceHas(raw json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return slices.Contains(many, want)
	}
	return false
}

// key returns the signing key kid, fetching the team's keys when the cache
// is empty, stale, or lacks kid (at most once per refetchEvery).
func (c *identityChecker) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	k, ok := c.keys[kid]
	fresh := now.Sub(c.fetched) < keysTTL
	if ok && fresh {
		return k, nil
	}
	if c.lastFetch.IsZero() || now.Sub(c.lastFetch) >= refetchEvery || !fresh {
		c.lastFetch = now
		keys, err := c.fetchKeys(ctx)
		if err != nil {
			return nil, fmt.Errorf("could not fetch the team's signing keys: %w", err)
		}
		c.keys, c.fetched = keys, now
		k, ok = keys[kid]
	}
	if !ok {
		return nil, errors.New("token signed by an unknown key")
	}
	return k, nil
}

type jwks struct {
	Keys []struct {
		Kid string `json:"kid"`
		Kty string `json:"kty"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (c *identityChecker) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.certsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", c.certsURL, resp.StatusCode)
	}
	var set jwks
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pub.N.BitLen() < 2048 {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, errors.New("no usable RSA keys")
	}
	return keys, nil
}
