// Command agent runs one A2A worker agent from the embedded scenarios.
// AGENT_ID selects the agent; everything else (card, skills, script, feature)
// comes from the scenario file.
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
	"strconv"
	"syscall"
	"time"

	"github.com/tranmh/agent-atlas/internal/agent"
	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/httpx"
	"github.com/tranmh/agent-atlas/internal/llm"
	"github.com/tranmh/agent-atlas/internal/scenario"
	"github.com/tranmh/agent-atlas/internal/script"
)

func main() {
	healthFlag := flag.Bool("healthcheck", false, "GET own /healthz and exit 0 (ok) or 1")
	flag.Parse()

	port, err := httpx.GetEnvInt("AGENT_PORT", 8080)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *healthFlag {
		os.Exit(httpx.RunHealthcheck(fmt.Sprintf("http://127.0.0.1:%d%s", port, agent.HealthPath)))
	}
	id, err := httpx.RequireEnv("AGENT_ID")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "agent", "agent", id)

	cat, err := scenario.LoadEmbedded()
	if err != nil {
		log.Error("scenarios", "err", err)
		os.Exit(1)
	}
	sc, a := cat.FindAgent(id)
	if a == nil {
		log.Error("unknown agent id", "id", id)
		os.Exit(1)
	}
	speed, _ := strconv.ParseFloat(httpx.GetEnv("SCRIPT_SPEED", "1"), 64)
	limit, _ := httpx.GetEnvInt("EVENT_PAYLOAD_MAX_BYTES", events.DefaultPayloadLimit)
	reporter := events.NewReporter(httpx.GetEnv("VISUALIZER_URL", ""), log)
	defer reporter.Close()

	var chatter script.Chatter
	if a.Mode == scenario.ModeLLM && httpx.GetEnvBool("LLM_ENABLED", true) {
		chatter = &llm.Client{
			BaseURL: httpx.GetEnv("LLM_BASE_URL", "http://agentgateway:3100/v1"),
			Model:   httpx.GetEnv("LLM_MODEL", "qwen3:1.7b"),
			HTTP:    &http.Client{Timeout: 60 * time.Second},
		}
	}

	h := agent.NewHandler(agent.Config{
		Scenario:         sc,
		Agent:            a,
		PublicURL:        httpx.GetEnv("AGENT_PUBLIC_URL", fmt.Sprintf("http://%s:%d", id, port)),
		Emit:             reporter,
		LLM:              chatter,
		DefaultSpeed:     speed,
		PayloadLimit:     limit,
		Log:              log,
		PushAllowPrivate: true, // webhooks live on the compose network
	})
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("listening", "addr", srv.Addr, "scenario", sc.ID, "feature", a.Feature, "mode", a.Mode)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	log.Info("stopped")
}
