package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/payments"
)

// The client commands' environment: the bearer token (a connector's
// secrets: fills it) and the kit's base URL.
const (
	envToken = "PAYMENTS_TOKEN"
	envURL   = "PAYMENTS_KIT_URL"
)

const defaultKitURL = "http://127.0.0.1:8787"

// connectorHint names the Sales template's connectors (docs/payments.md#from-a-department).
const connectorHint = "run it through issue-cli connector exec sales/payments -- … (sales/payments-refund for a refund), which fills $" + envToken + " from Keychain"

var httpClient = &http.Client{Timeout: 30 * time.Second}

// client runs payment, provider and refund against a running kit and prints
// its JSON answer.
func client(args []string, stdout, stderr io.Writer) int {
	name := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	def := os.Getenv(envURL)
	if def == "" {
		def = defaultKitURL
	}
	base := fs.String("url", def, "the kit's base URL")
	var amount int64
	var reason, key string
	if name == "refund" {
		fs.Int64Var(&amount, "amount", 0, "minor units to refund (2900 = 29.00); left out: all that is left")
		fs.StringVar(&reason, "reason", "", "why, kept with the refund")
		fs.StringVar(&key, "idempotency-key", "", "required: the same key never refunds twice (the issue's slug)")
	}
	// Flags may come before or after the id.
	var pos []string
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	want := 1
	if name == "provider" {
		want = 0
	}
	if len(pos) != want {
		fmt.Fprintf(stderr, "payments-kit %s: wants %s\n", name, map[string]string{"payment": "<payment-id>", "refund": "<payment-id>", "provider": "no arguments"}[name])
		return 2
	}
	tok := os.Getenv(envToken)
	if tok == "" {
		fmt.Fprintf(stderr, "payments-kit %s: $%s is not set: %s\n", name, envToken, connectorHint)
		return 2
	}
	var method, path string
	var body []byte
	switch name {
	case "payment":
		method, path = "GET", "/v1/payments/"+url.PathEscape(pos[0])
	case "provider":
		method, path = "GET", "/v1/provider"
	case "refund":
		if strings.TrimSpace(key) == "" {
			fmt.Fprintln(stderr, "payments-kit refund: --idempotency-key is required (use the issue's slug), so a repeated call never refunds twice")
			return 2
		}
		if amount < 0 {
			fmt.Fprintln(stderr, "payments-kit refund: --amount wants minor units above 0 (left out: all that is left)")
			return 2
		}
		method, path = "POST", "/v1/payments/"+url.PathEscape(pos[0])+"/refunds"
		body, _ = json.Marshal(payments.RefundRequest{Amount: amount, Reason: reason, IdempotencyKey: key})
	}
	req, err := http.NewRequest(method, strings.TrimRight(*base, "/")+path, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(stderr, "payments-kit %s: %v\n", name, err)
		return 1
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "payments-kit %s: %v (is payments-kit serve running at %s?)\n", name, err, *base)
		return 1
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(out))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(out, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		fmt.Fprintf(stderr, "payments-kit %s: %d: %s\n", name, resp.StatusCode, msg)
		if resp.StatusCode == http.StatusForbidden && name == "refund" {
			fmt.Fprintln(stderr, "a refund runs through issue-cli connector exec sales/payments-refund, after the owner approved it")
		}
		return 1
	}
	stdout.Write(out)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		fmt.Fprintln(stdout)
	}
	return 0
}
