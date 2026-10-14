# Code review of the MCP extension — Fable, 2026-10-14

Milestone M15 of [the MCP plan](../plans/2026-10-14-mcp-plan.md). Three read-only reviewers on the Fable model
reviewed `git diff 1bf4af8..0a92c68` (M9–M14), one per area, for **critical and high** defects only. Every
finding was reproduced with a failing test before it was fixed: a `test(red)` commit, then a `fix(green)` commit.

## Summary

| Area | Critical | High | Fixed |
|---|---|---|---|
| Go: MCP client and server, wire capture, script runner and LLM tool loop | 0 | 2 | 2 |
| Scenarios, generator, gateway config, visualizer, compose | 0 | 2 | 2 |
| Web: MCP UI and pin layout | 0 | 0 | — |

After the fixes: Go tests pass with `-race`, 51 web unit tests, 20 Playwright e2e tests against the rebuilt stack,
`make lint` and `make gen-check` are green.

## Findings (all high, all fixed test-first)

| Id | Area | Finding | Red | Green |
|---|---|---|---|---|
| C1 | Visualizer | Gateway log rows for refused MCP write tools returned early, before the scenario code and the gateway timestamp were set. In the UI they showed in every scenario's solo view and were ordered by receipt time instead of the moment of refusal. | `fcf1b84` | `23e0db5` |
| C2 | Scenarios | Scripted tool-step arguments were not checked against the tool's parameters. A typo or an enum value outside the list passed validation and only failed (or rendered nonsense) during a run. `CheckArgs` now checks names, enums and numeric types; template values such as `{{input}}` are only checked for presence. | `fcf1b84` | `23e0db5` |
| G1 | MCP client | The "denied" verdict was a sticky flag on the shared transport. A network error after a refused write tool (for example while the gateway restarts) was reported as a policy refusal, in the narration and to the model. | `6b80a68` | `059124e` |
| G2 | MCP client | The same flag was written by the SDK's own goroutine (it sends `notifications/cancelled` when a call's context ends) while the caller read it: a data race, confirmed by `-race`. Refusals are now classified from each call's own error ("Unknown tool" for 400, "Forbidden" for 403); no state is shared between requests. | `6b80a68` | `059124e` |

## Checked and found sound (reviewers' notes)

- Both gateway policies hold against probes: foreign or missing callers get 403 at `initialize`; the write tool is
  absent from `tools/list`, refused with 400 under its prefixed name and fails under any other spelling, never
  reaching the server; `resources/read` and `prompts/get` pass the CEL rule.
- Generated config and compose: no `$`, targets match the YAML ids, all ten MCP servers are healthy and restart the gateway.
- Flights are drawn to the right nodes; refused MCP calls stop at the hub; pin offsets are applied consistently to
  icons, flights and hit testing; the layout costs 1.5–10 ms per zoom for 35 pins and keeps pins ≥ 31 px apart.
- Old scenario files and recorded samples without MCP still parse.
- The A2A capture after the refactor onto `internal/wire` behaves as before.

## Below the bar (not fixed)

| Area | Note |
|---|---|
| Web | Label budget counts the faint MCP handshake flights, so labels disappear a little earlier with five scenarios running. |
| Web | Muted scenarios' pins still take part in the declutter layout. |
| Go | Leftover progress notifications can show under the next tool's name at high speed factors. |
| Go | `Session.Close` waits for an in-flight call; bounded by the tool's duration. |
| Infra | Any `<code>-*` caller passes the MCP route's HTTP policy (self-asserted `X-Caller`, as on the A2A routes). |
