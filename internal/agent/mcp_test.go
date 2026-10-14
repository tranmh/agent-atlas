package agent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/stretchr/testify/require"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/mcpx"
	"github.com/tranmh/agent-atlas/internal/mcpx/mcpxtest"
	"github.com/tranmh/agent-atlas/internal/scenario"
)

// startMCPAgent serves an agent whose MCP calls go to a fake MCP gateway.
func startMCPAgent(t *testing.T, id string) (*events.Recorder, *a2aclient.Client) {
	t.Helper()
	cat, err := scenario.LoadEmbedded()
	require.NoError(t, err)
	s, a := cat.FindAgent(id)
	rec := &events.Recorder{}
	gw := mcpxtest.NewGateway(t, s, rec)
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = NewHandler(Config{Scenario: s, Agent: a, PublicURL: "http://" + srv.Listener.Addr().String(), Emit: rec, PushAllowPrivate: true,
		MCP: &mcpx.Client{GatewayURL: gw.URL, Emit: rec}})
	srv.Start()
	t.Cleanup(srv.Close)
	card, err := agentcard.DefaultResolver.Resolve(context.Background(), srv.URL)
	require.NoError(t, err)
	client, err := a2aclient.NewFromCard(context.Background(), card, a2aclient.WithJSONRPCTransport(srv.Client()))
	require.NoError(t, err)
	return rec, client
}

func history(task *a2a.Task) []string {
	var out []string
	for _, m := range task.History {
		if m.Role == a2a.MessageRoleAgent && len(m.Parts) > 0 {
			out = append(out, m.Parts[0].Text())
		}
	}
	return out
}

func kindCount(rec *events.Recorder) map[events.Kind]int {
	n := map[events.Kind]int{}
	for _, e := range rec.Events() {
		n[e.Kind]++
	}
	return n
}

func TestSlowMCPToolProgressBecomesStatusUpdates(t *testing.T) {
	rec, client := startMCPAgent(t, "sc-logistics")
	res, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg("Ship it")})
	require.NoError(t, err)
	task := res.(*a2a.Task)
	require.Equal(t, a2a.TaskStateCompleted, task.Status.State)
	h := strings.Join(history(task), "\n")
	require.Contains(t, h, "erp_quote_freight: Asking carrier 2 of 4 for a quote… (2/4)")
	require.Contains(t, h, "erp_quote_freight → Best quote for SKF 23148 to Foz do Iguaçu")

	n := kindCount(rec)
	require.Equal(t, 4, n[events.KindMCPProgress])
	require.Positive(t, n[events.KindMCPRequest])
	require.Positive(t, n[events.KindMCPResponse])
	require.Equal(t, 1, n[events.KindMCPServerCall])
	for _, e := range rec.Events() {
		if e.Kind == events.KindMCPRequest && e.Method == "tools/call" {
			require.Equal(t, "sc-erp", e.To)
			require.Equal(t, "r-test", e.RunID)
		}
	}
}

func TestWriteToolIsRefusedAndTheAgentContinues(t *testing.T) {
	rec, client := startMCPAgent(t, "sc-field-service")
	m := msg("Schedule a visit")
	m.Metadata["skill"] = "schedule-visit"
	res, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: m})
	require.NoError(t, err)
	task := res.(*a2a.Task)
	require.Equal(t, a2a.TaskStateCompleted, task.Status.State)
	h := strings.Join(history(task), "\n")
	require.Contains(t, h, "As expected, agentgateway refused the write tool erp_restricted_shutdown_turbine")
	require.Contains(t, h, "erp_list_technicians → Technicians in Paraná")
	n := kindCount(rec)
	require.Equal(t, 1, n[events.KindMCPDenied])
	require.Equal(t, 1, n[events.KindMCPServerCall], "only the allowed tool reached the server")
}

func TestMultiTurnAgentReadsAnMCPResource(t *testing.T) {
	_, client := startMCPAgent(t, "sc-spare-parts")
	ctx := context.Background()
	res, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg("Need a bearing")})
	require.NoError(t, err)
	task := res.(*a2a.Task)
	answer := msg("SN-T3-2019-0042")
	answer.TaskID, answer.ContextID = task.ID, task.ContextID
	res, err = client.SendMessage(ctx, &a2a.SendMessageRequest{Message: answer})
	require.NoError(t, err)
	h := strings.Join(history(res.(*a2a.Task)), "\n")
	require.Contains(t, h, `Read "Turbine T3 manual, bearing section": Spherical roller bearing.`)
	require.Contains(t, h, "erp_check_stock → SKF 23148: 3 in stock")
}

func TestMCPUnavailableDoesNotBreakTheTask(t *testing.T) {
	cat, err := scenario.LoadEmbedded()
	require.NoError(t, err)
	s, a := cat.FindAgent("sc-logistics")
	rec := &events.Recorder{}
	srv := httptest.NewServer(NewHandler(Config{Scenario: s, Agent: a, PublicURL: "http://x", Emit: rec,
		MCP: &mcpx.Client{GatewayURL: "http://127.0.0.1:1", Emit: rec}}))
	defer srv.Close()
	card := Card(Config{Scenario: s, Agent: a, PublicURL: srv.URL})
	client, err := a2aclient.NewFromCard(context.Background(), card, a2aclient.WithJSONRPCTransport(srv.Client()))
	require.NoError(t, err)
	res, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg("Ship it")})
	require.NoError(t, err)
	task := res.(*a2a.Task)
	require.Equal(t, a2a.TaskStateCompleted, task.Status.State)
	require.Contains(t, strings.Join(history(task), "\n"), "MCP unavailable")
}
