"use client";

import { useState } from "react";
import type { Scenario } from "@/events/schema";
import { releaseGate } from "@/lib/api";
import type { GateView } from "@/replay/reduce";
import { ScenarioBadge } from "./ScenarioBadge";

/** Breakpoints waiting for a person: continue or abort, one or all. */
export function GatePrompt({ gates, scenarios, live }: { gates: GateView[]; scenarios: Scenario[]; live: boolean }) {
  const [error, setError] = useState<string | null>(null);
  if (gates.length === 0) return null;
  const act = async (body: Parameters<typeof releaseGate>[0]) => {
    setError(null);
    try {
      await releaseGate(body);
    } catch (e) {
      setError((e as Error).message);
    }
  };
  return (
    <div className="pointer-events-auto max-w-sm space-y-2 rounded-lg border border-serious bg-surface-1 p-3 shadow-lg" role="dialog" aria-label="Breakpoints" data-testid="gate-prompt">
      <div className="flex items-center justify-between text-sm font-semibold">
        <span>❚❚ {gates.length === 1 ? "Breakpoint" : `${gates.length} breakpoints`}</span>
        {live && gates.length > 1 ? (
          <button type="button" className="rounded-md border border-line px-2 py-0.5 text-xs" onClick={() => act({ all: true })}>
            Continue all
          </button>
        ) : null}
      </div>
      <ul className="space-y-2">
        {gates.map((g) => (
          <li key={g.corr} className="text-xs">
            <div className="flex items-center gap-1">
              <ScenarioBadge code={g.scenario} scenarios={scenarios} />
              <span className="font-semibold">before {g.step}</span>
              <span className="text-ink-3">→ {g.to}</span>
            </div>
            <p className="my-1 text-ink-2">{g.note.replace(/^Breakpoint before [^:]+: /, "")}</p>
            {live ? (
              <div className="flex gap-2">
                <button type="button" className="rounded-md bg-ink px-2 py-0.5 font-semibold text-surface-1" onClick={() => act({ id: g.corr })} data-testid="gate-continue">
                  ▷ Continue
                </button>
                <button type="button" className="rounded-md border border-line px-2 py-0.5" onClick={() => act({ id: g.corr, action: "abort" })}>
                  Abort run
                </button>
              </div>
            ) : (
              <p className="text-ink-3">(recorded breakpoint)</p>
            )}
          </li>
        ))}
      </ul>
      {error ? <p className="text-xs text-critical">{error}</p> : null}
    </div>
  );
}
