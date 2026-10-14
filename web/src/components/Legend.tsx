"use client";

import { STATUS_LOOK } from "@/map/colors";

const LINES: { label: string; dash: string; width: number; color?: string; dot?: "square" | "x" | "diamond" }[] = [
  { label: "SendMessage / GetTask", dash: "", width: 2.25 },
  { label: "SendStreamingMessage", dash: "7 4", width: 2.25 },
  { label: "SSE frame", dash: "2 4", width: 1.5 },
  { label: "agent card", dash: "1 3", width: 1.5, dot: "square" },
  { label: "push (bypasses gateway)", dash: "1 6", width: 3 },
  { label: "LLM call", dash: "5 3", width: 2.25 },
  { label: "MCP tool call (agent → tool)", dash: "9 3 2 3", width: 2.25, dot: "diamond" },
  { label: "403 denied / 503 retry", dash: "", width: 2.25, color: "#d03b3b", dot: "x" },
];

export function Legend() {
  return (
    <details className="pointer-events-auto w-[17rem] rounded-md border border-line bg-surface-1 p-2 text-[11px] shadow" open>
      <summary className="cursor-pointer select-none font-semibold">Legend</summary>
      <ul className="mt-1 grid grid-cols-1 gap-y-0.5">
        {LINES.map((l) => (
          <li key={l.label} className="flex items-center gap-2">
            <svg width="34" height="10" aria-hidden>
              <line x1="1" y1="5" x2="26" y2="5" stroke={l.color ?? "currentColor"} strokeWidth={l.width} strokeDasharray={l.dash} strokeLinecap="round" />
              {l.dot === "square" ? (
                <rect x="25" y="1" width="8" height="8" fill="currentColor" />
              ) : l.dot === "x" ? (
                <circle cx="29" cy="5" r="4.5" fill="#d03b3b" />
              ) : l.dot === "diamond" ? (
                <path d="M29 0 L34 5 L29 10 L24 5 Z" fill="currentColor" />
              ) : (
                <circle cx="29" cy="5" r="4" fill="currentColor" />
              )}
            </svg>
            {l.label}
          </li>
        ))}
      </ul>
      <ul className="mt-1 grid grid-cols-2 gap-x-2 gap-y-0.5">
        {(["working", "input-required", "completed", "failed", "denied", "waiting"] as const).map((s) => (
          <li key={s} className="flex items-center gap-1">
            <span className="inline-flex h-3.5 min-w-[14px] items-center justify-center rounded-full px-0.5 text-[9px] font-bold text-white" style={{ background: STATUS_LOOK[s].color, color: s === "input-required" || s === "waiting" ? "#0b0b0b" : "#fff" }}>
              {STATUS_LOOK[s].glyph}
            </span>
            {STATUS_LOOK[s].label}
          </li>
        ))}
      </ul>
      <p className="mt-1 text-ink-3">Square pin = orchestrator, round pin = agent, hexagon = MCP server. Letters = scenario.</p>
      <p className="mt-0.5 text-ink-3">A2A = agent ↔ agent (tasks). MCP = agent → tools &amp; data.</p>
    </details>
  );
}
