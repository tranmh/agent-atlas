# Progress

Plan: [2026-10-14-master-plan.md](2026-10-14-master-plan.md). One row per milestone, updated when it lands.

| # | Milestone | Status | Commit |
|---|---|---|---|
| M0 | Scaffolding: go.mod, copied helpers, Makefile, web skeleton, docs | done | c0cc234 |
| M1 | agentgateway + hello agent, card rewrite verified | done | (this commit) |
| M2 | scenario schema, generator, scripted executor | open | |
| M3 | orchestrator, event log, visualizer SSE | open | |
| M4 | map, arcs, panels, replay | open | |
| M5 | gateway policies, Jaeger, gateway view | open | |
| M6 | Ollama + LLM agents through the gateway | open | |
| M7 | all scenarios, e2e, screenshots, README | open | |

## Deviations from the plan

- `leaflet.markercluster` is not used. Thirty pins with per-scenario offsets stay readable without
  clustering, and arcs never have to re-anchor to a cluster icon.
- npm 10.9 crashes building the ideal tree from scratch (`edgesOut` of null). The web lockfile was
  seeded from `cp_amap_pci/web/package-lock.json`; `npm install` then pruned the unused packages.

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

## Notes for resuming

- Reference clones of a2a-go and agentgateway are not part of the repo. Re-clone them into a scratch
  directory when you need to read the SDK or gateway source.
