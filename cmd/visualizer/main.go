// Command visualizer is the event hub for the browser UI (see package
// internal/visualizer).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tranmh/agent-atlas/internal/eventlog"
	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/httpx"
	"github.com/tranmh/agent-atlas/internal/scenario"
	"github.com/tranmh/agent-atlas/internal/visualizer"
)

func main() {
	healthFlag := flag.Bool("healthcheck", false, "GET own /healthz and exit 0 (ok) or 1")
	flag.Parse()
	port, err := httpx.GetEnvInt("VISUALIZER_PORT", 8095)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *healthFlag {
		os.Exit(httpx.RunHealthcheck(fmt.Sprintf("http://127.0.0.1:%d/healthz", port)))
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "visualizer")
	cat, err := scenario.LoadEmbedded()
	if err != nil {
		log.Error("scenarios", "err", err)
		os.Exit(1)
	}
	store, err := eventlog.Open(httpx.GetEnv("SESSIONS_DIR", "data/sessions"), nil)
	if err != nil {
		log.Error("event store", "err", err)
		os.Exit(1)
	}
	limit, _ := httpx.GetEnvInt("EVENT_PAYLOAD_MAX_BYTES", events.DefaultPayloadLimit)
	tmpl := httpx.GetEnv("ORCH_URL_TEMPLATE", "http://{id}:8090")
	stagger, _ := httpx.GetEnvDuration("RUN_STAGGER", 2*time.Second)
	s := visualizer.New(visualizer.Config{
		Store: store, Catalog: cat, Log: log, PayloadLimit: limit, RunStagger: stagger,
		OrchestratorURL: func(id string) string { return strings.ReplaceAll(tmpl, "{id}", id) },
	})
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("listening", "addr", srv.Addr, "session", store.Session())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	// Closing the store ends every SSE stream; then the server can drain.
	_ = store.Close()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	log.Info("stopped")
}
