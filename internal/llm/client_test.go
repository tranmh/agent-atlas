package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatStripsThinkingAndSendsHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, "sc-diagnosis", r.Header.Get("X-Caller"))
		var req Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "qwen3:1.7b", req.Model)
		require.Equal(t, "none", req.ReasoningEffort)
		_, _ = w.Write([]byte(`{"model":"qwen3:1.7b","choices":[{"message":{"role":"assistant","content":"<think>\n\n</think>\n\nBearing B3."}}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/v1", Model: "qwen3:1.7b", Headers: map[string]string{"X-Caller": "sc-diagnosis"}}
	res, err := c.Chat(context.Background(), "sys", "prompt", 50, nil)
	require.NoError(t, err)
	require.Equal(t, "Bearing B3.", res.Text)
	require.Equal(t, 3, res.Usage.CompletionTokens)
}

func TestChatErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL, Model: "m"}).Chat(context.Background(), "s", "p", 5, nil)
	require.ErrorContains(t, err, "502")
	_, err = (&Client{}).Chat(context.Background(), "s", "p", 5, nil)
	require.ErrorContains(t, err, "disabled")
}

func TestChatMessagesWithToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Len(t, req.Tools, 1)
		require.Equal(t, "historian_read_trend", req.Tools[0].Function.Name)
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"historian_read_trend","arguments":"{\"asset\":\"T3\"}"}}]}}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Model: "m"}
	res, err := c.ChatMessages(context.Background(), []Message{{Role: "user", Content: "x"}},
		[]Tool{{Type: "function", Function: FunctionDef{Name: "historian_read_trend", Parameters: map[string]any{"type": "object"}}}}, 100, nil)
	require.NoError(t, err)
	require.Len(t, res.ToolCalls, 1)
	require.Equal(t, `{"asset":"T3"}`, res.ToolCalls[0].Function.Arguments)
	require.Equal(t, "assistant", res.Message.Role)
}
