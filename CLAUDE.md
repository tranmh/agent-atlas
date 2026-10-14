# CLAUDE.md — agent-atlas

Guidance for working in this repo. Where the plan and the code disagree, the code wins; deviations are in
`docs/plans/PROGRESS.md`.

## What this is

A didactic demo of A2A v1.0 behind agentgateway v1.6.0: 5 scenarios × (1 orchestrator + 4 agents), each
agent demonstrating one feature (streaming, multi-turn, long task + push, restricted), one LLM agent per
scenario (Ollama via the gateway's LLM route). A visualizer records every protocol event and the web UI
replays it on a world map. All five scenarios can run at the same time.

## Stack

- Go 1.26, `github.com/a2aproject/a2a-go/v2` v2.6.0, chi, yaml.v3, OTLP protos. Tests: testify.
- agentgateway `cr.agentgateway.dev/agentgateway:v1.6.0`, Jaeger all-in-one 1.76.0, Ollama 0.40 (host, user install).
- Web: Next.js 15.5, React 19, TypeScript strict, Leaflet 1.9, Tailwind 3, Zod 3, Vitest 4. e2e: Playwright 1.56.1.

## Commands

- `cp .env.example .env && make up` — build and start everything (`docker compose up -d --build --wait`).
- `make gen` after editing `scenarios/*.yaml` or `cmd/atlasgen` templates; `make gen-check` / `go test ./cmd/atlasgen` fail on stale output.
- `make test-infra` (fresh-clone checks). `make test` (Go), `make lint` (gofmt, vet, web lint), `make web-test`, `make e2e` (stack must be up), `make screenshots`.
- `make run SCENARIO=sc|ex|pm|tr|kh|all SPEED=1 CHAOS=true BREAKPOINTS=true`, `make tail-events`, `make gate-release`.
- `make web-dev` (hot reload on :3001 against the stack), `make web-fixtures` (sample replay, no backend).
- `make ollama-install | ollama-check | llm-check`. Ollama listens on 172.17.0.1:11434 (Docker bridge only).

## Ports (host)

web 3000 · visualizer 8095 · gateway A2A 8000, LLM 8100, admin UI 15000 · Jaeger 16686. Agents and
orchestrators publish no ports: the only way in is the gateway (agents) or the visualizer (orchestrators).

## Hard rules

- Scenario files are the single source of truth; never hand-edit `deploy/agentgateway/config.yaml`,
  `compose.agents.yml` or `web/public/samples/scenarios.json`.
- agentgateway expands `$VARS` anywhere in its config, comments included; an unset one makes the reload fail
  silently (old config stays). The atlasgen test forbids `$` in the generated config.
- The gateway caches backend addresses; it restarts with any recreated agent (`depends_on.restart`). After
  recreating one agent by hand: `docker compose restart agentgateway`.
- Push senders need `AllowPrivateNetworks: true` (a2a-go's SSRF guard blocks compose addresses).
- Wire methods are A2A v1.0 names (`SendMessage`, `SendStreamingMessage`, `GetTask`), states `TASK_STATE_*`;
  events use short states (`input-required`). Never add a geographic place for the gateway: it is the hub.
- `web/src/map`, `web/src/replay`, `web/src/events` stay framework-free (ESLint enforces it).
- qwen3 needs `reasoning_effort: "none"`; `/no_think` is ignored by Ollama's OpenAI endpoint.
- Commit after each milestone; update `docs/plans/PROGRESS.md`.

## Behaviours worth knowing

- Events are ordered by `produced_ts`; live mode plays 1.5 s behind real time.
- Chaos is deterministic: the streaming agent answers 503 to the first attempt of a message when `X-Chaos: 1`.
- One Jaeger trace per run (`traceparent` from the orchestrator; agents pass it to their LLM call).
- Breakpoint gates block the orchestrator in `POST /api/gate/wait` until released (UI or `make gate-release`).
