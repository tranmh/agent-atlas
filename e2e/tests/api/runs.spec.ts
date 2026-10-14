import { expect, test } from "@playwright/test";
import { collectRun, openEvents, startRun, type AtlasEvent } from "../helpers/atlas";
import { JAEGER_URL, VIZ_URL } from "../helpers/env";

const kinds = (evs: AtlasEvent[]) => new Set(evs.map((e) => e.kind));

test("service case: every A2A feature shows up on the wire, in the agents and in the gateway log", async () => {
  const stream = await openEvents();
  const [runId] = await startRun({ scenario: "sc", speed: 8, chaos: true });
  const evs = await collectRun(stream, runId);
  await stream.close();

  const finished = Object.fromEntries(evs.filter((e) => e.kind === "step_finished").map((e) => [e.step, e.state_to]));
  expect(finished).toEqual({ diagnose: "completed", parts: "completed", ship: "completed", shutdown: "denied", visit: "completed" });
  for (const k of ["card_fetch", "rpc_request", "rpc_response", "stream_event", "task_state", "push_received", "policy_denied", "agent_request", "agent_work", "artifact", "fault_injected", "gateway_access"]) {
    expect(kinds(evs), k).toContain(k);
  }
  // Multi-turn: the task went through input-required and completed.
  const parts = evs.filter((e) => e.kind === "task_state" && e.step === "parts").map((e) => e.state_to);
  expect(parts).toEqual(["input-required", "completed"]);
  // Card discovery shows the rewritten URL.
  const card = evs.find((e) => e.kind === "card_fetch")!;
  expect(card.payload.card.supportedInterfaces[0].url).toContain("agentgateway:3000/agents/");
  // Chaos: the gateway retried, and says so.
  const retried = evs.filter((e) => e.kind === "gateway_access" && Number(e.payload?.["retry.attempt"] ?? 0) > 0);
  expect(retried.length).toBeGreaterThan(0);
  // The denied call never reached the agent.
  const denied = evs.find((e) => e.kind === "policy_denied")!;
  expect(denied.http_status).toBe(403);
  expect(evs.some((e) => e.kind === "agent_request" && e.step === "shutdown")).toBeFalsy();
  // The LLM step ran (model answer) or fell back (model down); either way the run completed.
  expect(evs.some((e) => e.kind === "llm_response" || e.kind === "llm_unavailable")).toBeTruthy();
  // The whole run is one trace in Jaeger.
  const traceId = evs.find((e) => e.kind === "run_started")!.payload.trace_id;
  await expect
    .poll(async () => ((await (await fetch(`${JAEGER_URL}/api/traces/${traceId}`)).json()).data?.[0]?.spans ?? []).length, { timeout: 20_000 })
    .toBeGreaterThan(10);
});

test("all five scenarios run at the same time", async () => {
  const stream = await openEvents();
  const runIds = await startRun({ scenario: "all", speed: 8 });
  expect(runIds).toHaveLength(5);
  for (const id of runIds) {
    const evs = await collectRun(stream, id, 120_000);
    const states = evs.filter((e) => e.kind === "step_finished").map((e) => e.state_to);
    expect(states.sort(), id).toEqual(["completed", "completed", "completed", "completed", "denied"]);
  }
  await stream.close();
});

test("breakpoints hold a run until released; abort stops it", async () => {
  const stream = await openEvents();
  const [runId] = await startRun({ scenario: "kh", speed: 8, breakpoints: true });
  const bp = await stream.waitFor((e) => e.json?.kind === "breakpoint" && e.json?.run_id === runId, 30_000, "breakpoint");
  expect(bp.json.step).toBe("search");
  await new Promise((r) => setTimeout(r, 1500));
  expect(stream.events.some((e) => e.json?.kind === "step_started" && e.json?.run_id === runId)).toBeFalsy();

  const gates = await (await fetch(`${VIZ_URL}/api/gates`)).json();
  expect(gates.some((g: { run_id: string }) => g.run_id === runId)).toBeTruthy();
  await fetch(`${VIZ_URL}/api/gate/release`, { method: "POST", body: JSON.stringify({ run_id: runId }) });
  await stream.waitFor((e) => e.json?.kind === "step_started" && e.json?.run_id === runId, 15_000, "step after release");

  await stream.waitFor((e) => e.json?.kind === "breakpoint" && e.json?.run_id === runId && e.json?.step === "expert", 60_000, "second breakpoint");
  await fetch(`${VIZ_URL}/api/gate/release`, { method: "POST", body: JSON.stringify({ run_id: runId, action: "abort" }) });
  const fin = await stream.waitFor((e) => e.json?.kind === "run_finished" && e.json?.run_id === runId, 15_000, "run_finished");
  expect(fin.json.note).toContain("aborted");
  await stream.close();
});

test("the event stream resumes after Last-Event-ID", async () => {
  const health = await (await fetch(`${VIZ_URL}/healthz`)).json();
  expect(health.events).toBeGreaterThan(2);
  const res = await fetch(`${VIZ_URL}/api/events?after=${health.events - 2}`);
  const evs = await res.json();
  expect(evs.map((e: AtlasEvent) => e.id)).toEqual([health.events - 1, health.events]);
});
