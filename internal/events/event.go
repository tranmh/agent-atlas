// Package events defines the visualizer event log contract (docs/events.md)
// and the asynchronous reporter every agent and orchestrator uses to send
// events to the visualizer.
package events

import (
	"encoding/json"
	"strings"
	"time"
)

// Kind classifies an event. The browser decides how to draw an event by kind.
type Kind string

const (
	// Orchestrator narration.
	KindRunStarted   Kind = "run_started"
	KindRunFinished  Kind = "run_finished"
	KindStepStarted  Kind = "step_started"
	KindStepFinished Kind = "step_finished"
	KindBreakpoint   Kind = "breakpoint" // waiting at a gate (visualizer)
	KindGateReleased Kind = "gate_released"

	// Wire events seen by the orchestrator's HTTP client (always via the gateway).
	KindCardFetch    Kind = "card_fetch"    // GET /.well-known/agent-card.json and the (rewritten) card
	KindRPCRequest   Kind = "rpc_request"   // JSON-RPC request body
	KindRPCResponse  Kind = "rpc_response"  // JSON-RPC response body (or end of a stream)
	KindStreamEvent  Kind = "stream_event"  // one SSE frame of a streaming response
	KindPolicyDenied Kind = "policy_denied" // the gateway refused the request (HTTP 403)
	KindTaskState    Kind = "task_state"    // a task changed state (derived from wire data)
	KindPushReceived Kind = "push_received" // push notification on the orchestrator webhook (direct, not via gateway)

	// What the agents do behind the gateway.
	KindAgentRequest   Kind = "agent_request"   // a request arrived at the agent (headers after the gateway)
	KindAgentWork      Kind = "agent_work"      // a script step: the spoken status text
	KindArtifact       Kind = "artifact"        // the agent emitted an artifact
	KindFaultInjected  Kind = "fault_injected"  // chaos: the agent answered 503 so the gateway retries
	KindLLMRequest     Kind = "llm_request"     // agent → gateway LLM route
	KindLLMResponse    Kind = "llm_response"    // model answer
	KindLLMUnavailable Kind = "llm_unavailable" // model unreachable, scripted fallback used

	// What the gateway saw (OTLP access log export, decoded by the visualizer).
	KindGatewayAccess Kind = "gateway_access"
)

// Sources.
const (
	SourceOrchestrator = "orchestrator"
	SourceAgent        = "agent"
	SourceGateway      = "gateway"
	SourceVisualizer   = "visualizer"
)

// Via says which path a message took.
const (
	ViaGateway = "gateway"
	ViaDirect  = "direct"
)

// Event is one line of the event log. Producers leave ID, TS and Session
// empty; the visualizer assigns them on receipt (one clock for replay).
type Event struct {
	ID         int64     `json:"id"`
	TS         time.Time `json:"ts"`
	ProducedTS time.Time `json:"produced_ts,omitzero"`
	Session    string    `json:"session,omitempty"`

	RunID    string `json:"run_id,omitempty"`
	Scenario string `json:"scenario,omitempty"` // scenario code, e.g. "sc"
	Step     string `json:"step,omitempty"`     // orchestrator plan step
	Source   string `json:"source"`
	Kind     Kind   `json:"kind"`

	From string `json:"from,omitempty"` // agent id (or orchestrator id) that sends
	To   string `json:"to,omitempty"`   // agent id that receives; "llm" for the model
	Via  string `json:"via,omitempty"`

	Method    string `json:"method,omitempty"` // JSON-RPC method, HTTP method for card fetches
	TaskID    string `json:"task_id,omitempty"`
	ContextID string `json:"context_id,omitempty"`
	StateFrom string `json:"state_from,omitempty"`
	StateTo   string `json:"state_to,omitempty"`

	Route      string `json:"route,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Corr       string `json:"corr,omitempty"` // pairs a request with its response and stream frames

	Payload          json.RawMessage `json:"payload,omitempty"`
	PayloadTruncated bool            `json:"payload_truncated,omitempty"`
	Note             string          `json:"note,omitempty"`
}

// DefaultPayloadLimit is the default payload cap in bytes.
const DefaultPayloadLimit = 16 * 1024

// WithPayload sets Payload from raw JSON bytes (or a JSON string for non-JSON
// bytes), truncating to limit bytes.
func (e *Event) WithPayload(raw []byte, limit int) *Event {
	e.Payload, e.PayloadTruncated = Truncate(raw, limit)
	return e
}

// WithValue marshals v as the payload.
func (e *Event) WithValue(v any, limit int) *Event {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"marshal_error": err.Error()})
	}
	return e.WithPayload(b, limit)
}

// Truncate returns raw as a JSON value no larger than about limit bytes. Valid
// JSON within the limit is kept as is. Anything else becomes a JSON object
// {"truncated": true, "bytes": n, "head": "<first bytes>"}.
func Truncate(raw []byte, limit int) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	if limit <= 0 {
		limit = DefaultPayloadLimit
	}
	if len(raw) <= limit && json.Valid(raw) {
		return json.RawMessage(append([]byte(nil), raw...)), false
	}
	head := raw
	if len(head) > limit {
		head = head[:limit]
	}
	if len(raw) <= limit {
		b, _ := json.Marshal(map[string]any{"text": string(raw)})
		return b, false
	}
	b, _ := json.Marshal(map[string]any{"truncated": true, "bytes": len(raw), "head": string(head)})
	return b, true
}

// ShortState turns "TASK_STATE_INPUT_REQUIRED" into "input-required". Short
// names are what the event log and the browser use.
func ShortState(s string) string {
	s = strings.TrimPrefix(s, "TASK_STATE_")
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}
