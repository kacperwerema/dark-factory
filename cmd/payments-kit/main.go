// Command payments-kit is the provider-neutral payments service a project
// runs beside its app (docs/payments.md): the app opens checkouts, looks up
// payments, refunds and cancels subscriptions over local HTTP, and gets each
// provider event once, in neutral form, signed by the kit.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/buildversion"
	"github.com/kacperwerema/dark-factory/internal/payments"
	_ "github.com/kacperwerema/dark-factory/internal/payments/standin"
)

// Version is stamped by make install (-X main.Version=…).
var Version = "dev"

const usage = `payments-kit: the provider-neutral payments service (docs/payments.md)

  payments-kit serve [-config payments.yaml]   run the kit beside the app
  payments-kit check [-config payments.yaml]   check the config and each secret reference (set or missing, never its value)
  payments-kit providers                       list the provider adapters built in
  payments-kit version

Calls to a running kit (the bearer token from $PAYMENTS_TOKEN, the kit at
-url, $PAYMENTS_KIT_URL or http://127.0.0.1:8787); a Sales agent runs them
through issue-cli connector exec, which fills the token from Keychain:

  payments-kit payment <payment-id>            a payment's state (the read token is enough)
  payments-kit provider                        the provider and what it offers (read token)
  payments-kit refund <payment-id> --idempotency-key <key> [--amount <minor units>] [--reason "..."]
                                               refund all that is left, or --amount of it (the api token)

Start with provider: standin: the stand-in provider takes no money and needs no account.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "payments-kit", buildversion.Resolve(Version))
		return 0
	case "providers":
		for _, n := range payments.Registered() {
			fmt.Fprintln(stdout, n)
		}
		return 0
	case "payment", "provider", "refund":
		return client(args, stdout, stderr)
	case "check", "serve":
	default:
		fmt.Fprintf(stderr, "payments-kit: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "payments.yaml", "the project's payments config")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	cfg, err := payments.LoadConfig(*path)
	if err != nil {
		fmt.Fprintf(stderr, "payments-kit: %v\n", err)
		return 1
	}
	if args[0] == "check" {
		return check(cfg, *path, stdout)
	}
	return serve(cfg, *path, stderr)
}

func check(cfg payments.Config, path string, out io.Writer) int {
	switch cfg.Mode {
	case "live":
		fmt.Fprintln(out, "mode: live (real money)")
	case "sandbox":
		fmt.Fprintln(out, "mode: sandbox (no money moves)")
	default:
		fmt.Fprintf(out, "mode: %q\n", cfg.Mode)
	}
	if cc := cfg.Consent; cc != nil {
		if t, err := cc.LoadTexts(cfg.Dir); err == nil {
			state := fmt.Sprintf("reviewed by %s on %s", t.ReviewedBy, t.ReviewedOn)
			if t.Placeholder {
				state = "PLACEHOLDER wording, pending the lawyer's review: no live use"
			}
			fmt.Fprintf(out, "consent: on, texts %s (%s)\n", t.Version, state)
		}
	}
	ps := cfg.Validate()
	for _, p := range ps {
		fmt.Fprintf(out, "✗ %s\n", p)
	}
	missing := 0
	for _, s := range cfg.SecretStates() {
		state := "set"
		if !s.Set {
			state, missing = "missing", missing+1
		}
		fmt.Fprintf(out, "  %s: %s (%s)\n", s.Field, s.Ref, state)
	}
	if len(ps) > 0 || missing > 0 {
		fmt.Fprintf(out, "%s: %d problem(s), %d secret(s) missing\n", path, len(ps), missing)
		return 1
	}
	fmt.Fprintf(out, "%s: ok (provider %s, mode %s, events to %s)\n", path, cfg.Provider, cfg.Mode, cfg.EventsURL)
	return 0
}

func serve(cfg payments.Config, path string, stderr io.Writer) int {
	logger := log.New(stderr, "payments-kit ", log.LstdFlags)
	svc, err := payments.Open(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "payments-kit: %v\n(payments-kit check -config %s lists every problem)\n", err, path)
		return 1
	}
	svc.Log = logger
	srv := &http.Server{Addr: cfg.Listen, Handler: svc.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sc)
	}()
	logger.Printf("listening on %s: provider %s (%s), events to %s", cfg.Listen, cfg.Provider, cfg.Mode, cfg.EventsURL)
	if cfg.Provider == "standin" {
		logger.Printf("stand-in provider: checkout pages at %s/standin/checkout/<id>; no money moves", strings.TrimRight(cfg.PublicURL, "/"))
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "payments-kit: %v\n", err)
		return 1
	}
	return 0
}
