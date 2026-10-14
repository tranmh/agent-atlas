# Progress

Plan: [2026-10-14-master-plan.md](2026-10-14-master-plan.md). One row per milestone, updated when it lands.

| # | Milestone | Status | Commit |
|---|---|---|---|
| M0 | Scaffolding: go.mod, copied helpers, Makefile, web skeleton, docs | done | (this commit) |
| M1 | agentgateway + hello agent, card rewrite verified | open | |
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

## Notes for resuming

- Reference clones of a2a-go and agentgateway are not part of the repo. Re-clone them into a scratch
  directory when you need to read the SDK or gateway source.
