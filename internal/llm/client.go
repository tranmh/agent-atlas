// Package llm is a minimal OpenAI-compatible chat client. Agents point it at
// the gateway's LLM route (http://agentgateway:3100/v1), which forwards to
// Ollama, so LLM traffic shows up in the same gateway as the A2A traffic.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Client calls POST {BaseURL}/chat/completions.
type Client struct {
	BaseURL string // e.g. http://agentgateway:3100/v1
	Model   string
	HTTP    *http.Client
	Headers map[string]string // e.g. X-Caller, X-Run-Id for the gateway access log
}

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is the chat completion request body.
type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream"`
	// ReasoningEffort "none" turns off the thinking phase of reasoning models
	// (qwen3 on Ollama): answers in ~2 s instead of spending every token on
	// hidden reasoning. Other models ignore it.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// Usage is the token usage of a completion.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Response is the subset of the chat completion response we use.
type Response struct {
	Model   string `json:"model"`
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

// Result is the answer plus the raw request/response bodies for the event log.
type Result struct {
	Text     string
	Usage    Usage
	Model    string
	Duration time.Duration
	ReqBody  []byte
	RespBody []byte
}

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// Chat sends one system + user prompt and returns the cleaned answer.
func (c *Client) Chat(ctx context.Context, system, prompt string, maxTokens int, extraHeaders map[string]string) (*Result, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("llm disabled")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	body, _ := json.Marshal(Request{
		Model:           c.Model,
		Messages:        []Message{{Role: "system", Content: system}, {Role: "user", Content: prompt}},
		MaxTokens:       maxTokens,
		Temperature:     0.2,
		ReasoningEffort: "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	res := &Result{Duration: time.Since(start), ReqBody: body, RespBody: raw}
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var r Response
	if err := json.Unmarshal(raw, &r); err != nil {
		return res, fmt.Errorf("llm: decode: %w", err)
	}
	if len(r.Choices) == 0 {
		return res, fmt.Errorf("llm: no choices")
	}
	res.Text = strings.TrimSpace(thinkRe.ReplaceAllString(r.Choices[0].Message.Content, ""))
	res.Usage = r.Usage
	res.Model = r.Model
	if res.Text == "" {
		return res, fmt.Errorf("llm: empty answer")
	}
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
