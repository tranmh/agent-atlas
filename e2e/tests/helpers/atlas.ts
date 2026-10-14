import { expect } from "@playwright/test";
import { openSse, type SseStream } from "./sse";
import { VIZ_URL } from "./env";

export interface AtlasEvent {
  id: number;
  kind: string;
  run_id?: string;
  scenario?: string;
  step?: string;
  from?: string;
  to?: string;
  method?: string;
  state_to?: string;
  http_status?: number;
  note?: string;
  payload?: any;
}

/** Live event stream of the visualizer, positioned at the current end. */
export async function openEvents(): Promise<SseStream> {
  const health = await (await fetch(`${VIZ_URL}/healthz`)).json();
  return openSse(`${VIZ_URL}/api/events/stream`, { headers: { "Last-Event-ID": `${health.session}/${health.events}` } });
}

export async function startRun(body: { scenario: string; speed?: number; chaos?: boolean; breakpoints?: boolean }): Promise<string[]> {
  const res = await fetch(`${VIZ_URL}/api/runs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ speed: 8, chaos: false, breakpoints: false, ...body }),
  });
  expect(res.status).toBe(202);
  return ((await res.json()) as { run_id: string }[]).map((r) => r.run_id);
}

/** All events of one run, read from the stream until run_finished (+ a grace period for gateway logs). */
export async function collectRun(stream: SseStream, runId: string, timeoutMs = 90_000): Promise<AtlasEvent[]> {
  await stream.waitFor((e) => e.event === "atlas" && e.json?.kind === "run_finished" && e.json?.run_id === runId, timeoutMs, `run_finished of ${runId}`);
  await new Promise((r) => setTimeout(r, 3000));
  return stream.events.filter((e) => e.event === "atlas" && e.json?.run_id === runId).map((e) => e.json as AtlasEvent);
}
