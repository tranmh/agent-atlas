// Command agent is one A2A agent. M1 spike: a hello agent that streams two
// working updates and completes, used to verify agentgateway's card rewrite.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/tranmh/agent-atlas/internal/httpx"
)

func main() {
	healthFlag := flag.Bool("healthcheck", false, "GET own /healthz and exit 0 or 1")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "agent")
	port, _ := httpx.GetEnvInt("AGENT_PORT", 8080)
	if *healthFlag {
		os.Exit(httpx.RunHealthcheck(fmt.Sprintf("http://127.0.0.1:%d/healthz", port)))
	}
	publicURL := httpx.GetEnv("AGENT_PUBLIC_URL", fmt.Sprintf("http://127.0.0.1:%d", port))

	card := &a2a.AgentCard{
		Name:        "Hello Agent",
		Description: "Says hello, slowly",
		Version:     "0.1.0",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(publicURL+"/invoke", a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		Skills:             []a2a.AgentSkill{{ID: "hello", Name: "Hello", Description: "Greets", Tags: []string{"demo"}}},
	}
	exec := a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			if ec.StoredTask == nil {
				if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
					return
				}
			}
			for _, s := range []string{"Thinking…", "Still thinking…"} {
				if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(s))), nil) {
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Hello, world!"))), nil)
		}
	})
	h := a2asrv.NewHandler(exec)
	mux := http.NewServeMux()
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(h))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { httpx.WriteJSON(w, 200, map[string]string{"status": "ok"}) })
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
}
