import { describe, expect, it } from "vitest";
import { bezierAt, controlPoint, declutterOffsets, legPosition } from "./geo";

describe("geo", () => {
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

describe("declutterOffsets", () => {
  it("spreads nearby pins so no two are closer than minDist, and leaves far ones alone", () => {
    const nodes = [
      { id: "a", x: 100, y: 100 },
      { id: "b", x: 108, y: 104 },
      { id: "c", x: 112, y: 96 },
      { id: "far", x: 400, y: 400 },
    ];
    const off = declutterOffsets(nodes, 26);
    expect(off.get("far")).toEqual({ x: 0, y: 0 });
    const placed = nodes.slice(0, 3).map((n) => ({ x: n.x + off.get(n.id)!.x, y: n.y + off.get(n.id)!.y }));
    for (let i = 0; i < placed.length; i++)
      for (let j = i + 1; j < placed.length; j++) expect(Math.hypot(placed[i].x - placed[j].x, placed[i].y - placed[j].y)).toBeGreaterThanOrEqual(22);
  });

  it("moves a crowd only locally", () => {
    // 20 pins in a 30 px blob plus one pin 120 px away that does not overlap.
    const blob = Array.from({ length: 20 }, (_, i) => ({ id: `n${i}`, x: 100 + (i % 5) * 6, y: 100 + Math.floor(i / 5) * 6 }));
    const off = declutterOffsets([...blob, { id: "far", x: 260, y: 100 }]);
    for (const n of blob) expect(Math.hypot(off.get(n.id)!.x, off.get(n.id)!.y)).toBeLessThan(90);
    expect(Math.hypot(off.get("far")!.x, off.get("far")!.y)).toBeLessThan(20);
  });

  it("is stable for identical input", () => {
    const nodes = [
      { id: "x", x: 0, y: 0 },
      { id: "y", x: 0, y: 0 },
    ];
    expect(declutterOffsets(nodes)).toEqual(declutterOffsets([...nodes].reverse()));
  });
});
