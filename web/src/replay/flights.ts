/**
 * Flights: the animated messages on the map. Each wire event becomes a flight
 * along one or more legs between nodes (agent ids, the gateway hub, the LLM).
 * A flight starts at the event time and lasts LEG_MS per leg, in event time,
 * so slow replay shows it slowly.
 */
import type { AtlasEvent } from "@/events/schema";

export const HUB = "@gateway";
export const LLM = "@llm";
export const LEG_MS = 900;
export const MAX_LEGS = 4;
/** Flights stay visible as a fading trail for this long after they land. */
export const TRAIL_MS = 1200;
export const FLIGHT_WINDOW_MS = MAX_LEGS * LEG_MS + TRAIL_MS;

export type FlightStyle = "send" | "stream" | "frame" | "card" | "push" | "denied" | "fault" | "llm" | "mcp" | "mcp-frame" | "mcp-handshake";

/** MCP methods that do work on a server (drawn to the server pin); everything else is the session with the gateway. */
const MCP_WORK = new Set(["tools/call", "resources/read", "prompts/get"]);

function mcpLabel(e: AtlasEvent): string {
  const p = e.payload as { params?: { name?: string; uri?: string } } | undefined;
  const subject = p?.params?.name ?? p?.params?.uri ?? "";
  return `MCP ${subject || e.method || ""}`.trim();
}

export interface Flight {
  event: AtlasEvent;
  scenario: string;
  legs: [string, string][];
  style: FlightStyle;
  label?: string;
  /** 0..1 along all legs; > 1 while the trail fades. */
  progress: number;
  /** 0..1, 1 while flying, fading to 0 during the trail. */
  alpha: number;
  /** Where a blocked flight ends (draw an ✕). */
  blockedAt?: string;
}

function orchestratorOf(e: AtlasEvent): string {
  return `${e.scenario}-orchestrator`;
}

/** Legs and style of an event, or null when the event is not a message. */
export function routeOf(e: AtlasEvent, deniedCorr: ReadonlySet<string>): Omit<Flight, "progress" | "alpha" | "event" | "scenario"> | null {
  const orch = orchestratorOf(e);
  switch (e.kind) {
    case "card_fetch":
      return { legs: [[orch, HUB], [HUB, e.to ?? ""], [e.to ?? "", HUB], [HUB, orch]], style: "card", label: "agent card" };
    case "rpc_request":
      if (e.corr && deniedCorr.has(e.corr)) {
        return { legs: [[orch, HUB]], style: "denied", label: e.method, blockedAt: HUB };
      }
      return { legs: [[orch, HUB], [HUB, e.to ?? ""]], style: e.method === "SendStreamingMessage" ? "stream" : "send", label: e.method };
    case "rpc_response":
      if (e.method === "SendStreamingMessage") return null; // the frames were the response
      return { legs: [[e.from ?? "", HUB], [HUB, orch]], style: "send", label: `${e.method ?? ""} result` };
    case "stream_event":
      return { legs: [[e.from ?? "", HUB], [HUB, orch]], style: "frame" };
    case "push_received":
      return { legs: [[e.from ?? "", orch]], style: "push", label: "push" };
    case "policy_denied":
      return { legs: [[HUB, orch]], style: "denied", label: "403 denied" };
    case "fault_injected":
      return { legs: [[e.from ?? "", HUB]], style: "fault", label: "503 → retry", blockedAt: HUB };
    case "llm_request":
      return { legs: [[e.from ?? "", HUB], [HUB, LLM]], style: "llm", label: "LLM prompt" };
    case "llm_response":
      return { legs: [[LLM, HUB], [HUB, e.to ?? ""]], style: "llm", label: "LLM answer" };
    case "llm_unavailable":
      return { legs: [[HUB, e.from ?? ""]], style: "fault", label: "LLM down" };
    case "mcp_request": {
      const agent = e.from ?? "";
      if (!MCP_WORK.has(e.method ?? "") || !e.to || e.to === "agentgateway") {
        return { legs: [[agent, HUB]], style: "mcp-handshake" };
      }
      if (e.corr && deniedCorr.has(e.corr)) {
        return { legs: [[agent, HUB]], style: "denied", label: mcpLabel(e), blockedAt: HUB };
      }
      return { legs: [[agent, HUB], [HUB, e.to]], style: "mcp", label: mcpLabel(e) };
    }
    case "mcp_response":
      if (!MCP_WORK.has(e.method ?? "") || !e.from || e.from === "agentgateway") return { legs: [[HUB, e.to ?? ""]], style: "mcp-handshake" };
      return { legs: [[e.from, HUB], [HUB, e.to ?? ""]], style: "mcp" };
    case "mcp_progress":
      return { legs: [[e.from ?? "", HUB], [HUB, e.to ?? ""]], style: "mcp-frame" };
    case "mcp_denied":
      return { legs: [[HUB, e.to ?? ""]], style: "denied", label: "MCP denied" };
    default:
      return null;
  }
}

/**
 * Flights visible at `playhead`, given the events in [playhead - FLIGHT_WINDOW_MS, playhead]
 * and the correlation ids of requests the gateway refused.
 */
export function flightsAt(windowEvents: readonly AtlasEvent[], playhead: number, deniedCorr: ReadonlySet<string>, visible: (e: AtlasEvent) => boolean): Flight[] {
  const out: Flight[] = [];
  for (const e of windowEvents) {
    if (e.t > playhead || !visible(e)) continue;
    const r = routeOf(e, deniedCorr);
    if (!r) continue;
    const dur = r.legs.length * LEG_MS;
    const elapsed = playhead - e.t;
    if (elapsed > dur + TRAIL_MS) continue;
    const progress = elapsed / dur;
    const alpha = elapsed <= dur ? 1 : 1 - (elapsed - dur) / TRAIL_MS;
    out.push({ ...r, event: e, scenario: e.scenario ?? "", progress, alpha });
  }
  return out;
}

/** Correlation ids of requests refused by the gateway (policy_denied events). */
export function deniedCorrs(events: readonly AtlasEvent[]): Set<string> {
  const s = new Set<string>();
  for (const e of events) if ((e.kind === "policy_denied" || e.kind === "mcp_denied") && e.corr) s.add(e.corr);
  return s;
}
