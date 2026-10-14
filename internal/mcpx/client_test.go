package mcpx_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/mcpx"
	"github.com/tranmh/agent-atlas/internal/mcpx/mcpxtest"
	"github.com/tranmh/agent-atlas/internal/scenario"
)

// flaky fails every POST after it has seen one HTTP 400 (a refused call),
// like a connection reset while the gateway restarts.
type flaky struct{ saw400 atomic.Bool }

func (f *flaky) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPost && f.saw400.Load() {
		return nil, errors.New("connection reset by peer")
	}
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err == nil && resp.StatusCode == http.StatusBadRequest {
		f.saw400.Store(true)
	}
	return resp, err
}

func open(t *testing.T, base http.RoundTripper, speed float64) *mcpx.Session {
	t.Helper()
	cat, err := scenario.LoadEmbedded()
	require.NoError(t, err)
	sc := cat.ByCode("sc")
	gw := mcpxtest.NewGateway(t, sc, &events.Recorder{})
	c := &mcpx.Client{GatewayURL: gw.URL, Emit: &events.Recorder{}, Base: base}
	sess, err := c.Open(context.Background(), mcpx.Call{Scenario: sc, Caller: "sc-field-service", RunID: "r1", Speed: speed})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// Review G1: a transport error after a refused call is not a refusal.
func TestTransportErrorAfterRefusalIsNotDenied(t *testing.T) {
	sess := open(t, &flaky{}, 100)
	_, err := sess.CallTool(context.Background(), "erp_restricted_shutdown_turbine", map[string]any{"asset": "T3"})
	require.ErrorIs(t, err, mcpx.ErrDenied)
	_, err = sess.CallTool(context.Background(), "erp_list_technicians", map[string]any{"region": "Paraná"})
	require.Error(t, err)
	require.NotErrorIs(t, err, mcpx.ErrDenied)
}

// Review G2: a cancelled tool call must not race on shared transport state
// (the SDK sends notifications/cancelled from another goroutine). Run with -race.
func TestCancelledToolCallDoesNotRace(t *testing.T) {
	sess := open(t, http.DefaultTransport, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := sess.CallTool(ctx, "erp_quote_freight", map[string]any{"part": "SKF 23148", "destination": "Foz do Iguaçu"})
	require.Error(t, err)
	require.NotErrorIs(t, err, mcpx.ErrDenied)
	time.Sleep(300 * time.Millisecond)
}
