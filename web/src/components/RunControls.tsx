"use client";

import { useState } from "react";
import type { Scenario } from "@/events/schema";
import { startRun } from "@/lib/api";

const AGENT_SPEEDS = [0.5, 1, 2, 4] as const;

/** Start runs on the orchestrators (through the visualizer). */
export function RunControls({ scenarios, disabled, onStarted }: { scenarios: Scenario[]; disabled: boolean; onStarted: (msg: string) => void }) {
  const [scenario, setScenario] = useState("all");
  const [speed, setSpeed] = useState(1);
  const [chaos, setChaos] = useState(false);
  const [breakpoints, setBreakpoints] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async () => {
    setBusy(true);
    setError(null);
    try {
      const runs = await startRun({ scenario, speed, chaos, breakpoints });
      onStarted(`Started ${runs.map((r) => r.run_id).join(", ")}`);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const sel = "rounded-md border border-line bg-surface-2 px-1.5 py-1 text-sm";
  return (
    <div className="flex flex-wrap items-center gap-2" data-testid="run-controls">
      <select className={sel} value={scenario} onChange={(e) => setScenario(e.target.value)} aria-label="Scenario to run" disabled={disabled}>
        <option value="all">All 5 scenarios</option>
        {scenarios.map((s) => (
          <option key={s.code} value={s.code}>
            {s.code.toUpperCase()} · {s.title}
          </option>
        ))}
      </select>
      <label className="flex items-center gap-1 text-xs text-ink-2" title="Divides every scripted wait of the agents">
        Agents
        <select className={sel} value={speed} onChange={(e) => setSpeed(Number(e.target.value))} disabled={disabled} aria-label="Agent speed">
          {AGENT_SPEEDS.map((s) => (
            <option key={s} value={s}>
              {s}×
            </option>
          ))}
        </select>
      </label>
      <label className="flex items-center gap-1 text-xs" title="The streaming agent answers 503 to the first attempt; the gateway's retry policy resends.">
        <input type="checkbox" checked={chaos} onChange={(e) => setChaos(e.target.checked)} disabled={disabled} /> Chaos
      </label>
      <label className="flex items-center gap-1 text-xs" title="Orchestrators wait before every delegation until you press Continue.">
        <input type="checkbox" checked={breakpoints} onChange={(e) => setBreakpoints(e.target.checked)} disabled={disabled} /> Breakpoints
      </label>
      <button
        type="button"
        className="rounded-md bg-ink px-3 py-1 text-sm font-semibold text-surface-1 hover:opacity-90 disabled:opacity-40"
        onClick={run}
        disabled={disabled || busy}
        data-testid="run-button"
      >
        {busy ? "Starting…" : "▶ Run"}
      </button>
      {error ? (
        <span className="text-xs text-critical" role="alert">
          {error}
        </span>
      ) : null}
    </div>
  );
}
