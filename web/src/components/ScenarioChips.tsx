"use client";

import type { Scenario } from "@/events/schema";
import type { RunView } from "@/replay/reduce";

export interface ScenarioChipsProps {
  scenarios: Scenario[];
  solo: string | null;
  muted: ReadonlySet<string>;
  runs: Map<string, RunView>;
  onSolo: (code: string | null) => void;
  onMute: (code: string) => void;
}

/**
 * One chip per scenario. Click: solo (everything else dims). The eye button
 * mutes a scenario. Each chip shows the latest run's progress.
 */
export function ScenarioChips({ scenarios, solo, muted, runs, onSolo, onMute }: ScenarioChipsProps) {
  const latest = new Map<string, RunView>();
  for (const r of runs.values()) {
    const cur = latest.get(r.scenario);
    if (!cur || r.started > cur.started) latest.set(r.scenario, r);
  }
  return (
    <ul className="space-y-1" data-testid="scenario-chips">
      {scenarios.map((s) => {
        const active = solo ? solo === s.code : !muted.has(s.code);
        const run = latest.get(s.code);
        const done = run ? [...run.steps.values()].filter((x) => x.state !== "running").length : 0;
        const status = !run ? "no run yet" : run.finished ? (run.ok ? "finished" : "finished with problems") : `running · ${run.step ?? ""}`;
        return (
          <li key={s.code} className={`flex items-stretch gap-1 rounded-md border ${solo === s.code ? "border-ink" : "border-line"} ${active ? "" : "opacity-45"}`}>
            <button
              type="button"
              className="flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5 text-left"
              onClick={() => onSolo(solo === s.code ? null : s.code)}
              aria-pressed={solo === s.code}
              title={solo === s.code ? "Show all scenarios" : `Show only ${s.title}`}
              data-testid={`chip-${s.code}`}
            >
              <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded text-[10px] font-bold text-white" style={{ background: s.color }}>
                {s.code.toUpperCase()}
              </span>
              <span className="min-w-0">
                <span className="block truncate text-sm font-medium">{s.title}</span>
                <span className="block truncate text-[11px] text-ink-3">
                  {s.orchestrator.city} · {status}
                  {run ? ` · ${done}/${s.plan.length}` : ""}
                </span>
              </span>
            </button>
            <button
              type="button"
              className="px-2 text-ink-3 hover:text-ink"
              onClick={() => onMute(s.code)}
              aria-pressed={muted.has(s.code)}
              aria-label={muted.has(s.code) ? `Show ${s.title}` : `Hide ${s.title}`}
              title={muted.has(s.code) ? "Show" : "Hide"}
            >
              {muted.has(s.code) ? "◌" : "◉"}
            </button>
          </li>
        );
      })}
    </ul>
  );
}
