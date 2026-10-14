import { describe, expect, it } from "vitest";
import type { AtlasEvent } from "@/events/schema";
import { ReplayEngine, upperBound, type Fold } from "./engine";

const ev = (id: number, t: number, extra: Partial<AtlasEvent> = {}): AtlasEvent => ({
  id,
  t,
  ts: new Date(t).toISOString(),
  source: "agent",
  kind: "agent_work",
  session: "s1",
  ...extra,
});

/** A fold that records which ids it applied, to check incremental folding. */
const recordFold: Fold<{ ids: number[] }> = { init: () => ({ ids: [] }), apply: (s, e) => void s.ids.push(e.id) };

describe("upperBound", () => {
  it("finds the first index after a time", () => {
    const es = [ev(1, 10), ev(2, 20), ev(3, 20), ev(4, 30)];
    expect(upperBound(es, 5)).toBe(0);
    expect(upperBound(es, 20)).toBe(3);
    expect(upperBound(es, 99)).toBe(4);
  });
});

describe("ReplayEngine", () => {
  it("keeps events sorted by time and ignores duplicates", () => {
    const e = new ReplayEngine(recordFold);
    e.append([ev(1, 100), ev(3, 300)], 1000);
    e.append([ev(2, 200), ev(1, 100)], 1000);
    expect(e.all().map((x) => x.id)).toEqual([1, 2, 3]);
  });

  it("folds incrementally while playing and refolds when seeking back", () => {
    const e = new ReplayEngine(recordFold);
    e.load([ev(1, 1000), ev(2, 2000), ev(3, 3000)]);
    expect(e.getSnapshot().cursor).toBe(-1);
    e.seek(2000);
    expect(e.state().ids).toEqual([1, 2]);
    e.seek(3000);
    expect(e.state().ids).toEqual([1, 2, 3]);
    e.seek(1500);
    expect(e.state().ids).toEqual([1]);
  });

  it("refolds when a late event lands before the cursor", () => {
    const e = new ReplayEngine(recordFold);
    e.load([ev(1, 1000), ev(3, 3000)]);
    e.seek(3000);
    e.append([ev(2, 2000)]);
    expect(e.state().ids).toEqual([1, 2, 3]);
  });

  it("plays at speed and stops at the end", () => {
    const e = new ReplayEngine(recordFold);
    e.load([ev(1, 1000), ev(2, 2000)]);
    e.play();
    e.tick(10_000);
    e.tick(10_200); // dt 200 ms at speed 1
    expect(e.playhead).toBeCloseTo(999 + 200);
    e.setSpeed(4);
    e.tick(10_400); // +800
    expect(e.getSnapshot().cursor).toBe(0);
    e.tick(10_650); // capped dt 250 * 4 = +1000 → end
    expect(e.playhead).toBe(2000);
    expect(e.playing).toBe(false);
    expect(e.state().ids).toEqual([1, 2]);
  });

  it("steps between visible events only", () => {
    const e = new ReplayEngine(recordFold);
    e.load([ev(1, 1000, { scenario: "sc" }), ev(2, 2000, { scenario: "ex" }), ev(3, 3000, { scenario: "sc" })]);
    const sc = (x: AtlasEvent) => x.scenario === "sc";
    expect(e.stepForward(sc)?.id).toBe(1);
    expect(e.stepForward(sc)?.id).toBe(3);
    expect(e.stepForward(sc)).toBeNull();
    expect(e.stepBack(sc)?.id).toBe(1);
  });

  it("follows real time in live mode, behind by the live delay", () => {
    const e = new ReplayEngine(recordFold);
    e.liveDelayMs = 1000;
    const now = 1_000_000;
    e.append([ev(1, now - 5000)], now); // receipt ts == now - 5000, offset 5000 → but min offset wins
    e.tick(now);
    // offset = now - ts = 5000 (server clock appears 5 s behind)
    expect(e.playhead).toBe(now - 5000 - 1000);
    e.pause();
    expect(e.getSnapshot().mode).toBe("replay");
    e.goLive();
    expect(e.getSnapshot().mode).toBe("live");
  });

  it("returns a time window of events", () => {
    const e = new ReplayEngine(recordFold);
    e.load([ev(1, 1000), ev(2, 2000), ev(3, 3000), ev(4, 4000)]);
    expect(e.window(2000, 3000).map((x) => x.id)).toEqual([2, 3]);
  });

  // Review W1: events sharing a millisecond must each be a step.
  it("steps through events that share a millisecond", () => {
    const e = new ReplayEngine(recordFold);
    e.load([ev(1, 1000), ev(2, 1000), ev(3, 2000)]);
    const fwd: number[] = [];
    for (let x = e.stepForward(); x; x = e.stepForward()) fwd.push(x.id);
    expect(fwd).toEqual([1, 2, 3]);
    const back: number[] = [];
    for (let x = e.stepBack(); x; x = e.stepBack()) back.push(x.id);
    expect(back).toEqual([2, 1]);
  });

  it("folds exactly up to the stepped event, not its same-millisecond siblings", () => {
    const e = new ReplayEngine(recordFold);
    e.load([ev(1, 1000), ev(2, 1000), ev(3, 2000)]);
    e.stepForward();
    expect(e.state().ids).toEqual([1]);
    e.tick(5000); // paused: the step position holds
    expect(e.state().ids).toEqual([1]);
    e.stepForward();
    expect(e.state().ids).toEqual([1, 2]);
  });

  // Review W3: Play after an empty pause must reach events that arrive later.
  it("plays into events that arrive after an empty pause", () => {
    const e = new ReplayEngine(recordFold);
    e.load([], "live");
    const now = 1_700_000_000_000;
    e.tick(now);
    e.pause();
    e.play();
    e.tick(now + 16);
    e.append([ev(1, now + 2000), ev(2, now + 3000)], now + 2100);
    e.play();
    for (let t = now + 2200; t < now + 10_000; t += 16) e.tick(t);
    expect(e.getSnapshot().cursor).toBeGreaterThanOrEqual(0);
  });
});
