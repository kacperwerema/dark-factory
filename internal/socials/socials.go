// Package socials connects a department's social tool to the owner's
// networks: which networks a tool supports and what each needs (the
// owner's developer-app values, an app password, or plain OAuth), the
// tool's calls behind the Connect socials page (key check, channel states,
// a network's sign-in URL, an app-password connect, disconnect), and the
// log of what the last check found (<issues>/.connectors/socials.jsonl).
//
// The engine knows tools only through an Adapter, picked by the connector's
// per_run: tool: id (factory/factory-departments-floor-map-and-capabilities#D-9).
// Postiz is the first (postiz.go), epic
// factory/open-source-partners-integrate-outside-tools-openmontage-openseo-postiz-instead-of-building-them-in-house#D-5 and #D-7.
// Every call is made inside a connectors.Hold lease by the caller, so the
// tool runs only for the action and is stopped after.
package socials

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// How a network connects.
const (
	// ConnectOAuth: the tool hands out the network's sign-in URL; the
	// owner signs in at the network, which returns to the tool. The
	// factory never sees a password or token.
	ConnectOAuth = "oauth"
	// ConnectPassword: the network takes an app password typed into the
	// page; the viewer passes it once to the tool and keeps nothing.
	ConnectPassword = "password"
	// ConnectElsewhere: only the tool's own UI connects it.
	ConnectElsewhere = "elsewhere"
)

// Var is one developer-app value the tool reads from its environment.
type Var struct {
	// Name is the tool's env var (X_API_KEY); Label what the network calls it.
	Name, Label string
}

// Field is one value an app-password network asks for at connect time.
type Field struct {
	Key, Label, Default string
	Secret              bool
}

// Network is one network the tool can post to.
type Network struct {
	// ID is the tool's provider id (linkedin, x, bluesky).
	ID, Name string
	// Connect is ConnectOAuth, ConnectPassword or ConnectElsewhere.
	Connect string
	// Vars are the owner's developer-app values the tool needs first.
	Vars []Var
	// Fields are an app-password network's connect fields.
	Fields []Field
	// App says what developer app to make, Wait how long its review takes
	// and what it costs; both from the tool's docs and the spike report.
	App, Wait string
	// HTTPS: the network takes only an https redirect, so a tool on
	// http://localhost can't finish its sign-in.
	HTTPS bool
	// Step: after the sign-in the tool asks for one more choice (which
	// page) in its own UI.
	Step bool
}

// Channel states.
const (
	StateConnected = "connected"
	StateExpired   = "expired"
	StateDisabled  = "disabled"
	StateStep      = "needs a step"
)

// Channel is one connected account as the tool reports it. It holds the
// tool's channel id and provider only: no name, handle or token.
type Channel struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	State    string `json:"state"`
}

// Status is what one check found.
type Status struct {
	Channels []Channel
	// ExpiryKnown is false when the tool's answer had no expiry (the
	// fallback listing): expired then shows as connected.
	ExpiryKnown bool
}

// Client is one tool instance's calls, made with the owner's key.
type Client interface {
	// KeyOK asks the tool whether the key works: ErrKey when refused.
	KeyOK(ctx context.Context) error
	Status(ctx context.Context) (Status, error)
	// AuthURL is network id's sign-in URL (ConnectOAuth).
	AuthURL(ctx context.Context, id string) (string, error)
	// ConnectFields connects an app-password network with fields.
	ConnectFields(ctx context.Context, id string, fields map[string]string) error
	// Disconnect removes channel id from the tool.
	Disconnect(ctx context.Context, channel string) error
}

// Adapter is one tool the page knows.
type Adapter interface {
	// Name is the tool's name on the page (Postiz).
	Name() string
	Networks() []Network
	// Base is the tool's web address from the connector's url:.
	Base(c *tracker.Connector) string
	// RedirectURI is what the owner pastes into network id's developer app.
	RedirectURI(base, id string) string
	// UI is the tool's own page for connecting a network by hand.
	UI(base string) string
	// KeyRef is where the key is kept: the reference in the connector's
	// Authorization header.
	KeyRef(c *tracker.Connector) (tracker.SecretRef, bool)
	// EnvRef is the Keychain item that holds developer-app value v.
	EnvRef(v string) tracker.SecretRef
	// Override is the tool's compose override passing vars from the start
	// step's environment: references only, never a value.
	Override(vars []string) []byte
	NewClient(base, key string, hc *http.Client) Client
}

// ErrKey is a key the tool refused.
var ErrKey = errors.New("the tool refused the key (401)")

var adapters = map[string]Adapter{}

// Register adds a tool's adapter under its per_run: tool: id.
func Register(tool string, a Adapter) { adapters[strings.ToLower(tool)] = a }

// For is c's adapter, by its per_run: tool: id; nil when none.
func For(c *tracker.Connector) Adapter {
	if c == nil || c.PerRun == nil {
		return nil
	}
	return adapters[strings.ToLower(strings.TrimSpace(c.PerRun.Tool))]
}

// Find is the network id in a's table; nil when it has none.
func Find(a Adapter, id string) *Network {
	for _, n := range a.Networks() {
		if n.ID == id {
			return &n
		}
	}
	return nil
}

// HTTPClient is the client every adapter call uses: a short timeout, no
// redirects (an answer pointing elsewhere is the tool's, not ours to follow).
func HTTPClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
