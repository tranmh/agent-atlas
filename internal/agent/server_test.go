package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/stretchr/testify/require"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/scenario"
	"github.com/tranmh/agent-atlas/internal/script"
)

// startAgent serves one scenario agent on an httptest server.
func startAgent(t *testing.T, id string) (*httptest.Server, *events.Recorder, *a2aclient.Client) {
	t.Helper()
	cat, err := scenario.LoadEmbedded()
	require.NoError(t, err)
	s, a := cat.FindAgent(id)
	require.NotNil(t, a)
	rec := &events.Recorder{}
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = NewHandler(Config{Scenario: s, Agent: a, PublicURL: "http://" + srv.Listener.Addr().String(), Emit: rec, PushAllowPrivate: true})
	srv.Start()
	t.Cleanup(srv.Close)

	ctx := context.Background()
	card, err := agentcard.DefaultResolver.Resolve(ctx, srv.URL)
	require.NoError(t, err)
	require.Equal(t, a.Name, card.Name)
	client, err := a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(srv.Client()))
	require.NoError(t, err)
	return srv, rec, client
}

func msg(text string) *a2a.Message {
	m := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	m.Metadata = map[string]any{script.MetaSpeed: 1000.0, script.MetaRunID: "r-test", script.MetaStep: "s1"}
	return m
}

func TestStreamingAgentStreamsStatusUpdatesAndArtifacts(t *testing.T) {
	_, rec, client := startAgent(t, "sc-diagnosis")
	var states []string
	var artifacts []string
	for ev, err := range client.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: msg("Ticket #1: alarm")}) {
		require.NoError(t, err)
		switch e := ev.(type) {
		case *a2a.Task:
			states = append(states, string(e.Status.State))
		case *a2a.TaskStatusUpdateEvent:
			states = append(states, string(e.Status.State))
		case *a2a.TaskArtifactUpdateEvent:
			artifacts = append(artifacts, e.Artifact.Name)
		}
	}
	require.Equal(t, "TASK_STATE_SUBMITTED", states[0])
	require.Equal(t, "TASK_STATE_COMPLETED", states[len(states)-1])
	require.Contains(t, states, "TASK_STATE_WORKING")
	// No LLM configured: the llm step falls back to the scripted answer.
	require.Equal(t, []string{"answer", "diagnosis"}, artifacts)
	kinds := rec.Kinds()
	require.Contains(t, kinds, events.KindAgentRequest)
	require.Contains(t, kinds, events.KindLLMUnavailable)
	require.Contains(t, kinds, events.KindArtifact)
	for _, e := range rec.Events() {
		require.Equal(t, "sc", e.Scenario)
		if e.Kind == events.KindAgentWork {
			require.Equal(t, "r-test", e.RunID)
			require.Equal(t, "s1", e.Step)
		}
	}
}

func TestMultiTurnAgentAsksAndResumes(t *testing.T) {
	_, rec, client := startAgent(t, "sc-spare-parts")
	ctx := context.Background()
	res, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg("Need a bearing")})
	require.NoError(t, err)
	task, ok := res.(*a2a.Task)
	require.True(t, ok)
	require.Equal(t, a2a.TaskStateInputRequired, task.Status.State)
	require.Contains(t, task.Status.Message.Parts[0].Text(), "serial number")

	answer := msg("SN-42")
	answer.TaskID = task.ID
	answer.ContextID = task.ContextID
	res, err = client.SendMessage(ctx, &a2a.SendMessageRequest{Message: answer})
	require.NoError(t, err)
	task = res.(*a2a.Task)
	require.Equal(t, a2a.TaskStateCompleted, task.Status.State)
	require.Len(t, task.Artifacts, 1)
	require.Equal(t, "reservation", task.Artifacts[0].Name)

	found := false
	for _, e := range rec.Events() {
		found = found || (e.Kind == events.KindAgentWork && e.Note == "Serial SN-42 uses bearing SKF 23148. Checking stock…")
	}
	require.True(t, found, "the answer is substituted into the resumed script")
}

func TestLongTaskSendsPushNotifications(t *testing.T) {
	_, _, client := startAgent(t, "sc-logistics")
	got := make(chan a2a.StreamResponse, 32)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "tok-1", r.Header.Get("A2A-Notification-Token"))
		b, _ := io.ReadAll(r.Body)
		var sr a2a.StreamResponse
		require.NoError(t, json.Unmarshal(b, &sr))
		got <- sr
	}))
	defer hook.Close()

	ctx := context.Background()
	res, err := client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: msg("Ship it"),
		Config:  &a2a.SendMessageConfig{ReturnImmediately: true, PushConfig: &a2a.PushConfig{URL: hook.URL, Token: "tok-1"}},
	})
	require.NoError(t, err)
	task := res.(*a2a.Task)
	require.False(t, task.Status.State.Terminal(), "returnImmediately answers before the work is done")

	deadline := time.After(10 * time.Second)
	for {
		select {
		case sr := <-got:
			if su, ok := sr.Event.(*a2a.TaskStatusUpdateEvent); ok && su.Status.State == a2a.TaskStateCompleted {
				final, err := client.GetTask(ctx, &a2a.GetTaskRequest{ID: task.ID})
				require.NoError(t, err)
				require.Equal(t, a2a.TaskStateCompleted, final.Status.State)
				require.Equal(t, "shipment", final.Artifacts[0].Name)
				return
			}
		case <-deadline:
			t.Fatal("no completed push notification")
		}
	}
}

func TestChaosFailsFirstAttemptOnly(t *testing.T) {
	srv, rec, _ := startAgent(t, "sc-diagnosis")
	body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m-1","role":"ROLE_USER","parts":[{"text":"x"}],"metadata":{"speed":1000}}}}`
	do := func() int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+InvokePath, stringsReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(script.HeaderChaos, "1")
		req.Header.Set(script.HeaderRunID, "r1")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(t, http.StatusServiceUnavailable, do())
	require.Equal(t, http.StatusOK, do())
	require.Contains(t, rec.Kinds(), events.KindFaultInjected)
}
