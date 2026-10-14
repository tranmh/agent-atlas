# CODEMAP.md

One line per directory. See `CLAUDE.md` for conventions and commands, `docs/events.md` for the event contract.

## Root

- `Makefile` — every dev command (`make help`). `docker-compose.yml` — jaeger, visualizer, web; includes the generated `compose.agents.yml` (agentgateway, 20 agents, 5 orchestrators). `.env.example` — settings. `go.mod` — module `github.com/tranmh/agent-atlas`, Go 1.26 (ignores `web/`, `e2e/`).
- `build/` — `go.Dockerfile`, one image per Go binary (`ARG SERVICE=agent|orchestrator|visualizer`).
- `scenarios/` — the five scenario YAML files, embedded into every binary (`embed.go`).
- `scripts/` — `record-sample.sh` (bundled replay sample), `ollama-user-install.sh` (Ollama without root).
- `deploy/agentgateway/config.yaml` — generated gateway config (routes, A2A, retry, delay, authorization, LLM route, OTLP).

## Go binaries (`cmd/`)

- `cmd/agent/` — one A2A worker agent (`AGENT_ID`); card, script and feature come from the scenario.
- `cmd/orchestrator/` — one scenario's orchestrator (`ORCH_ID`): `POST /run`, push webhook `POST /push/{token}`.
- `cmd/visualizer/` — event hub: `POST /api/events`, SSE `GET /api/events/stream`, sessions, runs, gates, `POST /otlp/v1/logs`.
- `cmd/mcpserver/` — one scenario MCP server (`MCP_SERVER_ID`), Streamable HTTP at `/mcp`.
- `cmd/atlasgen/` — generates the gateway config (A2A + MCP routes), `compose.agents.yml` and `web/public/samples/scenarios.json`; its test fails on stale output.

## Go packages (`internal/`)

- `internal/scenario/` — scenario schema, loader, validation (one agent per feature, one LLM agent, plan consistency, two MCP servers with one slow and one write tool).
- `internal/events/` — event schema (`Event`, kinds), payload truncation, async batching `Reporter`, test `Recorder`.
- `internal/script/` — scripted A2A executor: steps → status updates, artifacts, input-required resume, MCP `tool`/`resource` steps, the LLM tool loop over MCP with fallback, speed factor.
- `internal/mcpx/` — the agents' MCP client through the gateway, with wire events (`mcp_request/response/progress/denied`); `mcpxtest/` fakes the gateway's multiplexed MCP route for tests.
- `internal/mcpserver/` — scenario MCP servers on the go-sdk: typed tools with templated results, progress, resources, prompts, `mcp_server_call` events.
- `internal/wire/` — shared HTTP capture: SSE tee, request/response buffering (used by `a2ax` and `mcpx`).
- `internal/agent/` — agent assembly: card, a2a-go JSON-RPC handler, push sender, request-capture and chaos middleware.
- `internal/a2ax/` — orchestrator wire capture (`Transport` RoundTripper: run headers, traceparent, card/RPC/SSE/403 events), task-state `Tracker`.
- `internal/orchestrator/` — plan runner (send, stream, push + GetTask, expected denial, parallel steps), webhook, breakpoint gate.
- `internal/llm/` — OpenAI-compatible chat client (through the gateway's LLM route).
- `internal/eventlog/` — per-session JSONL store with in-memory replay and broadcaster.
- `internal/visualizer/` — visualizer HTTP API and the OTLP access-log decoder (`gateway_access` events).
- `internal/httpx/` — copied from cp_amap_pci: SSE writer + generic broadcaster, env helpers, JSON errors, middleware, healthcheck.

## Web (`web/`, Next.js 15 + React 19 + Leaflet)

- `web/src/events/` — Zod schema of events and scenarios, JSONL parser, SSE `EventFeed` (framework-free).
- `web/src/replay/` — `ReplayEngine` (clock, live/replay, step, seek, incremental fold), `reduce.ts` (view state), `flights.ts` (messages → animated legs). Framework-free.
- `web/src/map/` — `AtlasMap` (Leaflet pins, canvas flows, gateway hub), geometry, colours, SSE client (copied). Framework-free.
- `web/src/components/` — `AtlasApp` (layout + animation loop), replay bar, chips, event list, payload/task/gateway/agent panels, gate prompt, narration, legend.
- `web/src/hooks/useAtlas.ts` — engine + live feed + recorded sessions + fixtures mode. `web/src/lib/` — API client, formatting.
- `web/public/` — `world-110m.geojson` (Natural Earth, offline base map), `samples/` (scenarios.json, demo.jsonl).

## e2e (`e2e/`, Playwright 1.56.1)

- `tests/api/` — gateway card rewrite, authorization, full runs (service case, all five at once), breakpoints, SSE resume; MCP routes (multiplexing, refused write tool, foreign caller, progress), MCP in a run.
- `tests/ui/` — map, run animation, replay and panels, solo, breakpoint prompt, agent card, sample replay.
- `tests/screenshots/` — `make screenshots`. `tests/helpers/` — env, SSE reader, run helpers.

## Docs (`docs/`)

- `docs/plans/` — master plan and `PROGRESS.md` (milestones, deviations, verified facts). `docs/events.md`, `docs/ollama.md`, `docs/screenshots/`, `docs/reviews/`.
