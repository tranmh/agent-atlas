/**
 * Browser mirror of the event log contract (docs/events.md, Go
 * internal/events.Event) and the scenario format (internal/scenario).
 * Everything that enters the app from the network is validated here.
 */
import { z } from "zod";

export const EVENT_KINDS = [
  "run_started",
  "run_finished",
  "step_started",
  "step_finished",
  "breakpoint",
  "gate_released",
  "card_fetch",
  "rpc_request",
  "rpc_response",
  "stream_event",
  "policy_denied",
  "task_state",
  "push_received",
  "agent_request",
  "agent_work",
  "artifact",
  "fault_injected",
  "llm_request",
  "llm_response",
  "llm_unavailable",
  "mcp_request",
  "mcp_response",
  "mcp_progress",
  "mcp_denied",
  "mcp_server_call",
  "llm_tool_choice",
  "gateway_access",
] as const;
export type EventKind = (typeof EVENT_KINDS)[number];

const opt = z.string().optional();

export const AtlasEventSchema = z.object({
  id: z.number(),
  ts: z.string(),
  produced_ts: opt,
  session: opt,
  run_id: opt,
  scenario: opt,
  step: opt,
  source: z.string(),
  kind: z.string(),
  from: opt,
  to: opt,
  via: opt,
  method: opt,
  task_id: opt,
  context_id: opt,
  state_from: opt,
  state_to: opt,
  route: opt,
  http_status: z.number().optional(),
  duration_ms: z.number().optional(),
  corr: opt,
  payload: z.unknown().optional(),
  payload_truncated: z.boolean().optional(),
  note: opt,
});

/** An event plus `t`: its time in ms (producer time, falling back to receipt time). */
export type AtlasEvent = z.infer<typeof AtlasEventSchema> & { t: number };

const ZERO_TIME = "0001-01-01T00:00:00Z";

/** Validate a raw event; null when it does not match the contract. */
export function toAtlasEvent(raw: unknown): AtlasEvent | null {
  const r = AtlasEventSchema.safeParse(raw);
  if (!r.success) return null;
  const e = r.data;
  const produced = e.produced_ts && e.produced_ts !== ZERO_TIME ? Date.parse(e.produced_ts) : NaN;
  const t = Number.isFinite(produced) ? produced : Date.parse(e.ts);
  return { ...e, t: Number.isFinite(t) ? t : 0 };
}

/** Parse a JSONL text (one event per line); malformed lines are skipped. */
export function parseJsonl(text: string): AtlasEvent[] {
  const out: AtlasEvent[] = [];
  for (const line of text.split(/\r?\n/)) {
    if (!line.trim()) continue;
    try {
      const e = toAtlasEvent(JSON.parse(line));
      if (e) out.push(e);
    } catch {
      // skip malformed line
    }
  }
  return out;
}

const PlaceSchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string().default(""),
  city: z.string(),
  country: z.string().default(""),
  lat: z.number(),
  lon: z.number(),
});
export type Place = z.infer<typeof PlaceSchema>;

const SkillSchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string().default(""),
  tags: z.array(z.string()).optional(),
});

export const FEATURES = ["streaming", "multi_turn", "long_task_push", "restricted"] as const;
export type Feature = (typeof FEATURES)[number];

const AgentSchema = PlaceSchema.extend({
  feature: z.enum(FEATURES),
  mode: z.enum(["scripted", "llm"]),
  skills: z.array(SkillSchema),
  /** Script steps; only the MCP references (tool, resource, tools, prompt_from) are read by the UI. */
  script: z
    .array(
      z.object({
        tool: z.object({ server: z.string(), name: z.string(), expect: z.string().optional() }).optional(),
        resource: z.object({ server: z.string(), uri: z.string() }).optional(),
        tools: z.array(z.string()).optional(),
        prompt_from: z.object({ server: z.string(), name: z.string() }).optional(),
      }),
    )
    .default([]),
});

const MCPServerSchema = PlaceSchema.extend({
  role: z.enum(["data", "ops"]),
  tools: z.array(
    z.object({
      name: z.string(),
      description: z.string().default(""),
      params: z.array(z.object({ name: z.string(), type: z.string(), enum: z.array(z.string()).optional() })).default([]),
      progress: z.object({ steps: z.number() }).optional(),
      write: z.boolean().optional(),
    }),
  ),
  resources: z.array(z.object({ uri: z.string(), name: z.string(), mime: z.string().default("") })).default([]),
  prompts: z.array(z.object({ name: z.string(), description: z.string().default("") })).default([]),
});
export type MCPServer = z.infer<typeof MCPServerSchema>;
export type Agent = z.infer<typeof AgentSchema>;

const PlanStepSchema = z.object({
  step: z.string(),
  agent: z.string(),
  call: z.enum(["send", "stream", "push"]),
  skill: z.string(),
  text: z.string(),
  say: z.string(),
  reply: z.string().optional(),
  expect: z.string().optional(),
  parallel: z.boolean().optional(),
});
export type PlanStep = z.infer<typeof PlanStepSchema>;

export const ScenarioSchema = z.object({
  id: z.string(),
  code: z.string(),
  title: z.string(),
  summary: z.string().default(""),
  color: z.string(),
  orchestrator: PlaceSchema,
  agents: z.array(AgentSchema),
  plan: z.array(PlanStepSchema),
  closing: z.string().default(""),
  mcp_servers: z.array(MCPServerSchema).default([]),
});
export type Scenario = z.infer<typeof ScenarioSchema>;

export const ScenarioListSchema = z.array(ScenarioSchema);

export const RunInfoSchema = z.object({
  run_id: z.string(),
  scenario: z.string(),
  started: z.string(),
  finished: z.string().optional(),
  note: z.string().optional(),
});
export type RunInfo = z.infer<typeof RunInfoSchema>;

export const GateSchema = z.object({
  id: z.string(),
  run_id: z.string(),
  scenario: z.string(),
  step: z.string(),
  from: z.string().default(""),
  to: z.string().default(""),
  note: z.string().default(""),
  since: z.string(),
});
export type Gate = z.infer<typeof GateSchema>;

export const SessionInfoSchema = z.object({
  id: z.string(),
  bytes: z.number().optional(),
  modified: z.string().optional(),
  current: z.boolean().optional(),
});
export type SessionInfo = z.infer<typeof SessionInfoSchema>;
