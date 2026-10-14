"use client";

import type { AtlasEvent, Scenario } from "@/events/schema";
import { GATEWAY_UI_URL, JAEGER_URL } from "@/lib/api";
import { clock } from "@/lib/format";
import { ScenarioBadge } from "./ScenarioBadge";

/** What the gateway saw: its access log, next to what the agents report. */
export function GatewayPanel({ events, scenarios, onSelect }: { events: readonly AtlasEvent[]; scenarios: Scenario[]; onSelect: (e: AtlasEvent) => void }) {
  return (
    <div className="space-y-3 p-3" data-testid="gateway-panel">
      <p className="text-xs text-ink-2">
        agentgateway understands A2A: for every call it logs the JSON-RPC method, the task state and the context id, adds the run id and caller headers, and reports retries.
        Policies per route: streaming agents retry 503s, long-task agents get a 2 s delay, restricted agents only accept their orchestrator and no <code>restricted-*</code> skills.
      </p>
      <div className="flex gap-3 text-xs">
        <a className="underline" href={JAEGER_URL} target="_blank" rel="noreferrer">
          Jaeger traces
        </a>
        <a className="underline" href={GATEWAY_UI_URL} target="_blank" rel="noreferrer">
          Gateway UI
        </a>
      </div>
      <table className="w-full text-left text-xs">
        <thead className="text-ink-3">
          <tr>
            <th className="py-1 font-normal">time</th>
            <th className="font-normal">sc</th>
            <th className="font-normal">call</th>
            <th className="font-normal">route</th>
            <th className="text-right font-normal">HTTP</th>
            <th className="text-right font-normal">ms</th>
          </tr>
        </thead>
        <tbody>
          {events.length === 0 ? (
            <tr>
              <td colSpan={6} className="py-3 text-ink-3">
                No gateway log lines yet.
              </td>
            </tr>
          ) : null}
          {events.map((e) => {
            const retry = Number((e.payload as Record<string, unknown> | undefined)?.["retry.attempt"] ?? 0);
            return (
              <tr key={e.id} className="cursor-pointer border-t border-line hover:bg-surface-0" onClick={() => onSelect(e)}>
                <td className="py-1 font-mono tabular-nums text-ink-3">{clock(e.t).slice(0, 8)}</td>
                <td>
                  <ScenarioBadge code={e.scenario} scenarios={scenarios} />
                </td>
                <td className="max-w-[9rem] font-mono" title={e.method}>
                  <span className="block truncate">{e.method}</span>
                  {retry > 0 ? <span className="rounded bg-serious px-1 font-sans text-[10px] text-black">↻ retried ×{retry}</span> : null}
                </td>
                <td className="max-w-[7rem] truncate">{e.to}</td>
                <td className={`text-right font-mono ${e.http_status && e.http_status >= 400 ? "font-bold text-critical" : ""}`}>{e.http_status}</td>
                <td className="text-right font-mono tabular-nums">{e.duration_ms ?? ""}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
