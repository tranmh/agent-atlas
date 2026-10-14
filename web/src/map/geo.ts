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

/**
 * Pixel offsets for nodes that share a location (several agents in Graz):
 * they sit on a small circle around the city, in a stable order by id.
 * Nodes alone at their location get {0, 0}.
 */
export function colocatedOffsets(nodes: readonly NodeLike[], precision = 0.05): Map<string, Pt> {
  const groups = new Map<string, NodeLike[]>();
  for (const n of nodes) {
    const key = `${Math.round(n.lat / precision)}:${Math.round(n.lon / precision)}`;
    const g = groups.get(key) ?? [];
    g.push(n);
    groups.set(key, g);
  }
  const out = new Map<string, Pt>();
  for (const g of groups.values()) {
    g.sort((a, b) => a.id.localeCompare(b.id));
    if (g.length === 1) {
      out.set(g[0].id, { x: 0, y: 0 });
      continue;
    }
    const r = 12 + 4 * g.length;
    g.forEach((n, i) => {
      const a = -Math.PI / 2 + (2 * Math.PI * i) / g.length;
      out.set(n.id, { x: Math.round(r * Math.cos(a)), y: Math.round(r * Math.sin(a)) });
    });
  }
  return out;
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
