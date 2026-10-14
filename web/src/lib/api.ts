/** Typed client for the visualizer API (cmd/visualizer). */
import { z } from "zod";
import {
  GateSchema,
  ScenarioListSchema,
  SessionInfoSchema,
  toAtlasEvent,
  type AtlasEvent,
  type Gate,
  type Scenario,
  type SessionInfo,
} from "@/events/schema";

export const VISUALIZER_URL = (process.env.NEXT_PUBLIC_VISUALIZER_URL ?? "http://localhost:8095").replace(/\/$/, "");
export const GATEWAY_URL = (process.env.NEXT_PUBLIC_GATEWAY_URL ?? "http://localhost:8000").replace(/\/$/, "");
export const JAEGER_URL = (process.env.NEXT_PUBLIC_JAEGER_URL ?? "http://localhost:16686").replace(/\/$/, "");
export const GATEWAY_UI_URL = (process.env.NEXT_PUBLIC_GATEWAY_UI_URL ?? "http://localhost:15000/ui").replace(/\/$/, "");

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status?: number,
  ) {
    super(message);
  }
}

async function request<S extends z.ZodTypeAny>(path: string, schema: S, init?: RequestInit): Promise<z.infer<S>> {
  let res: Response;
  try {
    res = await fetch(VISUALIZER_URL + path, { cache: "no-store", ...init });
  } catch (e) {
    throw new ApiError(`visualizer unreachable (${VISUALIZER_URL}): ${(e as Error).message}`);
  }
  const text = await res.text();
  if (!res.ok) {
    let msg = text;
    try {
      msg = (JSON.parse(text) as { message?: string }).message ?? text;
    } catch {
      // not JSON
    }
    throw new ApiError(`${res.status}: ${msg}`, res.status);
  }
  const parsed = schema.safeParse(text ? JSON.parse(text) : null);
  if (!parsed.success) throw new ApiError(`unexpected response from ${path}: ${parsed.error.message}`);
  return parsed.data;
}

export function fetchScenarios(): Promise<Scenario[]> {
  return request("/api/scenarios", ScenarioListSchema);
}

export interface RunRequest {
  scenario: string; // code or "all"
  speed: number;
  chaos: boolean;
  breakpoints: boolean;
}

export function startRun(req: RunRequest): Promise<{ run_id: string; scenario: string }[]> {
  return request("/api/runs", z.array(z.object({ run_id: z.string(), scenario: z.string() })), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

export function fetchGates(): Promise<Gate[]> {
  return request("/api/gates", z.array(GateSchema));
}

export function releaseGate(body: { id?: string; run_id?: string; all?: boolean; action?: "continue" | "abort" }): Promise<{ released: number }> {
  return request("/api/gate/release", z.object({ released: z.number() }), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

export function fetchSessions(): Promise<SessionInfo[]> {
  return request("/api/sessions", z.array(SessionInfoSchema));
}

export async function fetchSessionEvents(id: string): Promise<AtlasEvent[]> {
  const raw = await request(`/api/sessions/${encodeURIComponent(id)}/events`, z.array(z.unknown()));
  return raw.map(toAtlasEvent).filter((e): e is AtlasEvent => e !== null);
}

export function sessionDownloadUrl(id: string): string {
  return `${VISUALIZER_URL}/api/sessions/${encodeURIComponent(id)}/events?format=jsonl`;
}

/** Fetch an agent card through the gateway, as any A2A client would. */
export async function fetchAgentCard(agentId: string): Promise<unknown> {
  const res = await fetch(`${GATEWAY_URL}/agents/${encodeURIComponent(agentId)}/.well-known/agent-card.json`, { cache: "no-store" });
  if (!res.ok) throw new ApiError(`gateway answered ${res.status}`, res.status);
  return res.json();
}
