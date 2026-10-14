// Command orchestrator runs one scenario's orchestrator (ORCH_ID). The
// visualizer starts runs with POST /run; agents push notifications to
// POST /push/{token}.
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
	"syscall"
	"time"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/httpx"
	"github.com/tranmh/agent-atlas/internal/orchestrator"
	"github.com/tranmh/agent-atlas/internal/scenario"
)

func main() {
	healthFlag := flag.Bool("healthcheck", false, "GET own /healthz and exit 0 (ok) or 1")
	flag.Parse()
	port, err := httpx.GetEnvInt("ORCH_PORT", 8090)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *healthFlag {
		os.Exit(httpx.RunHealthcheck(fmt.Sprintf("http://127.0.0.1:%d/healthz", port)))
	}
	id, err := httpx.RequireEnv("ORCH_ID")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "orchestrator", "orchestrator", id)
	cat, err := scenario.LoadEmbedded()
	if err != nil {
		log.Error("scenarios", "err", err)
		os.Exit(1)
	}
	sc := cat.FindOrchestrator(id)
	if sc == nil {
		log.Error("unknown orchestrator id", "id", id)
		os.Exit(1)
	}
	limit, _ := httpx.GetEnvInt("EVENT_PAYLOAD_MAX_BYTES", events.DefaultPayloadLimit)
	vizURL := httpx.GetEnv("VISUALIZER_URL", "")
	reporter := events.NewReporter(vizURL, log)
	defer reporter.Close()

	o := orchestrator.New(orchestrator.Config{
		Scenario:      sc,
		GatewayURL:    httpx.GetEnv("GATEWAY_URL", "http://agentgateway:3000"),
		PublicURL:     httpx.GetEnv("ORCH_PUBLIC_URL", fmt.Sprintf("http://%s:%d", id, port)),
		VisualizerURL: vizURL,
		Emit:          reporter,
		Log:           log,
		PayloadLimit:  limit,
	})
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: o.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("listening", "addr", srv.Addr, "scenario", sc.ID)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}
