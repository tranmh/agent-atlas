/**
 * Replay engine: the single clock of the UI. It keeps every event sorted by
 * time and moves a playhead over them.
 *
 *  - live:   the playhead follows real time with a small delay, so events that
 *            arrive slightly out of order (batched by producers) still play in order.
 *  - replay: the playhead moves at `speed` while playing; pause, step, seek.
 *
 * A fold (see reduce.ts) is applied incrementally up to the cursor (the last
 * event at or before the playhead). Framework-free; React subscribes through
 * subscribe()/getSnapshot().
 */
import type { AtlasEvent } from "@/events/schema";

export interface Fold<S> {
  init(): S;
  apply(state: S, e: AtlasEvent): void;
}

export type EngineMode = "live" | "replay";

export interface EngineSnapshot {
  version: number;
  mode: EngineMode;
  playing: boolean;
  speed: number;
  /** Index of the last event at or before the playhead (-1 = none). */
  cursor: number;
  count: number;
  start: number;
  end: number;
}

export const SPEEDS = [0.1, 0.25, 0.5, 1, 2, 4, 8] as const;

/** Index of the first event with t > time (binary search). */
export function upperBound(events: readonly AtlasEvent[], time: number): number {
  let lo = 0;
  let hi = events.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (events[mid].t <= time) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

function before(a: AtlasEvent, b: AtlasEvent): boolean {
  return a.t < b.t || (a.t === b.t && a.id < b.id);
}

export class ReplayEngine<S> {
  private events: AtlasEvent[] = [];
  private ids = new Set<string>();
  private listeners = new Set<() => void>();
  private snap: EngineSnapshot;
  private foldState: S;
  private folded = -1; // index of the last folded event
  private dirtyFrom = Infinity; // smallest index changed since the last fold
  private lastTick = 0;
  private clockOffset: number | null = null;
  /**
   * Exact cursor after a step. The playhead is a millisecond timestamp and
   * several events can share one, so a step remembers the event index too.
   * Cleared by anything that moves the clock (play, seek, live).
   */
  private pinned: number | null = null;

  mode: EngineMode = "live";
  playing = true;
  speed = 1;
  playhead = 0;
  /** Live mode plays this far behind real time (ms). */
  liveDelayMs = 1500;

  constructor(private readonly fold: Fold<S>) {
    this.foldState = fold.init();
    this.snap = this.makeSnapshot(0);
  }

  // ---- data ----

  /** Add events (any order; duplicates by session+id are ignored). */
  append(batch: readonly AtlasEvent[], now = Date.now()): void {
    let changed = false;
    for (const e of batch) {
      const key = `${e.session ?? ""}/${e.id}`;
      if (this.ids.has(key)) continue;
      this.ids.add(key);
      // Estimate browser-minus-server clock offset from receipt timestamps.
      const recv = Date.parse(e.ts);
      if (Number.isFinite(recv)) {
        const off = now - recv;
        this.clockOffset = this.clockOffset === null ? off : Math.min(this.clockOffset, off);
      }
      let i = this.events.length;
      while (i > 0 && before(e, this.events[i - 1])) i--;
      this.events.splice(i, 0, e);
      if (i <= this.folded) this.dirtyFrom = Math.min(this.dirtyFrom, i);
      if (this.pinned !== null && i <= this.pinned) this.pinned++;
      changed = true;
    }
    if (changed) this.update(true);
  }

  /** Replace all events (load a recorded session) and switch mode. */
  load(events: readonly AtlasEvent[], mode: EngineMode = "replay"): void {
    this.events = [...events].sort((a, b) => (before(a, b) ? -1 : before(b, a) ? 1 : 0));
    this.ids = new Set(this.events.map((e) => `${e.session ?? ""}/${e.id}`));
    this.pinned = null;
    this.resetFold();
    this.mode = mode;
    this.playing = mode === "live";
    this.playhead = mode === "replay" ? (this.events[0]?.t ?? 0) - 1 : this.playhead;
    this.update(true);
  }

  clear(): void {
    this.load([], this.mode);
  }

  all(): readonly AtlasEvent[] {
    return this.events;
  }

  at(i: number): AtlasEvent | undefined {
    return this.events[i];
  }

  /** Events with from <= t <= to. */
  window(from: number, to: number): AtlasEvent[] {
    let lo = upperBound(this.events, from - 1e-9);
    while (lo > 0 && this.events[lo - 1].t >= from) lo--;
    const hi = upperBound(this.events, to);
    return this.events.slice(lo, hi);
  }

  /** The folded state at the cursor. Mutated in place; use snapshot().version to detect changes. */
  state(): S {
    return this.foldState;
  }

  // ---- clock ----

  /** Advance the playhead. Call once per animation frame. */
  tick(now = Date.now()): void {
    const dt = this.lastTick ? Math.min(now - this.lastTick, 250) : 0;
    this.lastTick = now;
    if (this.mode === "live") {
      this.pinned = null;
      this.playhead = now - (this.clockOffset ?? 0) - this.liveDelayMs;
    } else if (this.playing && this.events.length > 0) {
      this.playhead += dt * this.speed;
      const end = this.end();
      if (this.playhead >= end) {
        this.playhead = end;
        this.playing = false;
        this.update(true);
        return;
      }
    }
    this.update(false);
  }

  play(): void {
    if (this.mode === "live") return;
    // Outside the recorded range (at the end, or before the first event, e.g.
    // after pausing an empty view): start from the beginning.
    if (this.playhead >= this.end() || this.playhead < this.start() - 1) this.playhead = this.start() - 1;
    this.pinned = null;
    this.playing = true;
    this.update(true);
  }

  pause(): void {
    this.mode = "replay";
    this.playing = false;
    this.update(true);
  }

  togglePlay(): void {
    if (this.mode === "live" || this.playing) this.pause();
    else this.play();
  }

  goLive(): void {
    this.pinned = null;
    this.mode = "live";
    this.playing = true;
    this.update(true);
  }

  setSpeed(s: number): void {
    this.speed = s;
    this.update(true);
  }

  seek(t: number): void {
    this.mode = "replay";
    this.pinned = null;
    this.playhead = Math.max(this.start() - 1, Math.min(t, this.end()));
    this.update(true);
  }

  /** Jump to the next event (matching `visible`), paused. */
  stepForward(visible: (e: AtlasEvent) => boolean = () => true): AtlasEvent | null {
    this.mode = "replay";
    this.playing = false;
    for (let i = this.cursorIndex() + 1; i < this.events.length; i++) {
      if (visible(this.events[i])) {
        this.playhead = this.events[i].t;
        this.pinned = i;
        this.update(true);
        return this.events[i];
      }
    }
    this.update(true);
    return null;
  }

  /** Jump back to the previous event (matching `visible`), paused. */
  stepBack(visible: (e: AtlasEvent) => boolean = () => true): AtlasEvent | null {
    this.mode = "replay";
    this.playing = false;
    for (let i = this.cursorIndex() - 1; i >= 0; i--) {
      if (visible(this.events[i])) {
        this.playhead = this.events[i].t;
        this.pinned = i;
        this.update(true);
        return this.events[i];
      }
    }
    this.pinned = null;
    this.playhead = this.start() - 1;
    this.update(true);
    return null;
  }

  start(): number {
    return this.events[0]?.t ?? 0;
  }

  end(): number {
    return this.events[this.events.length - 1]?.t ?? 0;
  }

  // ---- subscription ----

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  getSnapshot = (): EngineSnapshot => this.snap;

  private resetFold(): void {
    this.foldState = this.fold.init();
    this.folded = -1;
    this.dirtyFrom = Infinity;
  }

  /** Last event at or before the playhead, or the pinned step position. */
  private cursorIndex(): number {
    const ub = upperBound(this.events, this.playhead) - 1;
    return this.pinned !== null ? Math.min(this.pinned, ub) : ub;
  }

  private update(force: boolean): void {
    const cursor = this.cursorIndex();
    if (this.dirtyFrom !== Infinity || cursor < this.folded) this.resetFold();
    for (let i = this.folded + 1; i <= cursor; i++) this.fold.apply(this.foldState, this.events[i]);
    const moved = cursor !== this.folded || cursor !== this.snap.cursor;
    this.folded = cursor;
    if (!force && !moved && this.events.length === this.snap.count) return;
    this.snap = this.makeSnapshot(this.snap ? this.snap.version + 1 : 0);
    for (const fn of this.listeners) fn();
  }

  private makeSnapshot(version: number): EngineSnapshot {
    return {
      version,
      mode: this.mode,
      playing: this.mode === "live" ? true : this.playing,
      speed: this.speed,
      cursor: this.folded,
      count: this.events.length,
      start: this.start(),
      end: this.end(),
    };
  }
}
