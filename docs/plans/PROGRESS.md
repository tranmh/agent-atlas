# Progress

Plan: [2026-10-14-master-plan.md](2026-10-14-master-plan.md). One row per milestone, updated when it lands.

| # | Milestone | Status | Commit |
|---|---|---|---|
| M0 | Scaffolding: go.mod, copied helpers, Makefile, web skeleton, docs | done | c0cc234 |
| M1 | agentgateway + hello agent, card rewrite verified | done | 3091f7d |
| M2 | scenario schema, generator, scripted executor | done | 2ef8ab9 |
| M3 | orchestrator, event log, visualizer SSE | done | c41c3ec |
| M4 | map, arcs, panels, replay | done | (this commit) |
| M5 | gateway policies, Jaeger, gateway view | open | |
| M6 | Ollama + LLM agents through the gateway | open | |
| M7 | all scenarios, e2e, screenshots, README | open | |
| M8 | Fable code review; critical/high findings fixed test-first (red commit, then green commit) | open | |

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

## Notes for resuming

- Reference clones of a2a-go and agentgateway are not part of the repo. Re-clone them into a scratch
  directory when you need to read the SDK or gateway source.
