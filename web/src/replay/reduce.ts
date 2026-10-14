/**
 * The view state at the playhead, folded event by event (ReplayEngine applies
 * it incrementally). Pure data; React and the map read it.
 */
import type { AtlasEvent } from "@/events/schema";
import type { Fold } from "./engine";

export type AgentStatus =
  | "idle"
  | "submitted"
  | "working"
  | "input-required"
  | "completed"
  | "failed"
  | "canceled"
  | "denied"
  | "waiting"; // orchestrator at a breakpoint

export interface AgentView {
  status: AgentStatus;
  note?: string;
  noteT?: number;
  runId?: string;
  taskId?: string;
  /** Time of the last activity (request arrived, work step); drives the pulse. */
  activeT?: number;
  /** Time of the last fault (503 chaos, 403 denial) at this agent. */
  faultT?: number;
}

export interface TaskStep {
  state: string;
  t: number;
  note?: string;
  eventId: number;
}

export interface TaskView {
  taskId: string;
  agent: string;
  scenario: string;
  runId: string;
  step: string;
  contextId?: string;
  current: string;
  history: TaskStep[];
}

export interface RunView {
  runId: string;
  scenario: string;
  started: number;
  finished?: number;
  step?: string;
  note?: string;
  steps: Map<string, { state: string; note?: string; agent?: string }>;
  ok: boolean;
  /** W3C trace id of the run (all gateway spans of the run share it). */
  traceId?: string;
}

export interface GateView {
  corr: string;
  runId: string;
  scenario: string;
  step: string;
  from: string;
  to: string;
  note: string;
  t: number;
}

export interface ViewState {
  agents: Map<string, AgentView>;
  tasks: Map<string, TaskView>;
  runs: Map<string, RunView>;
  /** Latest narration line per scenario (step_started / run_* notes). */
  narration: Map<string, { note: string; t: number; eventId: number }>;
  /** Breakpoints currently waiting (at the playhead). */
  gates: Map<string, GateView>;
  /** Last time the gateway logged a request (hub flash). */
  gatewayT?: number;
  llmT?: number;
}

export function emptyView(): ViewState {
  return { agents: new Map(), tasks: new Map(), runs: new Map(), narration: new Map(), gates: new Map() };
}

function agent(s: ViewState, id: string | undefined): AgentView | null {
  if (!id || id === "llm" || id === "agentgateway") return null;
  let a = s.agents.get(id);
  if (!a) {
    a = { status: "idle" };
    s.agents.set(id, a);
  }
  return a;
}

const WORK_STATES = new Set(["submitted", "working", "input-required", "completed", "failed", "canceled"]);

/** Apply one event to the view state (mutates). */
export function applyEvent(s: ViewState, e: AtlasEvent): void {
  const scenario = e.scenario ?? "";
  switch (e.kind) {
    case "run_started": {
      const traceId = (e.payload as { trace_id?: unknown } | undefined)?.trace_id;
      s.runs.set(e.run_id ?? "", { runId: e.run_id ?? "", scenario, started: e.t, steps: new Map(), ok: true, traceId: typeof traceId === "string" ? traceId : undefined });
      // A new run resets the scenario's agents.
      for (const [id, a] of s.agents) {
        if (id.startsWith(scenario + "-")) {
          a.status = "idle";
          a.note = undefined;
          a.runId = e.run_id;
        }
      }
      const o = agent(s, e.from);
      if (o) Object.assign(o, { status: "working", note: e.note, noteT: e.t, runId: e.run_id, activeT: e.t });
      s.narration.set(scenario, { note: e.note ?? "", t: e.t, eventId: e.id });
      break;
    }
    case "run_finished": {
      const r = s.runs.get(e.run_id ?? "");
      if (r) {
        r.finished = e.t;
        r.note = e.note;
        r.step = undefined;
      }
      const o = agent(s, e.from);
      if (o) Object.assign(o, { status: r && !r.ok ? "failed" : "completed", note: e.note, noteT: e.t });
      s.narration.set(scenario, { note: e.note ?? "", t: e.t, eventId: e.id });
      break;
    }
    case "step_started": {
      const r = s.runs.get(e.run_id ?? "");
      if (r && e.step) {
        r.step = e.step;
        if (!r.steps.has(e.step)) r.steps.set(e.step, { state: "running", agent: e.to });
      }
      const o = agent(s, e.from);
      if (o) Object.assign(o, { status: "working", note: e.note, noteT: e.t, activeT: e.t });
      s.narration.set(scenario, { note: e.note ?? "", t: e.t, eventId: e.id });
      break;
    }
    case "step_finished": {
      const r = s.runs.get(e.run_id ?? "");
      if (r && e.step) {
        r.steps.set(e.step, { state: e.state_to ?? "", note: e.note, agent: e.to });
        if (e.state_to === "error" || e.state_to === "unexpected") r.ok = false;
      }
      if (e.state_to === "denied") {
        const a = agent(s, e.to);
        if (a) Object.assign(a, { status: "denied", note: e.note, noteT: e.t, faultT: e.t });
      }
      s.narration.set(scenario, { note: e.note ?? "", t: e.t, eventId: e.id });
      break;
    }
    case "breakpoint": {
      if (e.corr) {
        s.gates.set(e.corr, { corr: e.corr, runId: e.run_id ?? "", scenario, step: e.step ?? "", from: e.from ?? "", to: e.to ?? "", note: e.note ?? "", t: e.t });
      }
      const o = agent(s, e.from);
      if (o) Object.assign(o, { status: "waiting", note: e.note, noteT: e.t });
      s.narration.set(scenario, { note: e.note ?? "", t: e.t, eventId: e.id });
      break;
    }
    case "gate_released": {
      if (e.corr) s.gates.delete(e.corr);
      const o = agent(s, e.from);
      if (o && o.status === "waiting") o.status = "working";
      break;
    }
    case "agent_request": {
      const a = agent(s, e.to);
      if (a) a.activeT = e.t;
      break;
    }
    case "agent_work": {
      const a = agent(s, e.from);
      if (a && e.state_to && WORK_STATES.has(e.state_to)) {
        Object.assign(a, { status: e.state_to as AgentStatus, note: e.note, noteT: e.t, runId: e.run_id, taskId: e.task_id, activeT: e.t });
      }
      if (e.task_id) {
        const tv = s.tasks.get(e.task_id);
        if (tv && e.note) tv.history.push({ state: `note:${e.state_to ?? ""}`, t: e.t, note: e.note, eventId: e.id });
      }
      break;
    }
    case "fault_injected": {
      const a = agent(s, e.from);
      if (a) Object.assign(a, { faultT: e.t, note: e.note, noteT: e.t });
      break;
    }
    case "task_state": {
      if (!e.task_id) break;
      let tv = s.tasks.get(e.task_id);
      if (!tv) {
        tv = { taskId: e.task_id, agent: e.from ?? "", scenario, runId: e.run_id ?? "", step: e.step ?? "", contextId: e.context_id, current: "", history: [] };
        s.tasks.set(e.task_id, tv);
      }
      tv.current = e.state_to ?? tv.current;
      tv.history.push({ state: e.state_to ?? "", t: e.t, note: e.note, eventId: e.id });
      break;
    }
    case "gateway_access":
      s.gatewayT = e.t;
      if (e.to === "llm") s.llmT = e.t;
      break;
    case "llm_request":
    case "llm_response":
    case "llm_unavailable":
      s.llmT = e.t;
      break;
  }
}

export const viewFold: Fold<ViewState> = { init: emptyView, apply: applyEvent };
