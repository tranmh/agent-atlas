// Command mcpserver runs one scenario MCP server (MCP_SERVER_ID) over
// Streamable HTTP. Agents reach it only through the scenario's MCP route on
// agentgateway (/mcp/<code>).
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
	"github.com/tranmh/agent-atlas/internal/mcpserver"
	"github.com/tranmh/agent-atlas/internal/scenario"
)

func main() {
	healthFlag := flag.Bool("healthcheck", false, "GET own /healthz and exit 0 (ok) or 1")
	flag.Parse()
	port, err := httpx.GetEnvInt("MCP_PORT", 8070)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *healthFlag {
		os.Exit(httpx.RunHealthcheck(fmt.Sprintf("http://127.0.0.1:%d%s", port, mcpserver.HealthPath)))
	}
	id, err := httpx.RequireEnv("MCP_SERVER_ID")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "mcpserver", "mcp_server", id)
	cat, err := scenario.LoadEmbedded()
	if err != nil {
		log.Error("scenarios", "err", err)
		os.Exit(1)
	}
	sc, def := cat.FindMCPServer(id)
	if def == nil {
		log.Error("unknown MCP server id", "id", id)
		os.Exit(1)
	}
	limit, _ := httpx.GetEnvInt("EVENT_PAYLOAD_MAX_BYTES", events.DefaultPayloadLimit)
	reporter := events.NewReporter(httpx.GetEnv("VISUALIZER_URL", ""), log)
	defer reporter.Close()

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mcpserver.NewHandler(mcpserver.Config{Scenario: sc, Server: def, Emit: reporter, PayloadLimit: limit, Log: log}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("listening", "addr", srv.Addr, "scenario", sc.ID, "tools", len(def.Tools))
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
