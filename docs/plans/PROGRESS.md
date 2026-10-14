# Progress

Plan: [2026-10-14-master-plan.md](2026-10-14-master-plan.md). One row per milestone, updated when it lands.

| # | Milestone | Status | Commit |
|---|---|---|---|
| M0 | Scaffolding: go.mod, copied helpers, Makefile, web skeleton, docs | done | c0cc234 |
| M1 | agentgateway + hello agent, card rewrite verified | done | 3091f7d |
| M2 | scenario schema, generator, scripted executor | done | 2ef8ab9 |
| M3 | orchestrator, event log, visualizer SSE | done | c41c3ec |
| M4 | map, arcs, panels, replay | done | 6db95b6 |
| M5 | gateway policies, Jaeger, gateway view | done | 3dc6cd9 |
| M6 | Ollama + LLM agents through the gateway | done | 3b5d777 |
| M7 | all scenarios, e2e, screenshots, README | done | ab70359 |
| M8 | Fable code review; critical/high findings fixed test-first (red commit, then green commit) | done | f576cd0, see docs/reviews/2026-10-14-fable-code-review.md |

MCP extension, plan: [2026-10-14-mcp-plan.md](2026-10-14-mcp-plan.md).

| # | Milestone | Status | Commit |
|---|---|---|---|
| M9 | Spike: MCP route, multiplexing, progress, authorization through agentgateway | done | (this commit) |
| M10 | Scenario schema for MCP servers, `cmd/mcpserver`, generated routes and compose | open | |
| M11 | Shared wire capture, MCP client, scripted tool/resource steps, events | open | |
| M12 | LLM tool loop over MCP (prompts/get, tools/list, tool_calls) | open | |
| M13 | Visualizer MCP decoding, UI hexagons, MCP flights, panels | open | |
| M14 | e2e, screenshots, sample, docs | open | |
| M15 | Fable code review of the MCP changes, TDD fixes | open | |

## Deviations from the plan

- All five scenarios were written in M2, not M7: the generator needs them, and they are data.
- Plan steps use `call: send | stream | push` instead of method names. A2A v1.0 method names on the
  wire are `SendMessage`, `SendStreamingMessage`, `GetTask`; the UI shows what is on the wire.
- The LLM-backed agent is always the scenario's streaming agent, so the model's answer is streamed.
- Gateway retries are demonstrated with agent-side chaos (`X-Chaos: 1` makes the streaming agent answer
  503 to the first attempt of a message) instead of `random()` in the gateway, so runs stay deterministic.
- The gateway LLM model entry has no fixed model: agents send `$LLM_MODEL` and the gateway passes it on.
- Scenario colours (light-mode categorical slots, all-pairs validated with the dataviz validator):
  sc blue `#2a78d6`, ex yellow `#eda100`, pm green `#008300`, tr magenta `#e87ba4`, kh aqua `#1baf7a`.
  Normal-vision floor passes (15.6); CVD separation is in the 6–8 warn band, so every pin, arc label
  and event row also carries the two-letter scenario code (secondary encoding).

- `leaflet.markercluster` is not used. Thirty pins with per-scenario offsets stay readable without
  clustering, and arcs never have to re-anchor to a cluster icon.
- npm 10.9 crashes building the ideal tree from scratch (`edgesOut` of null). The web lockfile was
  seeded from `cp_amap_pci/web/package-lock.json`; `npm install` then pruned the unused packages.

- M3 has no browser event list; the real UI lands in M4. M3 is verified with `make tail-events` and
  `curl localhost:8095/api/events`.
- The orchestrator's wire capture is an `http.RoundTripper` (not a2a-go call interceptors), so the event
  log shows the JSON-RPC bodies and SSE frames exactly as sent.
- agentgateway moved into the generated `compose.agents.yml`: it caches backend addresses, so it must
  restart when agent containers are recreated (`depends_on: {condition: service_healthy, restart: true}`).
  After recreating a single agent by hand, run `docker compose restart agentgateway`.

- The gateway is drawn as a hub floating at the top of the map, not at a geographic place: every A2A
  and LLM call visibly goes up into it and back down; push notifications bypass it. Flights are drawn on
  one canvas per frame (not Leaflet polylines). The default base map is the bundled Natural Earth
  110m outline (offline, public domain); OpenStreetMap tiles are an opt-in toggle.
- Events are ordered by `produced_ts` (producer clock; all containers share the host clock), not by
  receipt. Live mode plays 1.5 s behind real time so late batches still land in order.
- Zoom buttons (World, Europe, Central EU) were added: 18 of the 30 pins are in Europe.

- One W3C trace per run: the orchestrator sends `traceparent` with the run's trace id on every call and
  agents pass it on to their LLM call, so Jaeger shows a whole run as one trace (verified: 26 spans for a
  service-case run, the retried upstream attempt appears twice, the denied call has no upstream span).
  The payload panel links to it ("whole run in Jaeger").

