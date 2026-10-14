# Event log contract

The visualizer stores one JSON object per line in `data/sessions/<session>.jsonl` and streams the same
objects over SSE (`GET /api/events/stream`, event name `atlas`, id `<session>/<n>`). Go type:
`internal/events.Event`. Browser mirror: `web/src/events/schema.ts`.

## Fields

| field | meaning |
|---|---|
| `id` | Monotonic per session, assigned by the visualizer. |
| `ts` | Receipt time at the visualizer. |
| `produced_ts` | When the producer created the event. All containers share the host clock, so the UI orders by this. Gateway events use the OTLP log record time (end of the request). |
| `session` | Visualizer session (one per visualizer start). |
| `run_id`, `scenario`, `step` | Which run (`sc-154458-1`), scenario code (`sc`) and plan step. |
| `source` | `orchestrator`, `agent`, `gateway` or `visualizer`. |
| `kind` | See below. |
| `from`, `to` | Agent ids. `agentgateway` for refusals, `llm` for the model. |
| `via` | `gateway` or `direct` (push notifications go straight to the orchestrator webhook). |
| `method` | JSON-RPC method on the wire (`SendMessage`, `SendStreamingMessage`, `GetTask`), or an HTTP method and path. |
| `task_id`, `context_id` | A2A task and context ids. |
| `state_from`, `state_to` | Short task states: `submitted`, `working`, `input-required`, `completed`, `failed`, `canceled`, `rejected`. Step results also use `denied`, `error`, `unexpected`. |
| `route`, `http_status`, `duration_ms` | Gateway route (= agent id), HTTP status, duration. |
| `corr` | Correlates a request with its response and stream frames (and a breakpoint with its release). |
| `payload` | The raw JSON: request/response body, SSE frame, agent card, push body, access-log attributes. Capped at `EVENT_PAYLOAD_MAX_BYTES`; larger payloads become `{"truncated": true, "bytes": n, "head": "..."}` and `payload_truncated` is set. |
| `note` | One human sentence. |

## Kinds

| kind | source | from → to | what it shows |
|---|---|---|---|
| `run_started`, `run_finished` | orchestrator | orchestrator | A run begins (payload: options) or ends (note: closing line). |
| `step_started`, `step_finished` | orchestrator | orchestrator → agent | Narration of a plan step; `step_finished.state_to` is the result. |
| `breakpoint`, `gate_released` | visualizer | orchestrator → agent | Breakpoint mode: the orchestrator waits until someone continues. |
| `card_fetch` | orchestrator | orchestrator → agent | `GET /.well-known/agent-card.json` through the gateway; payload has the card with the rewritten URL. |
| `rpc_request` | orchestrator | orchestrator → agent | The JSON-RPC request body as sent. |
| `rpc_response` | orchestrator | agent → orchestrator | The JSON-RPC response, or "stream closed" for streaming calls. |
| `stream_event` | orchestrator | agent → orchestrator | One SSE frame of `SendStreamingMessage`. |
| `task_state` | orchestrator | agent → orchestrator | A task changed state (derived from responses, frames and pushes; never backwards). |
| `policy_denied` | orchestrator | agentgateway → orchestrator | HTTP 403 from the gateway's authorization policy. |
| `push_received` | orchestrator | agent → orchestrator | Push notification on the webhook (`via: direct`). |
| `agent_request` | agent | orchestrator → agent | A request as it arrived behind the gateway (headers + body). |
| `agent_work` | agent | agent | A script step: the status text the agent is "saying". |
| `artifact` | agent | agent | The agent produced an artifact. |
| `fault_injected` | agent | agent | Chaos: the agent answered 503 to the first attempt; the gateway retries. |
| `llm_request`, `llm_response`, `llm_unavailable` | agent | agent ↔ llm | The LLM step through the gateway's LLM route, or the scripted fallback. |
| `mcp_request` | agent | agent → MCP server (or `agentgateway` for the session handshake and lists) | A JSON-RPC request to the scenario's MCP route `/mcp/<code>`: `server/discover`, `initialize`, `tools/list`, `tools/call`, `resources/read`, `prompts/get`. |
| `mcp_response` | agent | MCP server → agent | The JSON-RPC result (JSON body or the result frame of the SSE stream). |
| `mcp_progress` | agent | MCP server → agent | A `notifications/progress` frame of a slow tool, inside the call's SSE stream. |
| `mcp_denied` | agent | agentgateway → agent | The gateway refused an MCP call: HTTP 400 "Unknown tool" for write tools (hidden by the MCP policy), 403 for callers from another scenario. |
| `mcp_server_call` | agent (MCP server) | agent → MCP server | A `tools/call`, `resources/read` or `prompts/get` as it arrived at the MCP server behind the gateway. |
| `llm_tool_choice` | agent | llm → agent | The model asked for a tool call (name and arguments); the agent runs it over MCP. |
| `gateway_access` | gateway | caller → route | One gateway access-log record (OTLP): method, status, duration, task state, `retry.attempt`, `trace.id`. |
