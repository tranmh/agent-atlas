/** Geometry for pins and flights (pure functions, screen-space pixels). */

export interface Pt {
  x: number;
  y: number;
}

export interface NodeLike {
  id: string;
  lat: number;
  lon: number;
}

/** Control point of a curved leg from a to b (bends to the left of a→b). */
export function controlPoint(a: Pt, b: Pt, bend = 0.22): Pt {
  const mx = (a.x + b.x) / 2;
  const my = (a.y + b.y) / 2;
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  // Left normal of a→b scaled by the length.
  return { x: mx - dy * bend, y: my + dx * bend };
}

/** Point at t (0..1) on the quadratic Bézier a → c → b. */
export function bezierAt(a: Pt, c: Pt, b: Pt, t: number): Pt {
  const u = 1 - t;
  return { x: u * u * a.x + 2 * u * t * c.x + t * t * b.x, y: u * u * a.y + 2 * u * t * c.y + t * t * b.y };
}

/**
 * Where a multi-leg flight is: the leg index and the position within it.
 * progress 0..1 covers all legs evenly.
 */
export function legPosition(legs: number, progress: number): { leg: number; t: number } {
  const p = Math.max(0, Math.min(1, progress)) * legs;
  const leg = Math.min(legs - 1, Math.floor(p));
  return { leg, t: p - leg };
}

/** Distance from p to the segment ab (for hit testing). */
export function distToSegment(p: Pt, a: Pt, b: Pt): number {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const len2 = dx * dx + dy * dy;
  const t = len2 === 0 ? 0 : Math.max(0, Math.min(1, ((p.x - a.x) * dx + (p.y - a.y) * dy) / len2));
  const x = a.x + t * dx;
  const y = a.y + t * dy;
  return Math.hypot(p.x - x, p.y - y);
}

export interface ScreenNode {
  id: string;
  x: number;
  y: number;
}

/**
 * Pixel offsets that keep pins apart at the current zoom with as little
 * displacement as possible: overlapping pins push each other apart, and every
 * pin is pulled back towards its true position. Deterministic (fixed order,
 * fixed tie-break angles), so the same input gives the same layout.
 */
export function declutterOffsets(nodes: readonly ScreenNode[], minDist = 26, iterations = 150): Map<string, Pt> {
  const sorted = [...nodes].sort((a, b) => a.id.localeCompare(b.id));
  const pos = sorted.map((n) => ({ x: n.x, y: n.y }));
  for (let it = 0; it < iterations; it++) {
    for (let i = 0; i < pos.length; i++) {
      for (let j = i + 1; j < pos.length; j++) {
        let dx = pos[j].x - pos[i].x;
        let dy = pos[j].y - pos[i].y;
        let d = Math.hypot(dx, dy);
        if (d >= minDist) continue;
        if (d < 1e-6) {
          // Same spot: separate along a fixed angle per pair.
          const a = ((i * 7 + j * 13) % 360) * (Math.PI / 180);
          dx = Math.cos(a);
          dy = Math.sin(a);
          d = 1;
        }
        const push = (minDist - d) / 2 / d;
        pos[i].x -= dx * push;
        pos[i].y -= dy * push;
        pos[j].x += dx * push;
        pos[j].y += dy * push;
      }
    }
    // Pull back towards the true position; the last iterations only separate.
    if (it >= (iterations * 2) / 3) continue;
    sorted.forEach((n, k) => {
      pos[k].x += (n.x - pos[k].x) * 0.04;
      pos[k].y += (n.y - pos[k].y) * 0.04;
    });
  }
  const out = new Map<string, Pt>();
  sorted.forEach((n, k) => out.set(n.id, { x: Math.round(pos[k].x - n.x), y: Math.round(pos[k].y - n.y) }));
  return out;
}
