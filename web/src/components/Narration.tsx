"use client";

import type { Scenario } from "@/events/schema";
import type { ViewState } from "@/replay/reduce";

/** Subtitles: the latest thing each orchestrator said, per visible scenario. */
export function Narration({ view, scenarios, visible, playhead, onSelect }: { view: ViewState; scenarios: Scenario[]; visible: (code: string) => boolean; playhead: number; onSelect: (eventId: number) => void }) {
  const lines = scenarios
    .filter((s) => visible(s.code))
    .map((s) => ({ s, n: view.narration.get(s.code) }))
    .filter((x) => x.n && playhead - x.n.t < 30_000);
  if (lines.length === 0) return null;
  return (
    <ul className="pointer-events-auto max-w-[44rem] space-y-1" aria-live="polite" data-testid="narration">
      {lines.map(({ s, n }) => (
        <li key={s.code}>
          <button
            type="button"
            onClick={() => onSelect(n!.eventId)}
            title={n!.note}
            className="flex w-full items-start gap-2 rounded-md bg-[rgba(252,252,251,0.92)] px-2 py-1 text-left text-[13px] text-[#0b0b0b] shadow"
            style={{ borderLeft: `4px solid ${s.color}` }}
          >
            <span className="font-bold">{s.code.toUpperCase()}</span>
            <span className="line-clamp-1">{n!.note}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}
