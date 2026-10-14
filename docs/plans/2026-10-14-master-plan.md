# agent-atlas — A2A on a world map, behind agentgateway

## Context

Goal: learn A2A (Agent2Agent protocol v1.0) by *watching* it. Several small Go agents sit at real
locations on a world map; orchestrators delegate work to them exclusively through agentgateway.
A visualizer records every protocol event into a JSONL log and streams it to a browser UI that shows
the map (pins + animated arcs), the real JSON-RPC payloads, the task state machine, and a replay
engine (pause / step / scrubber / speed) slow enough for a human. Map = stage, payload panel = textbook.

Decided with the user:
- **Scope: all five stages** (gateway hello world → scripted agents → orchestrator + log + replay UI →
  gateway policies made visible (CEL authz, delay, retry, Jaeger) → one LLM-backed agent per scenario
  via **Ollama through the gateway's LLM route**).
- **Five scenarios**, selectable, and **all five must be able to run at the same time**, colour-coded
  per scenario so the map stays readable.
- Frontend: **Next.js + Leaflet** (reuse from `cp_amap_pci`). Agents: **Go + a2a-go v2** (A2A v1.0).
- **Ollama native on the host** (I install it with sudo during M6; the password was given in chat and
  is never written to any file), model **`qwen3:1.7b`**. Machine: Intel Core Ultra 5 335, Intel iGPU,
  8 cores, 30 GB RAM, no NVIDIA → CPU inference expected; Vulkan is tried and measured.
- Everything local in docker compose, one `make up`. Target repo `/home/tranmh/work/agent-atlas` is empty.

Verified facts the plan relies on (sources were read, not guessed):
- agentgateway **v1.6.0** (`cr.agentgateway.dev/agentgateway:v1.6.0`, `-f /config.yaml`): config style
  `gateways:` + `routes:` (`binds:` deprecated). `routes[].policies.a2a: {}` marks A2A traffic; the gateway
  recognises `/.well-known/agent-card.json` and `agent.json`, and rewrites `supportedInterfaces[].url`
  so clients never bypass it. With `matches.path.pathPrefix` + `urlRewrite.path.prefix` the rewrite maps
  the interface path back under the matched prefix (`crates/agentgateway/src/a2a/mod.rs`
  `public_interface_url`). Access log (JSON) carries `a2a.method`, `a2a.response.outcome`,
  `a2a.result.kind`, `a2a.task.state`, `a2a.context.id`, `a2a.response.error_code`, `route`, `duration`;
  `frontendPolicies.accessLog.add` takes CEL; `frontendPolicies.accessLog.otlp {host, protocol: http, path}`
  exports logs over OTLP/HTTP. Policies: `authorization.rules[] {allow|require|deny: cel}` with
  `request.headers`, `request.body` + `json()`; `delay.duration`; `retry {attempts, backoff, codes}`;
  `directResponse.conditional`; `cors`; `frontendPolicies.tracing {host: jaeger:4317, randomSampling: true}`.
  LLM: top-level `llm.models: [{name: "*", provider: ollama, params: {model, baseUrl: http://host:11434/v1}}]`
  served at `/v1/chat/completions` (OpenAI-compatible).
- a2a-go **v2.6.0** (`github.com/a2aproject/a2a-go/v2`, Go ≥ 1.25; host has Go 1.26, Node 22, Docker 29,
  Compose 2.40): server `a2asrv.NewHandler(executor, opts...)`, `a2asrv.NewJSONRPCHandler`,
  `a2asrv.NewStaticAgentCardHandler` at `a2asrv.WellKnownAgentCardPath`; executor
  `AgentExecutorFunc(func(ctx, ec *ExecutorContext) iter.Seq2[a2a.Event, error])` yielding
  `a2a.NewSubmittedTask`, `a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking|InputRequired|Completed|Failed, msg)`,
  `a2a.NewArtifactEvent`; multi-turn via `ec.StoredTask`; push via
  `a2asrv.WithPushNotifications(push.NewInMemoryStore(), push.NewHTTPPushSender(&push.HTTPSenderConfig{AllowPrivateNetworks: true}))`
  (SSRF guard blocks 172.x otherwise); interceptors `a2asrv.WithCallInterceptors` / `a2aclient.WithCallInterceptors`
  (`Before/After`). Client: `agentcard.NewResolver(httpClient).Resolve(ctx, fullCardURL)` (a non-root path is
  fetched as the complete card URL), `a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(httpClient), ...)`,
  `SendMessage`, `SendStreamingMessage` (iter.Seq2), `GetTask`, `SendMessageConfig.PushConfig{URL, Token}`;
  webhook receives header `A2A-Notification-Token`. `a2aevent.ApplyUpdate`, `a2aevent.IsFinal`.

## Topology (the concurrency decision)

**One process per agent, generated from the scenario files.** 5 scenarios × (1 orchestrator + 4 worker
agents) = 25 small Go containers (≈10 MB RSS each) + agentgateway + jaeger + visualizer + web.
Rationale: "an agent is a process at a location, reachable only through the gateway" is the mental
model being taught; concurrency then needs no slot juggling; per-agent gateway routes and policies stay
explicit and readable in one `config.yaml`. The compose fragment and the gateway config are **generated**
by `cmd/atlasgen` from `scenarios/*.yaml` and checked in (`make gen`; a test fails on drift).
Only one compose service per binary carries `build:`; the rest reference the same `image:` tag.

Gateway routing: single port **3000**, one route per worker agent, `pathPrefix: /agents/<agent-id>`,
`urlRewrite: {path: {prefix: /}}`, backend `<agent-id>:8080`. LLM on a second gateway `llm` port **3100**.
Orchestrators are A2A clients only (not routed), ports 8090.

Agent ids: `<code>-<role>` with codes `sc` service case, `ex` export order, `pm` predictive maintenance,
`tr` travel booking, `kh` expert finder. Example: `sc-diagnosis`, `pm-orchestrator`.

## Repository layout

```
agent-atlas/
├── go.mod (github.com/tranmh/agent-atlas, go 1.26)  Makefile  docker-compose.yml  compose.agents.yml (generated)
├── .env.example  .dockerignore  CLAUDE.md  CODEMAP.md  README.md
├── cmd/agent  cmd/orchestrator  cmd/visualizer  cmd/atlasgen
├── internal/httpx        copied as-is from cp_amap_pci: sse.go env.go health.go json.go middleware.go
├── internal/scenario     schema.go (YAML types + Validate), load.go, embed.go (//go:embed scenarios), colors (hue per scenario)
├── internal/events       event.go (schema + Kind consts), reporter.go (async batching POST), truncate.go
├── internal/a2ax         card.go, transport.go (RoundTripper adds X-Caller, X-Run-Id, X-Chaos), interceptors.go (client+server reporting), classify.go
├── internal/script       runner.go (step executor, SCRIPT_SPEED, input-required resume, chaos), steps.go, *_test.go
├── internal/llm          client.go (OpenAI-compatible chat to gateway :3100), fallback
├── internal/agent        server.go (mux: card, /invoke, /healthz, /chaos middleware), executor.go
├── internal/orchestrator plan.go (runs plan steps), delegate.go, push.go (webhook), gate.go (breakpoints)
├── internal/eventlog     store.go (JSONL append per session, EventsAfter(id), index), broadcaster wiring
├── internal/visualizer/httpapi  router.go events.go (POST + SSE) runs.go sessions.go scenarios.go gate.go otlp.go
├── scenarios/            service-case.yaml export-order.yaml predictive-maintenance.yaml travel-booking.yaml expert-finder.yaml
├── deploy/agentgateway/config.yaml (generated)   deploy/jaeger/
├── build/go.Dockerfile (copied)   web/Dockerfile (copied, OIDC args dropped)
├── web/                  Next.js 15.5 + React 19 + Leaflet 1.9 + Tailwind 3 + Vitest 4
├── e2e/                  Playwright 1.56.1 (api + ui projects)
├── data/sessions/        JSONL output, bind-mounted, gitignored;  data/samples/*.jsonl committed (fixtures + README)
└── docs/plans/2026-10-14-master-plan.md  docs/plans/PROGRESS.md  docs/events.md  docs/scenarios.md  docs/ollama.md
```

## Scenario file (contract shared by atlasgen, agent, orchestrator, visualizer, UI)

```yaml
id: service-case          code: sc        hue: 210        title: ANDRITZ service case
description: Ticket from Foz do Iguaçu → Service Desk Göppingen → specialists
orchestrator: { id: sc-orchestrator, name: Service Desk, city: Göppingen, lat: 48.7026, lon: 9.6527 }
agents:
  - id: sc-diagnosis    name: Diagnosis     city: Göppingen      lat: 48.7026  lon: 9.6527
    feature: streaming        # streaming | multi_turn | long_task_push | restricted
    mode: llm                 # scripted | llm  (llm falls back to script when Ollama is unreachable)
    skills: [{id: analyze-fault, name: Analyze fault memory}]
    llm: { system: "You are a hydro turbine diagnostics engineer. Answer in 3 sentences.", max_tokens: 200 }
    script:
      - { say: "Reading fault memory…",            state: working, wait: 2.5s }
      - { say: "Correlating with vibration data…", state: working, wait: 2.5s }
      - { llm: "Give a diagnosis for: {{input}}" }               # only in mode: llm; else skipped
      - { artifact: { name: diagnosis, data: { fault: "bearing B3 overheating", confidence: 0.87 } } }
      - { state: completed, say: "Diagnosis complete" }
  - id: sc-spare-parts  feature: multi_turn   city: Rotterdam  …
    script:
      - { say: "Looking up bill of materials…", state: working, wait: 2s }
      - { input_required: "Which serial number is affected?" }   # runner stops; resumes on next message
      - { say: "Checking stock for {{input}}", state: working, wait: 2s }
      - { state: completed, artifact: { data: { part: "SKF 23148", stock: 3 } } }
  - id: sc-logistics    feature: long_task_push  city: Singapore …   (8–10 steps × 2–3 s)
  - id: sc-field-service feature: restricted     city: Foz do Iguaçu …
    skills: [{id: schedule-visit}, {id: restricted-shutdown-plant}]
plan:
  - { step: diagnose, agent: sc-diagnosis,     call: message/stream, text: "Ticket #4711: turbine T3 bearing alarm", skill: analyze-fault }
  - { step: parts,    agent: sc-spare-parts,   call: message/send,   text: "Need bearing for T3", on_input_required: { text: "SN-T3-2019-0042" } }
  - { step: ship,     agent: sc-logistics,     call: message/send,   push: true, text: "Ship SKF 23148 to BR", wait_for: push }
  - { step: shutdown, agent: sc-field-service, call: message/send,   skill: restricted-shutdown-plant, expect: denied }
  - { step: visit,    agent: sc-field-service, call: message/send,   skill: schedule-visit }
```

`skill` travels as `message.metadata.skill` (the CEL rule reads it with `json(request.body)`), `run_id` as
header `X-Run-Id` and in `message.metadata`. Validation: unique ids, lat/lon ranges, exactly one
`input_required` for `multi_turn`, plan agents exist, exactly one `mode: llm` agent per scenario.

The five scenarios (each has streaming / multi-turn / long+push / restricted / one LLM agent):

| code | scenario | orchestrator | streaming | multi-turn | long task + push | restricted | LLM agent |
|---|---|---|---|---|---|---|---|
| sc | ANDRITZ service case | Service Desk, Göppingen | Diagnosis, Göppingen | Spare parts, Rotterdam | Logistics, Singapore | Field service, Foz do Iguaçu (`restricted-shutdown-plant` denied) | Diagnosis |
| ex | Export order & customs | Sales Hub, Vienna | Credit check, Vienna | Export control, Brussels (end-user certificate) | Customs broker, Rotterdam | Shipping, Singapore (`restricted-hazardous-cargo`) | Order summary (Sales Hub side agent, Vienna) |
| pm | Predictive maintenance | Monitoring Center, Graz | Signal analysis, Graz | Maintenance planner, Helsinki (maintenance window) | Parts & logistics, Shanghai | Remote intervention, Äänekoski (`restricted-plc-write`) | Signal analysis (root-cause hypothesis) |
| tr | Business travel booking | Travel desk, Munich | Flights, Frankfurt | Hotel, Munich (dates) | Visa service, New Delhi | Expense policy, Munich (`restricted-business-class`) | Itinerary summary (Flights) |
| kh | Global expert finder | Knowledge hub, Graz | Document search, Graz | Expert matching, Vienna (urgency) | Translation, Tokyo | HR directory, Graz (`restricted-salary-lookup`) | Document search (answer drafting) |

## Event log contract (`docs/events.md`, Go `events.Event`, Zod mirror `web/src/events/schema.ts`)

One JSON object per line; `id` assigned by the visualizer (monotonic per session), `ts` stamped on receipt
(producer time kept as `produced_ts`, so replay timing uses one clock):

```json
{"id":42,"ts":"…","session":"s-20261014-100000","run_id":"sc-r1","scenario":"sc","source":"orchestrator",
 "kind":"rpc_request","from_agent":"sc-orchestrator","to_agent":"sc-diagnosis","via":"gateway",
 "method":"message/stream","task_id":"…","context_id":"…","state_from":"submitted","state_to":"working",
 "route":"sc-diagnosis","http_status":200,"duration_ms":12,"payload":{…},"payload_truncated":false,
 "note":"Service Desk asks Diagnosis to analyze the fault memory"}
```

Kinds: `run_started run_finished card_fetch rpc_request rpc_response stream_event task_state artifact
push_received policy_denied retry delay breakpoint llm_request llm_response llm_unavailable gateway_access`.
Payload capped at 16 KB. Capture points:
- `a2ax.transport` (orchestrator http client): `card_fetch` for the well-known GET (payload shows the rewritten URL).
- `a2ax` client interceptor: `rpc_request` / `rpc_response` / `policy_denied` (403 from gateway) with duration.
- orchestrator stream loop: each event from `SendStreamingMessage` → `stream_event`; state change → `task_state`.
- `a2ax` server interceptor on every agent (`source: agent`): shows what arrived *behind* the gateway.
- `script.Runner`: `task_state`, `artifact`, `llm_*` with `note` = the spoken status text.
- orchestrator webhook `POST /push/{run}/{task}` → `push_received` (token checked).
- **gateway view**: `frontendPolicies.accessLog.otlp {host: visualizer:8095, protocol: http, path: /otlp/v1/logs}`
  with `add: {run_id: 'request.headers["x-run-id"]', caller: 'request.headers["x-caller"]'}`; the visualizer
  decodes OTLP/HTTP (protobuf via `go.opentelemetry.io/proto/otlp/collector/logs/v1`, JSON via protojson)
  into `gateway_access` events. Two access-log lines for one client `rpc_request` on a retry route → `retry`.
  Fallback if the export misbehaves in M5: Jaeger deep link only, interceptor events already tell the story.
- `events.Reporter`: buffered channel, batches `POST /api/events` every 200 ms / 50 events, never blocks the protocol path.

## Gateway config (generated; `deploy/agentgateway/config.yaml`)

Per worker route: `a2a: {}`, `cors`, plus by `feature`:
- `streaming` → `retry: {attempts: 3, backoff: 500ms, codes: [503]}`. Chaos is **agent-side**: with header
  `X-Chaos: 1` the agent's `/chaos` middleware answers 503 to the first attempt per `X-Run-Id`, so the gateway
  retry is real and deterministic (no `random()`).
- `long_task_push` → `delay: {duration: "2s"}` (visible as `delay` in the gateway access log duration).
- `restricted` → `authorization.rules:`
  `- allow: 'request.method == "GET"'` (card fetch always works)
  `- allow: 'request.headers["x-caller"].endsWith("-orchestrator") && !coalesce(json(request.body).params.message.metadata.skill, "").startsWith("restricted-")'`
- `frontendPolicies.tracing: {host: jaeger:4317, randomSampling: true}`, `config.logging.format: json`,
  access log `add` + `otlp` as above, `ui` on 15000 (host 15000).
- `gateways: {default: {port: 3000}, llm: {port: 3100}}`; `llm.models: [{name: "*", provider: ollama,
  params: {model: qwen3:1.7b, baseUrl: http://host.docker.internal:11434/v1}}]` attached to the `llm` gateway
  (key to verify in M6: `llm.gateways`; fallback `llm.port`). Compose: `extra_hosts: ["host.docker.internal:host-gateway"]`.

## Services

- **agent** (`cmd/agent`, env `AGENT_ID`, `VISUALIZER_URL`, `SCRIPT_SPEED`, `LLM_BASE_URL`, `LLM_MODEL`):
  loads its scenario entry, serves `/.well-known/agent-card.json`, `/invoke`, `/healthz`; card has
  `Capabilities{Streaming: true, PushNotifications: true}` and the scenario skills. Executor = `script.Runner`:
  emits `NewSubmittedTask` only when `ec.StoredTask == nil`, sleeps `wait / speed` per step, stops at
  `input_required`, resumes after it when `StoredTask != nil` substituting `{{input}}`. `llm` step calls the
  gateway with 20 s timeout, `/no_think` in the system prompt (qwen3), `max_tokens` ≤ 200; on error emits
  `llm_unavailable` and continues scripted.
- **orchestrator** (`cmd/orchestrator`, env `ORCH_ID`, `GATEWAY_URL=http://agentgateway:3000`): `POST /run
  {run_id, breakpoints, chaos}` (several runs concurrently, goroutine per run), `GET /runs/{id}`, `POST /push/{run}/{task}`.
  Per plan step: optional gate → resolve card at `GATEWAY_URL/agents/<id>/.well-known/agent-card.json` →
  client with reporting interceptor → `message/send` or `message/stream`; `on_input_required` → second message
  with `TaskID`/`ContextID`; `push: true` → `PushConfig{URL: http://<orch>:8090/push/<run>/<task>, Token}`,
  wait for webhook, then `GetTask`; `expect: denied` → 403 counts as success of the demo step.
- **visualizer** (`cmd/visualizer`, 8095): `POST /api/events`, `GET /api/events/stream?session=` (SSE,
  subscribe-before-replay, `Retry(3000)`, `Last-Event-ID` → `EventsAfter`, heartbeat 15 s — adapted from
  `internal/pci/httpapi` `stream()`), `GET /api/sessions`, `GET /api/sessions/{id}/events`, `GET /api/scenarios`,
  `POST /api/runs {scenario|all, breakpoints, chaos}` (fans out to the orchestrators, 2 s stagger for `all`),
  `GET /api/gate` (long-poll), `POST /api/gate/release`, `POST /otlp/v1/logs`, `/healthz`.
- **web** (3000 on host, `NEXT_PUBLIC_VISUALIZER_URL`), **agentgateway** (host 8000 → 3000, 8100 → 3100, 15000),
  **jaeger** (`jaegertracing/all-in-one`, `COLLECTOR_OTLP_ENABLED=true`, 16686).

## Web UI

Reuse as-is from `cp_amap_pci/web`: `src/map/sse.ts` (+ test), `leaflet-setup.ts`, `MapShell.tsx`,
`hooks/useAsync.ts`, `map/html.ts`, `next.config.ts`, `tsconfig.json`, `vitest.*`, `postcss.config.mjs`,
`.eslintrc.json` boundary rule (`src/map` and `src/replay` stay React/Zod-free), `app/api/health/route.ts`,
`useServices.tsx` + `services.ts` DI with fixtures mode, `test/render.tsx`, `Dockerfile` (drop OIDC args).

Adapt: `map-controller.ts` → `AtlasMapController` (`setAgents`, `setArcs`, `highlight`, same mount /
cluster / ResizeObserver / destroy) + new `src/map/arcs.ts` (pure quadratic-Bezier great-circle-ish arc,
`pointAt(t)`; rendered as `L.polyline` in its own pane with `stroke-dashoffset` animation and a moving
`L.circleMarker` driven by `requestAnimationFrame`); `pins.ts`/`colors.ts`/`popup.ts`; `status-feed.ts` →
`src/events/feed.ts` (no auth); `AssetsMapApp.tsx` layout → `AtlasApp.tsx` (left: scenario chips + run
controls + filtered event list; centre: map + legend; right drawer: tabs Payload | Task state | Gateway);
`PlantDetailPanel.tsx` → `PayloadPanel.tsx`; `ConnectionIndicator`, `Legend`, `Sparkline`, `globals.css`.

New: `src/replay/engine.ts` (framework-free: `events[]`, `cursor`, `mode live|replay`, `speed 0.25–8`,
`tick/step/seek/pause`; live appends from SSE, pause switches to replay on the same buffer),
`src/replay/reduce.ts` (pure reducer → `{agents, arcs, tasks, runs}` with checkpoints every 100 events so
scrubbing is cheap), `TaskStatePanel.tsx` (SVG state graph, current state highlighted, history),
`ReplayBar.tsx`, `GatePrompt.tsx`, `ScenarioChips.tsx`. Fixtures mode replays `public/samples/*.jsonl` with
no backend (Vitest + README GIF). Offline map: bundled Natural Earth 110m GeoJSON as base layer, OSM tiles on
top, hidden on tile errors or `NEXT_PUBLIC_OFFLINE_MAP=1`.

**Colour coding for concurrent runs** (follow the `dataviz` skill when choosing the palette):
- Scenario = **hue** (5 distinct hues from the scenario files), applied to pins, arcs, event rows, badges.
- Protocol = **shape, not colour**: arc stroke solid `message/send`, dashed `message/stream`, dotted push
  webhook (labelled `via: direct`), red cross-hatched `policy_denied`; direction by the moving dot.
- Pin: hue fill, glyph by role (orchestrator ★, worker ●), ring by task state (idle / working pulse /
  input-required blink / completed / failed). Shared cities (Graz, Vienna, Munich, Rotterdam, Singapore)
  get a deterministic small offset per scenario index and spiderfy on click; cluster icons are hue pies.
- Scenario chips: click = solo, shift-click = mute; everything else dims to 25 % opacity. Event list,
  payload panel and task panel always show the scenario badge.

## Compose, Makefile, env

`docker-compose.yml` (static: agentgateway, jaeger, visualizer, web, `x-go-service` anchor, healthchecks
`["CMD","/app/<svc>","--healthcheck"]`, `depends_on: service_healthy`, host ports `${X_HOST_PORT:-…}`) +
`compose.agents.yml` (generated: 25 services; `include:` or `COMPOSE_FILE`). Agents depend on the gateway
(`service_started`; the gateway image has no shell, so no exec healthcheck — verify in M1).

Makefile: `help build gen gen-check test lint up down logs run SCENARIO=sc|all [BREAKPOINTS=1] [CHAOS=1]
replay FILE= gate-release card ID= (curl via gateway) e2e e2e-docker web-dev viz-dev orch-dev ID= agent-dev ID=
ollama-install ollama-check llm-check screenshots clean-sessions`.

`.env.example`: `GATEWAY_HOST_PORT=8000 GATEWAY_LLM_HOST_PORT=8100 GATEWAY_UI_HOST_PORT=15000 WEB_HOST_PORT=3000
VISUALIZER_HOST_PORT=8095 JAEGER_HOST_PORT=16686 SCRIPT_SPEED=1.0 LLM_MODEL=qwen3:1.7b LLM_ENABLED=1
EVENT_PAYLOAD_MAX_BYTES=16384`.

Docs: `CLAUDE.md`, `CODEMAP.md`, `docs/plans/2026-10-14-master-plan.md`, `docs/plans/PROGRESS.md`
(milestone table + deviations + resume notes, updated per milestone) — same conventions as `cp_amap_pci`.

## Milestones


| M | Deliverable | Verification |
|---|---|---|
| M0 | go.mod, `internal/httpx` copied, Makefile, compose skeleton, `.env.example`, docs skeleton, web skeleton builds | `make build && make test`; `cd web && npm run lint && npm test` |
| M1 | gateway config (hand-written, 1 route) + hello agent (static card, echo executor) + Jaeger | `make up`; `make card ID=hello` shows `supportedInterfaces[0].url == http://localhost:8000/agents/hello/invoke`; `curl -X POST …/agents/hello/invoke` `message/send` returns a task; gateway JSON log shows `a2a.method`; trace in Jaeger |
| M2 | `internal/scenario` + `cmd/atlasgen` (compose fragment + gateway config) + `internal/script` + generic executor: stream, input-required resume, push | `go test ./internal/...` (speed = 100, fake visualizer); Go test client against `httptest` walks all features; `make gen-check` clean |
| M3 | orchestrator plan runner, reporter, visualizer (JSONL sessions, SSE, runs, scenarios, gate), minimal UI event list | `make run SCENARIO=sc`; `curl -N localhost:8095/api/events/stream` shows `card_fetch → rpc_request → stream_event → task_state …`; `data/sessions/*.jsonl` written; reconnect with `Last-Event-ID` resumes |
| M4 | map + pins + arcs + payload panel + task state panel + replay engine / scrubber + fixtures mode + offline layer + scenario colour coding | Vitest for `arcs.ts`, `reduce.ts`, `engine.ts`; open `localhost:3000`, run `all`, five hues visible; pause, scrub, step; `NEXT_PUBLIC_USE_FIXTURES=1 npm run dev` replays a sample without backend |
| M5 | stage-4 policies, `X-Chaos`, OTLP access logs → `gateway_access` / `retry` events, Gateway tab, Jaeger deep link | `make run SCENARIO=sc CHAOS=1`: `policy_denied` for `restricted-shutdown-plant` (403 in payload panel), `retry` on the streaming route, ~2 s `delay` on the long-task route; Jaeger shows the retry spans |
| M6 | Ollama install + verify, `internal/llm`, LLM agents through gateway `:3100` | `make ollama-install` (official script, sudo), `ollama pull qwen3:1.7b`, `ollama list`; measure default (Vulkan) vs `OLLAMA_VULKAN=0` with the same prompt, pin the faster via systemd override, record in `docs/ollama.md`; `make llm-check` (curl through gateway); run shows `llm_request` / `llm_response`; `systemctl stop ollama` → `llm_unavailable`, run still completes |
| M7 | scenarios ex/pm/tr/kh, concurrent `all` run polished, e2e Playwright (api: run + SSE assertions via `openSse/waitFor`; ui: pins, arc appears, scrubber, gate, chips solo/mute), screenshots, README | `make e2e`, `make e2e-docker`; `make screenshots` writes `docs/img/*.png` |

## Verification (end to end)

```bash
cp .env.example .env && make up                 # ~30 services healthy
make card ID=sc-diagnosis                        # rewritten URL points at the gateway
make run SCENARIO=all                            # five runs, 2 s stagger
open http://localhost:3000                       # map, arcs in five hues, payload + task panels, replay bar
open http://localhost:16686                      # Jaeger traces per route
make test && make lint && make e2e               # Go unit, web Vitest, Playwright
make llm-check                                   # chat completion through the gateway LLM route
```

## Risks and mitigations

- **Prefix routing + card rewrite**: supported in source; M1 gate verifies with curl. Fallback: one gateway
  port per agent (generator change only).
- **`message/stream` is SSE over a JSON-RPC POST**: retry is configured only on streaming routes with the
  agent-side chaos firing on the first attempt (503 before any SSE bytes). If retry + SSE misbehave, demo
  retries on `message/send` and document.
- **Long SSE through the gateway**: agents emit a working update at least every 3 s; the client has no read
  timeout on streams, 10 min per step.
- **Gateway OTLP log export** encoding (protobuf vs JSON) is verified in M5 with a captured sample; Jaeger-only fallback.
- **Gateway image uid / no shell**: config mounted `0644` read-only; `depends_on` without exec healthcheck.
- **Push SSRF guard**: `AllowPrivateNetworks: true` mandatory; pushes go agent → orchestrator directly, labelled `via: direct`.
- **Authorization CEL on body**: `allow GET` first so cards always load; `coalesce()` guards non-A2A bodies; test the denial with curl before UI wiring.
- **Ollama on Intel iGPU**: Vulkan (default since 0.30) may be slower than CPU; measured in M6; 1.7B model,
  small `num_ctx`, 20 s timeout; scripted fallback keeps `make up` independent of Ollama.
- **25 agent containers**: one `build:` per binary, the rest share the image; `make up` ≈ 1 min warm.
- **Leaflet tiles need internet**: bundled GeoJSON world layer; e2e runs with `NEXT_PUBLIC_OFFLINE_MAP=1`.
- **"Honest" remote agent on a VM in another region**: out of scope now; the generator makes it a later one-file change.

## Reuse map (from `/home/tranmh/work/cp_amap_pci`)

| copy as-is | adapt | drop |
|---|---|---|
| `internal/httpx/{sse,env,health,json,middleware}.go`; `build/go.Dockerfile`; `.dockerignore`; compose anchor + healthcheck pattern; Makefile `help`/`up --wait`/`e2e*`; `web/src/map/{sse,leaflet-setup,html}.ts`; `MapShell.tsx`; `useAsync.ts`; web configs; DI + fixtures pattern; `e2e/playwright.config.ts`, helpers `env/poll/sse.ts`; docs conventions | `cmd/mock-crm/main.go` skeleton; `internal/pci/httpapi` `stream()`; `internal/sim` tick/speed/seed/force ideas; `map-controller.ts`; `pins/colors/popup.ts`; `status-feed.ts`; `AssetsMapApp.tsx`; `PlantDetailPanel.tsx`; `api-client.ts` request helper; `globals.css`; `web/Dockerfile`; `cities.json` rows (add Göppingen 48.7026/9.6527, Graz, Vienna, Brussels, Helsinki, Munich, Frankfurt, New Delhi, Tokyo, Äänekoski) | Postgres/goose/pgx, Keycloak/OIDC/`internal/auth`, MQTT/paho, testcontainers, Salesforce model, CRM sync, persona scoping, PCI rules, FilterBar/PlantList/i18n |
