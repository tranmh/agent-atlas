# Code review — Fable, 2026-10-14

Milestone M8 of the master plan. Three read-only reviewers on the Fable model reviewed `main` at
`ab70359`, one per area, for **critical and high** defects only. Every finding was reproduced in this repo
with a failing test before it was fixed: a `test(red)` commit, then a `fix(green)` commit.

## Summary

| Area | Critical | High | Fixed |
|---|---|---|---|
| Go: agents, orchestrator, wire capture, LLM client, events | 0 | 1 | 1 |
| Go: visualizer, event log, scenarios, generator, compose, Makefile | 0 | 1 | 1 |
| Web frontend | 0 | 3 | 3 |

After the fixes: Go tests pass with `-race`, 44 web unit tests, 14 Playwright e2e tests against the
rebuilt stack, `make lint` and `make test-infra` are green.

## Findings (all high, all fixed test-first)

| Id | Area | Finding | Red | Green |
|---|---|---|---|---|
| R1 | Infra | On a fresh clone `data/sessions` does not exist (git-ignored, never created). Docker creates the bind-mount source as root, the visualizer (running as uid 1000) cannot write its session file and crash-loops; `web` never starts. Also wrong for any user whose uid is not 1000. | `2938f02` | `713e53a` |
| W1 | Web | Stepping (← / →) skipped every event sharing a millisecond with the previous one (232 of 511 events in the bundled sample), and the fold ran ahead of the selected event. The engine now remembers the exact event index after a step. | `864cf6a` | `6a9791e` |
| W2 | Web | Pins created after the view was folded (map chunk or scenarios arriving after the first SSE batch) stayed idle until the next event. `AtlasMap` now re-applies the last view to new pins. | `864cf6a` | `6a9791e` |
| W3 | Web | After pausing an empty live view, or a visualizer restart while paused, Play advanced from epoch 0 and never reached the events. Play now restarts from the beginning when the playhead is outside the recorded range. | `864cf6a` | `6a9791e` |
| G1 | Go events | `Reporter.Emit` after `Close` panicked ("send on closed channel"). a2a-go runs executions detached from the request, so a long push task still emits after `srv.Shutdown` returns; `docker compose restart` during a run ended in a panic. The reporter now stops through a separate channel and drops late events. | `f7c27ac` | `5a1de4b` |

## Checked and found sound (reviewers' notes)

- SSE resume: subscribe before backlog plus id dedupe; no lost or duplicated events; slow subscribers are evicted and resume with `Last-Event-ID`.
- Gates: released on every exit path; orchestrators have a 30 min client timeout.
- Session file access is regex-guarded against path traversal; the OTLP decoder is nil-safe and size-bounded.
- Generated gateway config does what its comments say (delay measured at 2 s, 403 for `restricted-*` skills and foreign callers).
- `resumeIndex` matches a2a-go's history handling; push webhook needs the random token in path and header; request bodies are capped; parallel steps and concurrent runs share no unsafe state.
- No XSS: everything interpolated into pin HTML is escaped, except the scenario colour, which only comes from validated scenario files (`#rrggbb`).

## Below the bar (not fixed)

| Area | Note |
|---|---|
| Web | Scenario colour is interpolated unescaped into a `style` attribute; safe today because colours are validated in Go. |
| Web | `load()` does not dedupe repeated lines inside one JSONL file (only live `append` does). |
| Web, Go | Event buffers, task stores and run maps grow for the process lifetime (by design for replay; ~400 KB per five-scenario run). |
| Go | A stream that ends cleanly without a final state counts as a successful step with the last seen state. a2a-go reports abrupt closes as errors, so no realistic trigger was found. |
| Scripts | `ollama-user-install.sh`: the `172.17.0.1` fallback is dead code when `docker0` is missing (`set -e` exits first). |
| Web | `src/hooks/useAsync.ts` (copied from cp_amap_pci) is unused. |
