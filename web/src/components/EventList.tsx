"use client";

import { memo } from "react";
import type { AtlasEvent, Scenario } from "@/events/schema";
import { clock, GROUP_LABEL, kindInfo, type KindGroup } from "@/lib/format";
import { stateLook } from "@/map/colors";
import { ScenarioBadge } from "./ScenarioBadge";

export interface EventListProps {
  events: readonly AtlasEvent[];
  scenarios: Scenario[];
  groups: ReadonlySet<KindGroup>;
  selectedId: number | null;
  onToggleGroup: (g: KindGroup) => void;
  onSelect: (e: AtlasEvent) => void;
}

/** Events up to the playhead, newest first. */
export const EventList = memo(function EventList({ events, scenarios, groups, selectedId, onToggleGroup, onSelect }: EventListProps) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-wrap gap-1 pb-2" role="group" aria-label="Event filters">
        {(Object.keys(GROUP_LABEL) as KindGroup[]).map((g) => (
          <button
            key={g}
            type="button"
            className={`rounded-full border px-2 py-0.5 text-[11px] ${groups.has(g) ? "border-ink bg-ink text-surface-1" : "border-line text-ink-2"}`}
            aria-pressed={groups.has(g)}
            onClick={() => onToggleGroup(g)}
          >
            {GROUP_LABEL[g]}
          </button>
        ))}
      </div>
      <ol className="min-h-0 flex-1 space-y-px overflow-y-auto pr-1" data-testid="event-list" aria-label="Events">
        {events.length === 0 ? <li className="px-1 py-4 text-sm text-ink-3">No events yet. Start a run.</li> : null}
        {events.map((e) => {
          const info = kindInfo(e.kind);
          const look = e.state_to ? stateLook(e.state_to) : null;
          return (
            <li key={`${e.session}/${e.id}`}>
              <button
                type="button"
                onClick={() => onSelect(e)}
                className={`w-full rounded px-1.5 py-1 text-left text-xs ${selectedId === e.id ? "bg-surface-0 ring-1 ring-accent" : "hover:bg-surface-0"}`}
                data-kind={e.kind}
              >
                <span className="flex items-center gap-1.5">
                  <span className="font-mono text-[10px] tabular-nums text-ink-3">{clock(e.t).slice(0, 8)}</span>
                  <ScenarioBadge code={e.scenario} scenarios={scenarios} />
                  <span className={`font-semibold ${e.kind === "policy_denied" || e.kind === "fault_injected" || e.kind === "mcp_denied" ? "text-critical" : ""}`}>
                    {info.icon} {info.label}
                  </span>
                  {e.method ? <span className="truncate font-mono text-[10.5px] text-ink-2">{e.method}</span> : null}
                  {look && look.glyph ? (
                    <span className="ml-auto shrink-0 rounded px-1 text-[10px]" style={{ background: look.color, color: "#fff" }} title={look.label}>
                      {look.glyph} {look.label}
                    </span>
                  ) : null}
                </span>
                {e.note ? <span className="mt-0.5 line-clamp-2 block text-ink-2">{e.note}</span> : null}
              </button>
            </li>
          );
        })}
      </ol>
    </div>
  );
});
