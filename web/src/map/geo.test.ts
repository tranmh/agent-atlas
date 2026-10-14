import { describe, expect, it } from "vitest";
import { bezierAt, colocatedOffsets, controlPoint, legPosition } from "./geo";

describe("geo", () => {
  it("spreads co-located nodes on a circle and leaves lone nodes alone", () => {
    const off = colocatedOffsets([
      { id: "b", lat: 47.07, lon: 15.44 },
      { id: "a", lat: 47.07, lon: 15.44 },
      { id: "c", lat: 1.35, lon: 103.8 },
    ]);
    expect(off.get("c")).toEqual({ x: 0, y: 0 });
    const a = off.get("a")!;
    const b = off.get("b")!;
    expect(a).not.toEqual(b);
    expect(Math.hypot(a.x, a.y)).toBeCloseTo(Math.hypot(b.x, b.y), 0);
    expect(a.y).toBeLessThan(0); // first in id order sits on top
  });

  it("evaluates curves and leg positions", () => {
    const a = { x: 0, y: 0 };
    const b = { x: 100, y: 0 };
    const c = controlPoint(a, b);
    expect(c.x).toBe(50);
    expect(bezierAt(a, c, b, 0)).toEqual(a);
    expect(bezierAt(a, c, b, 1)).toEqual(b);
    expect(legPosition(2, 0.75)).toEqual({ leg: 1, t: 0.5 });
    expect(legPosition(2, 1)).toEqual({ leg: 1, t: 1 });
  });
});
