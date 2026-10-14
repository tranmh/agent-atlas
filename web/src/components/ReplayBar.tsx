"use client";

import { SPEEDS, type EngineSnapshot } from "@/replay/engine";
import { clock } from "@/lib/format";

export interface ReplayBarProps {
  snapshot: EngineSnapshot;
  playhead: number;
  canLive: boolean;
  onToggle: () => void;
  onStepBack: () => void;
  onStepForward: () => void;
  onSeek: (t: number) => void;
  onSpeed: (s: number) => void;
  onLive: () => void;
}

const btn = "rounded-md border border-line bg-surface-2 px-2.5 py-1 text-sm hover:bg-surface-0 disabled:opacity-40";

/** Debugger-style transport: play/pause, single step, speed, scrubber, live. */
export function ReplayBar({ snapshot, playhead, canLive, onToggle, onStepBack, onStepForward, onSeek, onSpeed, onLive }: ReplayBarProps) {
  const live = snapshot.mode === "live";
  const playing = snapshot.playing;
  const span = Math.max(1, snapshot.end - snapshot.start);
  const value = Math.round(Math.max(0, Math.min(span, playhead - snapshot.start)));
  return (
    <div className="flex flex-wrap items-center gap-2 border-t border-line bg-surface-1 px-3 py-2" data-testid="replay-bar">
      <button type="button" className={btn} onClick={onStepBack} title="Previous event (←)" aria-label="Previous event">
        ⏮
      </button>
      <button type="button" className={`${btn} min-w-[4.5rem] font-semibold`} onClick={onToggle} title="Play / pause (space)" aria-label={playing && !live ? "Pause" : live ? "Pause live" : "Play"} data-testid="play-toggle">
        {live ? "❚❚ Pause" : playing ? "❚❚ Pause" : "▶ Play"}
      </button>
      <button type="button" className={btn} onClick={onStepForward} title="Next event (→)" aria-label="Next event" data-testid="step-forward">
        ⏭
      </button>
      <label className="flex items-center gap-1 text-xs text-ink-2">
        Speed
        <select className="rounded-md border border-line bg-surface-2 px-1 py-1 text-sm" value={snapshot.speed} onChange={(e) => onSpeed(Number(e.target.value))} disabled={live} aria-label="Replay speed">
          {SPEEDS.map((s) => (
            <option key={s} value={s}>
              {s}×
            </option>
          ))}
        </select>
      </label>
      <input
        type="range"
        className="min-w-[8rem] flex-1 accent-[var(--accent)]"
        min={0}
        max={span}
        step={10}
        value={value}
        onChange={(e) => onSeek(snapshot.start + Number(e.target.value))}
        aria-label="Timeline"
        data-testid="scrubber"
        disabled={snapshot.count === 0}
      />
      <span className="w-[6.5rem] text-right font-mono text-xs tabular-nums text-ink-2" data-testid="playhead-clock">
        {clock(playhead)}
      </span>
      <span className="w-[5.5rem] text-right font-mono text-xs tabular-nums text-ink-3" title="Events at or before the playhead / all events">
        {snapshot.cursor + 1}/{snapshot.count}
      </span>
      <button
        type="button"
        className={`${btn} font-semibold ${live ? "border-critical text-critical" : ""}`}
        onClick={onLive}
        disabled={!canLive}
        title="Follow live events (L)"
        data-testid="live-button"
      >
        ● LIVE
      </button>
    </div>
  );
}
