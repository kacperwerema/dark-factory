// Command telemetry-collector receives the opt-in usage batches issue-cli
// installs send (docs/CLI/telemetry.md, "Sharing usage"). It validates each
// batch against the names-only schema, refuses anything that could carry
// free text, and appends accepted batches to <data>/batches.jsonl, the file
// the viewer's /telemetry page reads (telemetry_data: in projects.yaml).
//
//	go run ./cmd/telemetry-collector -data ./telemetry-data [-listen 127.0.0.1:8787]
//
// It listens on loopback by default. Serving it to other machines (a proxy,
// TLS, a domain) is the operator's choice; the engine ships no collector URL.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/telemetry/share"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "telemetry-collector:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("telemetry-collector", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8787", "address to listen on")
	data := fs.String("data", "", "directory for batches.jsonl (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *data == "" {
		return errors.New("-data <dir> is required")
	}
	srv := &http.Server{
		Addr:              *listen,
		Handler:           share.Handler(share.Store{Dir: *data}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("telemetry-collector: listening on %s, storing in %s", *listen, share.Store{Dir: *data}.Path())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
