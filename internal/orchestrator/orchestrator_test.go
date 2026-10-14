package orchestrator

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tranmh/agent-atlas/internal/agent"
	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/scenario"
)

// fakeGateway routes /agents/<id>/... to the agent and refuses restricted
// skills with 403, like the generated agentgateway config.
func fakeGateway(t *testing.T, s *scenario.Scenario, rec events.Emitter) *httptest.Server {
	gw := httptest.NewUnstartedServer(nil)
	base := "http://" + gw.Listener.Addr().String()
	mux := http.NewServeMux()
	for i := range s.Agents {
		a := &s.Agents[i]
		srv := httptest.NewServer(agent.NewHandler(agent.Config{Scenario: s, Agent: a, PublicURL: base + "/agents/" + a.ID, Emit: rec, PushAllowPrivate: true}))
		t.Cleanup(srv.Close)
		target, _ := url.Parse(srv.URL)
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.FlushInterval = -1
		proxy.Transport = retry503{}
		prefix := "/agents/" + a.ID
		mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && a.Feature == scenario.FeatureRestricted {
				b, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(b))
				if bytes.Contains(b, []byte(`"skill":"`+scenario.DeniedSkillPrefix)) {
					http.Error(w, "authorization failed", http.StatusForbidden)
					return
				}
			}
			r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
			proxy.ServeHTTP(w, r)
		})
	}
	gw.Config.Handler = mux
	gw.Start()
	t.Cleanup(gw.Close)
	return gw
}

func TestServiceCaseRunsEndToEnd(t *testing.T) {
	cat, err := scenario.LoadEmbedded()
	require.NoError(t, err)
	s := cat.ByCode("sc")
	rec := &events.Recorder{}
	gw := fakeGateway(t, s, rec)

	hook := httptest.NewUnstartedServer(nil)
	o := New(Config{Scenario: s, GatewayURL: gw.URL, PublicURL: "http://" + hook.Listener.Addr().String(), Emit: rec})
	hook.Config.Handler = o.Handler()
	hook.Start()
	defer hook.Close()

	_, err = o.Start(Options{RunID: "sc-t1", Speed: 200, Chaos: true})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return !o.Runs()[0].Finished.IsZero() }, 30*time.Second, 50*time.Millisecond)
	require.Empty(t, o.Runs()[0].Error)

	finished := map[string]string{}
	kinds := map[events.Kind]int{}
	for _, e := range rec.Events() {
		kinds[e.Kind]++
		if e.Kind == events.KindStepFinished {
			finished[e.Step] = e.StateTo
		}
		if e.Source == events.SourceOrchestrator {
			require.Equal(t, "sc-t1", e.RunID)
		}
	}
	require.Equal(t, map[string]string{"diagnose": "completed", "parts": "completed", "ship": "completed", "shutdown": "denied", "visit": "completed"}, finished)
	for _, k := range []events.Kind{events.KindRunStarted, events.KindCardFetch, events.KindRPCRequest, events.KindRPCResponse,
		events.KindStreamEvent, events.KindTaskState, events.KindPushReceived, events.KindPolicyDenied, events.KindAgentRequest,
		events.KindAgentWork, events.KindArtifact, events.KindRunFinished} {
		require.Positive(t, kinds[k], "no %s event", k)
	}
	require.Equal(t, 1, kinds[events.KindFaultInjected], "chaos fails the first streaming attempt once")
}

// retry503 resends a request once when the backend answers 503, like the
// gateway's retry policy on the streaming route.
type retry503 struct{}

func (retry503) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	for attempt := 0; ; attempt++ {
		r.Body = io.NopCloser(bytes.NewReader(body))
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil || resp.StatusCode != http.StatusServiceUnavailable || attempt == 2 {
			return resp, err
		}
		resp.Body.Close()
	}
}
