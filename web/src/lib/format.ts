import type { AtlasEvent } from "@/events/schema";

export type KindGroup = "story" | "wire" | "mcp" | "agent" | "gateway";

export interface KindInfo {
  label: string;
  group: KindGroup;
  icon: string;
}

/** How each event kind is named and grouped in the UI. */
export const KIND_INFO: Record<string, KindInfo> = {
  run_started: { label: "run started", group: "story", icon: "▶" },
  run_finished: { label: "run finished", group: "story", icon: "■" },
  step_started: { label: "step", group: "story", icon: "→" },
  step_finished: { label: "step done", group: "story", icon: "✓" },
  breakpoint: { label: "breakpoint", group: "story", icon: "❚❚" },
  gate_released: { label: "continue", group: "story", icon: "▷" },
  card_fetch: { label: "agent card", group: "wire", icon: "▣" },
  rpc_request: { label: "request", group: "wire", icon: "↗" },
  rpc_response: { label: "response", group: "wire", icon: "↙" },
  stream_event: { label: "SSE frame", group: "wire", icon: "≋" },
  policy_denied: { label: "denied (403)", group: "wire", icon: "⛔" },
  task_state: { label: "task state", group: "wire", icon: "◆" },
  push_received: { label: "push", group: "wire", icon: "⚑" },
  agent_request: { label: "arrived", group: "agent", icon: "⇣" },
  agent_work: { label: "agent", group: "agent", icon: "…" },
  artifact: { label: "artifact", group: "agent", icon: "▤" },
  fault_injected: { label: "503 chaos", group: "agent", icon: "⚡" },
  llm_request: { label: "LLM prompt", group: "agent", icon: "✎" },
  llm_response: { label: "LLM answer", group: "agent", icon: "✎" },
  llm_unavailable: { label: "LLM down", group: "agent", icon: "✎" },
  mcp_request: { label: "MCP request", group: "mcp", icon: "⚒" },
  mcp_response: { label: "MCP result", group: "mcp", icon: "⚒" },
  mcp_progress: { label: "MCP progress", group: "mcp", icon: "⋯" },
  mcp_denied: { label: "MCP denied", group: "mcp", icon: "⛔" },
  mcp_server_call: { label: "tool server", group: "mcp", icon: "⬡" },
  llm_tool_choice: { label: "model picks tool", group: "mcp", icon: "✎" },
  gateway_access: { label: "gateway log", group: "gateway", icon: "⛩" },
};

export const GROUP_LABEL: Record<KindGroup, string> = {
  story: "Story",
  wire: "A2A",
  mcp: "MCP",
  agent: "Agents",
  gateway: "Gateway",
};

export function kindInfo(kind: string): KindInfo {
  return KIND_INFO[kind] ?? { label: kind, group: "agent", icon: "·" };
}

/** 14:03:07.250 */
export function clock(t: number): string {
  if (!Number.isFinite(t) || t <= 0) return "--:--:--";
  const d = new Date(t);
  const p = (n: number, w = 2) => String(n).padStart(w, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`;
}

/** +12.3 s */
export function relative(ms: number): string {
  const s = ms / 1000;
  return `${s >= 0 ? "+" : ""}${s.toFixed(1)} s`;
}

export function shortId(id: string | undefined, n = 8): string {
  return id ? (id.length > n ? id.slice(0, n) + "…" : id) : "";
}

/** Who → whom, for lists. */
export function route(e: AtlasEvent): string {
  if (e.from && e.to) return `${e.from} → ${e.to}`;
  return e.from ?? e.to ?? "";
}
