import { describe, expect, it } from "vitest";
import type { AtlasEvent } from "@/events/schema";
import { applyEvent, emptyView } from "./reduce";
import { deniedCorrs, flightsAt, HUB, LEG_MS, routeOf } from "./flights";

let n = 0;
const ev = (kind: string, extra: Partial<AtlasEvent> = {}): AtlasEvent => ({ id: ++n, t: n * 100, ts: "", source: "orchestrator", kind, scenario: "sc", run_id: "r1", ...extra });

describe("applyEvent", () => {
  it("tracks agents, runs, tasks and narration", () => {
    const s = emptyView();
    applyEvent(s, ev("run_started", { from: "sc-orchestrator", note: "go" }));
    applyEvent(s, ev("step_started", { from: "sc-orchestrator", step: "parts", to: "sc-spare-parts", note: "ask parts" }));
    applyEvent(s, ev("agent_work", { source: "agent", from: "sc-spare-parts", state_to: "input-required", note: "serial?", task_id: "t1" }));
    applyEvent(s, ev("task_state", { from: "sc-spare-parts", task_id: "t1", state_to: "input-required", step: "parts" }));
    applyEvent(s, ev("task_state", { from: "sc-spare-parts", task_id: "t1", state_to: "completed", step: "parts" }));
    applyEvent(s, ev("step_finished", { step: "shutdown", to: "sc-field-service", state_to: "denied", note: "refused" }));
    expect(s.agents.get("sc-spare-parts")?.status).toBe("input-required");
    expect(s.agents.get("sc-field-service")?.status).toBe("denied");
    expect(s.tasks.get("t1")?.current).toBe("completed");
    expect(s.tasks.get("t1")?.history.map((h) => h.state)).toEqual(["input-required", "completed"]);
    expect(s.runs.get("r1")?.steps.get("shutdown")?.state).toBe("denied");
    expect(s.narration.get("sc")?.note).toBe("refused");
  });

  it("holds breakpoints until released", () => {
    const s = emptyView();
    applyEvent(s, ev("breakpoint", { corr: "g1", from: "sc-orchestrator", step: "x" }));
    expect(s.gates.size).toBe(1);
    expect(s.agents.get("sc-orchestrator")?.status).toBe("waiting");
    applyEvent(s, ev("gate_released", { corr: "g1", from: "sc-orchestrator" }));
    expect(s.gates.size).toBe(0);
  });

  it("resets a scenario's agents when a new run starts", () => {
    const s = emptyView();
    applyEvent(s, ev("agent_work", { from: "sc-diagnosis", state_to: "completed" }));
    applyEvent(s, ev("agent_work", { from: "ex-shipping", scenario: "ex", state_to: "completed" }));
    applyEvent(s, ev("run_started", { from: "sc-orchestrator", run_id: "r2" }));
    expect(s.agents.get("sc-diagnosis")?.status).toBe("idle");
    expect(s.agents.get("ex-shipping")?.status).toBe("completed");
  });
});

describe("flights", () => {
  it("routes requests through the hub and pushes directly", () => {
    const req = ev("rpc_request", { to: "sc-diagnosis", method: "SendStreamingMessage", corr: "c1" });
    expect(routeOf(req, new Set())?.legs).toEqual([
      ["sc-orchestrator", HUB],
      [HUB, "sc-diagnosis"],
    ]);
    expect(routeOf(req, new Set())?.style).toBe("stream");
    const push = ev("push_received", { from: "sc-logistics", to: "sc-orchestrator" });
    expect(routeOf(push, new Set())?.legs).toEqual([["sc-logistics", "sc-orchestrator"]]);
  });

  it("stops a refused request at the gateway", () => {
    const req = ev("rpc_request", { to: "sc-field-service", method: "SendMessage", corr: "c9" });
    const denied = ev("policy_denied", { corr: "c9" });
    const r = routeOf(req, deniedCorrs([req, denied]));
    expect(r?.legs).toEqual([["sc-orchestrator", HUB]]);
    expect(r?.blockedAt).toBe(HUB);
  });

  it("animates in event time and fades out", () => {
    const req = ev("rpc_request", { to: "sc-diagnosis", method: "SendMessage" });
    const all = () => true;
    expect(flightsAt([req], req.t - 1, new Set(), all)).toHaveLength(0);
    const mid = flightsAt([req], req.t + LEG_MS, new Set(), all)[0];
    expect(mid.progress).toBeCloseTo(0.5);
    expect(mid.alpha).toBe(1);
    const fading = flightsAt([req], req.t + 2 * LEG_MS + 600, new Set(), all)[0];
    expect(fading.alpha).toBeCloseTo(0.5);
    expect(flightsAt([req], req.t + 10 * LEG_MS, new Set(), all)).toHaveLength(0);
    expect(flightsAt([req], req.t + LEG_MS, new Set(), () => false)).toHaveLength(0);
  });
});

describe("MCP flights", () => {
  it("draws tool calls to the server and the handshake only to the gateway", () => {
    const call = ev("mcp_request", { source: "agent", from: "sc-logistics", to: "sc-erp", method: "tools/call", corr: "m1", payload: { params: { name: "erp_quote_freight" } } });
    expect(routeOf(call, new Set())).toMatchObject({ legs: [["sc-logistics", HUB], [HUB, "sc-erp"]], style: "mcp", label: "MCP erp_quote_freight" });
    const init = ev("mcp_request", { source: "agent", from: "sc-logistics", to: "agentgateway", method: "initialize" });
    expect(routeOf(init, new Set())).toMatchObject({ legs: [["sc-logistics", HUB]], style: "mcp-handshake" });
    const prog = ev("mcp_progress", { from: "sc-erp", to: "sc-logistics" });
    expect(routeOf(prog, new Set())?.style).toBe("mcp-frame");
  });

  it("stops a refused tool call at the gateway", () => {
    const call = ev("mcp_request", { from: "sc-field-service", to: "sc-erp", method: "tools/call", corr: "m9" });
    const denied = ev("mcp_denied", { from: "agentgateway", to: "sc-field-service", corr: "m9" });
    const r = routeOf(call, deniedCorrs([call, denied]));
    expect(r?.legs).toEqual([["sc-field-service", HUB]]);
    expect(r?.blockedAt).toBe(HUB);
    expect(routeOf(denied, new Set())?.label).toBe("MCP denied");
  });

  it("pulses the MCP server when a call arrives", () => {
    const s = emptyView();
    applyEvent(s, ev("mcp_server_call", { from: "sc-logistics", to: "sc-erp", note: "ERP received tool quote_freight" }));
    expect(s.agents.get("sc-erp")?.activeT).toBeDefined();
    applyEvent(s, ev("mcp_denied", { from: "agentgateway", to: "sc-field-service" }));
    expect(s.agents.get("sc-field-service")?.faultT).toBeDefined();
  });
});
