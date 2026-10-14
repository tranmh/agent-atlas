"use client";

import type { Scenario } from "@/events/schema";
import { relative } from "@/lib/format";
import { stateLook } from "@/map/colors";
import type { TaskView } from "@/replay/reduce";
import { ScenarioBadge } from "./ScenarioBadge";

const NODES: Record<string, { x: number; y: number }> = {
  submitted: { x: 50, y: 80 },
  working: { x: 160, y: 80 },
  "input-required": { x: 160, y: 165 },
  completed: { x: 290, y: 30 },
  failed: { x: 290, y: 80 },
  canceled: { x: 290, y: 130 },
  rejected: { x: 290, y: 180 },
};

const EDGES: [string, string][] = [
  ["submitted", "working"],
  ["working", "input-required"],
  ["input-required", "working"],
  ["working", "completed"],
  ["working", "failed"],
  ["working", "canceled"],
  ["submitted", "rejected"],
  ["submitted", "completed"],
];

const HALF_W = 46;
const HALF_H = 13;

/** Where the segment from the centre of a pill towards `to` leaves the pill (plus a small gap). */
export function border(from: { x: number; y: number }, to: { x: number; y: number }): { x: number; y: number } {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  if (dx === 0 && dy === 0) return from;
  const k = Math.min(dx === 0 ? Infinity : HALF_W / Math.abs(dx), dy === 0 ? Infinity : HALF_H / Math.abs(dy));
  const len = Math.hypot(dx, dy);
  const gap = 3 / len;
  return { x: from.x + dx * (k + gap), y: from.y + dy * (k + gap) };
}

const EDGE_SET = new Set(EDGES.map(([a, b]) => `${a}>${b}`));

/**
 * The path a task took. A blocking client only sees some states (for example
 * input-required, then completed); a missing step through "working" is
 * marked as inferred.
 */
export function path(states: string[]): { visited: Set<string>; inferred: Set<string>; taken: Set<string> } {
  const visited = new Set(states);
  const inferred = new Set<string>();
  const taken = new Set<string>();
  for (let i = 1; i < states.length; i++) {
    const a = states[i - 1];
    const b = states[i];
    if (EDGE_SET.has(`${a}>${b}`)) taken.add(`${a}>${b}`);
    else if (EDGE_SET.has(`${a}>working`) && EDGE_SET.has(`working>${b}`)) {
      taken.add(`${a}>working`);
      taken.add(`working>${b}`);
      if (!visited.has("working")) inferred.add("working");
    }
  }
  return { visited, inferred, taken };
}

/** The A2A task state machine with the path this task took. */
export function TaskStatePanel({ task, scenarios, onEvent }: { task: TaskView | null; scenarios: Scenario[]; onEvent: (id: number) => void }) {
  if (!task) {
    return (
      <p className="p-4 text-sm text-ink-3">
        Select an event that belongs to a task (a request, a stream frame, a push) to see the task&apos;s state machine. Requests refused by the gateway never become tasks.
      </p>
    );
  }
  const { visited, inferred, taken } = path(task.history.filter((h) => !h.state.startsWith("note:")).map((h) => h.state));
  const start = task.history[0]?.t ?? 0;
  return (
    <div className="space-y-3 p-3" data-testid="task-panel">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <ScenarioBadge code={task.scenario} scenarios={scenarios} />
        <span className="font-semibold">Task at {task.agent}</span>
        <span className="font-mono text-[11px] text-ink-3" title={task.taskId}>
          {task.taskId.slice(0, 13)}…
        </span>
      </div>
      <svg viewBox="0 0 360 210" className="w-full max-w-[26rem]" role="img" aria-label={`Task state machine, current state ${task.current}`}>
        <defs>
          <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
            <path d="M0,0 L10,5 L0,10 z" fill="currentColor" />
          </marker>
        </defs>
        {EDGES.map(([a, b]) => {
          const on = taken.has(`${a}>${b}`);
          // The two directions between working and input-required run side by side.
          const lane = (a === "working" && b === "input-required") || (a === "input-required" && b === "working") ? (a === "working" ? -12 : 12) : 0;
          const p = { x: NODES[a].x + lane, y: NODES[a].y };
          const q = { x: NODES[b].x + lane, y: NODES[b].y };
          const s1 = border(p, q);
          const e1 = border(q, p);
          return (
            <line
              key={`${a}-${b}`}
              x1={s1.x}
              y1={s1.y}
              x2={e1.x}
              y2={e1.y}
              stroke="currentColor"
              strokeWidth={on ? 2.5 : 1}
              opacity={on ? 0.9 : 0.25}
              markerEnd="url(#arrow)"
              className="text-ink"
            />
          );
        })}
        {Object.entries(NODES).map(([name, p]) => {
          const look = stateLook(name);
          const current = task.current === name;
          return (
            <g key={name} transform={`translate(${p.x - 46},${p.y - 13})`} data-state={name} data-current={current || undefined}>
              <rect
                width={92}
                height={26}
                rx={13}
                fill={visited.has(name) ? look.color : "var(--surface-2)"}
                stroke={current ? "var(--text-primary)" : inferred.has(name) ? look.color : "var(--border)"}
                strokeWidth={current ? 3 : inferred.has(name) ? 2 : 1}
                strokeDasharray={inferred.has(name) ? "4 3" : undefined}
              />
              <text x={46} y={17} textAnchor="middle" fontSize="11" fontWeight={current ? 700 : 500} fill={visited.has(name) && name !== "input-required" ? "#fff" : "var(--text-primary)"}>
                {look.glyph} {name}
              </text>
            </g>
          );
        })}
      </svg>
      {inferred.size > 0 ? (
        <p className="text-xs text-ink-2">Dashed: not seen by this client (a blocking call only returns the end state), but the task must have passed through it.</p>
      ) : null}
      <ol className="space-y-1 text-xs">
        {task.history.map((h, i) => {
          const note = h.state.startsWith("note:");
          return (
            <li key={i}>
              <button type="button" className="w-full rounded px-1 py-0.5 text-left hover:bg-surface-0" onClick={() => onEvent(h.eventId)}>
                <span className="mr-2 font-mono tabular-nums text-ink-3">{relative(h.t - start)}</span>
                {note ? <span className="text-ink-2">“{h.note}”</span> : <span className="font-semibold">{h.state}</span>}
              </button>
            </li>
          );
        })}
      </ol>
    </div>
  );
}