- Ollama is installed per user without root (`scripts/ollama-user-install.sh`), not with the official
  root script: `sudo` with a password is not permitted from the agent session. It runs as a systemd user
  service bound to the Docker bridge (172.17.0.1), so it is not exposed on the LAN. Details and the
  CPU-vs-iGPU measurement are in `docs/ollama.md` (CPU wins: 26.3 vs 22.4 tokens/s).
- `/no_think` does not work on Ollama 0.40's OpenAI endpoint; the client sends `reasoning_effort: none`.

- e2e: 7 API + 7 UI Playwright tests (14 passed, ~1.6 min) against the running stack; a separate
  `screenshots` project writes `docs/screenshots/`. The screenshot script failed once on a first attempt
  (artefacts were overwritten by the passing second run), so it may be flaky.
- The task panel marks states a blocking client never saw (input-required → completed implies working)
  as inferred, drawn dashed.

## Verified facts (M1 spike, agentgateway v1.6.0 + a2a-go v2.6.0)

- Path prefix `/agents/<id>` plus `urlRewrite.path.prefix: /` works with A2A card rewriting: the card
  fetched at `localhost:8000/agents/hello/.well-known/agent-card.json` advertises
  `http://localhost:8000/agents/hello/invoke`. The host part follows the request `Host`.
- JSON-RPC method names are the A2A v1.0 ones: `SendMessage`, `SendStreamingMessage`, `GetTask`, ...
  States are `TASK_STATE_*`, roles `ROLE_USER` / `ROLE_AGENT`.
- `SendStreamingMessage` passes through the gateway unbuffered (500 ms gaps preserved).
- Access log JSON has `route`, `endpoint`, `a2a.method`, `a2a.response.outcome`, `a2a.result.kind`,
  `a2a.task.state`, `a2a.context.id`, `duration`, plus the `add:` fields `run_id` and `caller`.
- `authorization.rules` with `json(request.body).params.message.metadata.skill` and `default()` returns
  403 for denied skills; `GET` (card) stays allowed.
- `delay.duration` as a CEL expression in milliseconds works.
- `frontendPolicies.accessLog.otlp {host, protocol: http, path}` posts `application/x-protobuf` OTLP logs
  (scope `agentgateway.access`).
- `llm: {gateways: llm, models: [{name: "*", provider: ollama, params: {model, baseUrl}}]}` is accepted.
- `config.adminAddr: 0.0.0.0:15000` exposes the gateway UI to the host.
- Request headers reach the a2a-go executor as `ExecutorContext.ServiceParams` (lower-cased keys).

- agentgateway expands `$VARS` everywhere in its config file, comments included. An unset variable makes
  the reload fail and the gateway keeps the previous config. A test in `cmd/atlasgen` guards this.
- The gateway hot-reloads `config.yaml` when the file changes.
- The access log's OTLP record carries `retry.attempt` (1 after one retry) and `trace.id`.

## Verified facts (M9 MCP spike, agentgateway v1.6.0 + go-sdk v1.8.0)

Two Go MCP servers (Streamable HTTP, stateful) behind one route with `backends: [{mcp: {statefulMode: stateful, targets: [...]}}]`:

- Multiplexing prefixes names with `<target>_`: tools `historian_read_trend`, prompts `historian_diagnose`.
  Resource URIs become `<target>+<uri>` (`historian+historian://sc/manual`); reading the original URI fails (500),
  the rewritten one works. Clients must use the names from `*/list`.
- Progress notifications (`notifications/progress` with the client's progress token) pass through the gateway.
  `tools/call` answers come back as `text/event-stream`, with progress frames before the result.
- `mcpAuthorization.rules` (CEL) see both `mcp.tool.*` and `request.headers`, e.g.
  `!(has(mcp.tool) && mcp.tool.name.startsWith("restricted_")) || request.headers["x-caller"] == "sc-admin"`.
  `mcp.tool.name` is the unprefixed upstream name.
- A denied tool is removed from `tools/list`; calling it anyway returns **HTTP 400** with JSON-RPC error
  "Unknown tool: erp_restricted_shutdown" (access log `reason: MCP`). It never reaches the server. There is no 403.
- The HTTP-level `authorization` policy on the same route returns 403 at `initialize` for a foreign caller.
- Request headers (`X-Caller`, ...) are forwarded to the upstream MCP server.
- Access log per MCP request: `mcp.method.name` (initialize, notifications/initialized, tools/list, tools/call,
  resources/read, prompts/get, ...), `mcp.target`, `mcp.resource.uri`, `mcp.session.id`, `http.status`, `duration`,
  `error`/`reason`. The tool name is not logged by default; `accessLog.add: {mcp_tool: 'mcp.tool.name'}` adds it.
- qwen3:1.7b function calling (OpenAI `tools`, `reasoning_effort: none`) picked a sensible tool 3/3 times.

## Notes for resuming

- Reference clones of a2a-go and agentgateway are not part of the repo. Re-clone them into a scratch
  directory when you need to read the SDK or gateway source.
