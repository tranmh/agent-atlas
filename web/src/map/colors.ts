/**
 * Colours. Scenario hues come from the scenario files (validated categorical
 * slots, see docs/plans/PROGRESS.md). Task states use the reserved status
 * palette and always come with a glyph and a label, never colour alone.
 */
import type { AgentStatus } from "@/replay/reduce";

export const STATUS = {
  good: "#0ca30c",
  warning: "#fab219",
  serious: "#ec835a",
  critical: "#d03b3b",
} as const;

export const INK = "#0b0b0b";
export const INK_SECONDARY = "#52514e";
export const SURFACE = "#fcfcfb";

export interface StatusLook {
  label: string;
  glyph: string;
  color: string;
}

export const STATUS_LOOK: Record<AgentStatus, StatusLook> = {
  idle: { label: "idle", glyph: "", color: "#9a9893" },
  submitted: { label: "submitted", glyph: "•", color: INK_SECONDARY },
  working: { label: "working", glyph: "⋯", color: INK },
  "input-required": { label: "needs input", glyph: "?", color: STATUS.warning },
  completed: { label: "completed", glyph: "✓", color: STATUS.good },
  failed: { label: "failed", glyph: "✕", color: STATUS.critical },
  canceled: { label: "canceled", glyph: "–", color: INK_SECONDARY },
  denied: { label: "denied by gateway", glyph: "⛔", color: STATUS.critical },
  waiting: { label: "at breakpoint", glyph: "❚❚", color: STATUS.serious },
};

/** Look for a short task state string (from events). */
export function stateLook(state: string | undefined): StatusLook {
  return STATUS_LOOK[(state ?? "idle") as AgentStatus] ?? { label: state ?? "", glyph: "", color: INK_SECONDARY };
}

/** #rrggbb + alpha → rgba(). */
export function withAlpha(hex: string, alpha: number): string {
  const n = parseInt(hex.slice(1), 16);
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${Math.max(0, Math.min(1, alpha))})`;
}
