/**
 * Framework-free Leaflet controller for the atlas:
 *
 *   const m = new AtlasMap().mount(el);
 *   m.setScenarios(scenarios);          // pins for orchestrators and agents
 *   m.setVisibility(code => true);      // solo / mute scenarios
 *   m.setView(view, playhead);          // pin status from the folded view state
 *   m.draw(frame);                      // every animation frame: flights, pulses, hub
 *   m.on("agent", id => ...); m.on("flight", event => ...); m.on("hub", () => ...);
 *
 * The gateway is not a place, so it is drawn as a hub floating at the top of
 * the map: every A2A and LLM call visibly goes up into it and back down.
 * Push notifications bypass it (agent → orchestrator directly).
 */
import L from "./leaflet-setup";
import type { AtlasEvent, Scenario } from "@/events/schema";
import type { Flight } from "@/replay/flights";
import { HUB, LLM } from "@/replay/flights";
import type { AgentView, ViewState } from "@/replay/reduce";
import { STATUS, STATUS_LOOK, SURFACE, withAlpha } from "./colors";
import { bezierAt, colocatedOffsets, controlPoint, distToSegment, legPosition, type Pt } from "./geo";
import { escapeHtml } from "./html";

export interface AtlasMapOptions {
  /** URL of the bundled world outline (GeoJSON). */
  worldUrl?: string;
  /** Start with OpenStreetMap tiles instead of the offline outline. */
  osm?: boolean;
  mapOptions?: L.MapOptions;
  fetch?: typeof fetch;
}

export interface Frame {
  playhead: number;
  flights: Flight[];
  view: ViewState;
}

interface Node {
  id: string;
  scenario: string;
  color: string;
  role: "orchestrator" | "agent";
  name: string;
  city: string;
  lat: number;
  lon: number;
  offset: Pt;
  marker: L.Marker;
  lastClass?: string;
}

type Events = {
  agent: (id: string) => void;
  flight: (e: AtlasEvent) => void;
  hub: () => void;
  llm: () => void;
};

export const OSM_TILE_URL = "https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png";
// Keep fitted places clear of the overlays: hub (top), narration (bottom), legend (right).
const FIT_PADDING: L.FitBoundsOptions = { paddingTopLeft: [40, 100], paddingBottomRight: [60, 200] };
const HUB_W = 150;
const HUB_H = 34;
const LLM_W = 120;

export class AtlasMap {
  private map: L.Map | null = null;
  private canvas: HTMLCanvasElement | null = null;
  private ctx: CanvasRenderingContext2D | null = null;
  private nodes = new Map<string, Node>();
  private colors = new Map<string, string>();
  private visible: (code: string) => boolean = () => true;
  private osm: L.TileLayer | null = null;
  private world: L.GeoJSON | null = null;
  private lastFlights: Flight[] = [];
  /** The last view state, re-applied to pins created later (setScenarios). */
  private lastView: ViewState | null = null;
  private resize: ResizeObserver | null = null;
  private zooming = false;
  private listeners: { [K in keyof Events]: Set<Events[K]> } = { agent: new Set(), flight: new Set(), hub: new Set(), llm: new Set() };

  mount(el: HTMLElement, opts: AtlasMapOptions = {}): this {
    if (this.map) throw new Error("AtlasMap already mounted");
    const map = L.map(el, {
      center: [25, 20],
      zoom: 2,
      minZoom: 2,
      maxZoom: 10,
      worldCopyJump: true,
      zoomSnap: 0.25,
      attributionControl: true,
      ...opts.mapOptions,
    });
    map.attributionControl.setPrefix(false);
    this.map = map;
    el.classList.add("atlas-map");

    const flows = map.createPane("flows");
    flows.style.zIndex = "450";
    flows.style.pointerEvents = "none";
    const canvas = L.DomUtil.create("canvas", "atlas-flows", flows) as HTMLCanvasElement;
    this.canvas = canvas;
    this.ctx = canvas.getContext?.("2d") ?? null;

    this.loadWorld(opts.worldUrl ?? "/world-110m.geojson", opts.fetch);
    this.osm = L.tileLayer(OSM_TILE_URL, {
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
      maxZoom: 18,
    });
    if (opts.osm) this.osm.addTo(map);

    map.on("click", (e: L.LeafletMouseEvent) => this.onClick(e.containerPoint));
    map.on("zoomstart", () => {
      this.zooming = true;
      canvas.style.visibility = "hidden";
    });
    map.on("zoomend", () => {
      this.zooming = false;
      canvas.style.visibility = "";
      el.classList.toggle("atlas-map--labels", map.getZoom() >= 5.5);
    });
    if (typeof ResizeObserver !== "undefined") {
      this.resize = new ResizeObserver(() => map.invalidateSize());
      this.resize.observe(el);
    }
    return this;
  }

  private async loadWorld(url: string, fetchImpl?: typeof fetch): Promise<void> {
    try {
      const res = await (fetchImpl ?? fetch)(url);
      if (!res.ok || !this.map) return;
      const data = (await res.json()) as GeoJSON.FeatureCollection;
      this.world = L.geoJSON(data, {
        interactive: false,
        style: { color: "#c9c6bd", weight: 0.8, fillColor: "#ecebe6", fillOpacity: 1 },
      });
      this.world.addTo(this.map);
      this.world.bringToBack();
    } catch {
      // Offline outline unavailable: the map still works (blank ocean, or OSM tiles).
    }
  }

  /** Switch between the offline outline and OpenStreetMap tiles. */
  setOsm(on: boolean): void {
    if (!this.map || !this.osm) return;
    if (on) this.osm.addTo(this.map);
    else this.osm.remove();
  }

  on<K extends keyof Events>(name: K, fn: Events[K]): () => void {
    this.listeners[name].add(fn);
    return () => this.listeners[name].delete(fn);
  }

  private emit<K extends keyof Events>(name: K, ...args: Parameters<Events[K]>): void {
    for (const fn of this.listeners[name]) (fn as (...a: Parameters<Events[K]>) => void)(...args);
  }

  setScenarios(scenarios: readonly Scenario[]): void {
    const map = this.requireMap();
    for (const n of this.nodes.values()) n.marker.remove();
    this.nodes.clear();
    this.colors.clear();
    const all: { id: string; lat: number; lon: number }[] = [];
    for (const s of scenarios) {
      this.colors.set(s.code, s.color);
      all.push(s.orchestrator, ...s.agents);
    }
    const offsets = colocatedOffsets(all);
    for (const s of scenarios) {
      const places = [
        { p: s.orchestrator, role: "orchestrator" as const },
        ...s.agents.map((a) => ({ p: a, role: "agent" as const })),
      ];
      for (const { p, role } of places) {
        const offset = offsets.get(p.id) ?? { x: 0, y: 0 };
        const marker = L.marker([p.lat, p.lon], {
          icon: this.icon(s.code, s.color, role, p.name, "idle", offset),
          keyboard: true,
          title: `${p.name} (${p.city}) · ${s.title}`,
          riseOnHover: true,
        });
        marker.on("click", () => this.emit("agent", p.id));
        marker.addTo(map);
        this.nodes.set(p.id, { id: p.id, scenario: s.code, color: s.color, role, name: p.name, city: p.city, lat: p.lat, lon: p.lon, offset, marker });
      }
    }
    if (this.lastView) this.setView(this.lastView);
  }

  private icon(code: string, color: string, role: string, name: string, status: string, offset: Pt): L.DivIcon {
    const look = STATUS_LOOK[status as AgentView["status"]] ?? STATUS_LOOK.idle;
    const size = role === "orchestrator" ? 30 : 24;
    // Labels of co-located pins point away from the group centre.
    const dir = offset.x === 0 && offset.y === 0 ? "down" : Math.abs(offset.x) > Math.abs(offset.y) ? (offset.x > 0 ? "right" : "left") : offset.y > 0 ? "down" : "up";
    return L.divIcon({
      className: "",
      iconSize: [size, size],
      iconAnchor: [size / 2 - offset.x, size / 2 - offset.y],
      html:
        `<div class="atlas-pin atlas-pin--${role} atlas-pin--${status}" style="--c:${color};--s:${look.color}" data-agent="${escapeHtml(name)}" data-label="${dir}">` +
        `<span class="atlas-pin__code">${escapeHtml(code.toUpperCase())}</span>` +
        (look.glyph ? `<span class="atlas-pin__badge" aria-label="${escapeHtml(look.label)}">${escapeHtml(look.glyph)}</span>` : "") +
        `<span class="atlas-pin__label">${escapeHtml(name)}</span>` +
        `</div>`,
    });
  }

  setVisibility(visible: (code: string) => boolean): void {
    this.visible = visible;
    for (const n of this.nodes.values()) {
      n.marker.setOpacity(visible(n.scenario) ? 1 : 0.22);
      n.marker.setZIndexOffset(visible(n.scenario) ? 0 : -1000);
    }
  }

  /** Update pin icons from the view state (only pins whose status changed). */
  setView(view: ViewState): void {
    this.lastView = view;
    for (const n of this.nodes.values()) {
      const a = view.agents.get(n.id);
      const status = a?.status ?? "idle";
      if (n.lastClass === status) continue;
      n.lastClass = status;
      n.marker.setIcon(this.icon(n.scenario, n.color, n.role, n.name, status, n.offset));
      const look = STATUS_LOOK[status];
      n.marker.options.title = `${n.name} (${n.city}): ${look.label}`;
    }
  }

  /** Zoom to a scenario's places, or to everything. */
  fit(code: string | null): void {
    const map = this.requireMap();
    const pts = [...this.nodes.values()].filter((n) => code === null || n.scenario === code).map((n) => L.latLng(n.lat, n.lon));
    if (pts.length === 0) return;
    map.fitBounds(L.latLngBounds(pts), { ...FIT_PADDING, maxZoom: 5 });
  }

  /** Zoom to a named region. */
  region(name: "world" | "europe" | "central"): void {
    const map = this.requireMap();
    if (name === "europe") map.fitBounds(L.latLngBounds([46.5, 2], [62.8, 26]), FIT_PADDING);
    else if (name === "central") map.fitBounds(L.latLngBounds([46.6, 8.4], [48.9, 16.6]), FIT_PADDING);
    else this.fit(null);
  }

  focus(id: string): void {
    const n = this.nodes.get(id);
    if (n) this.requireMap().setView([n.lat, n.lon], Math.max(this.requireMap().getZoom(), 5));
  }

  // ---- drawing ----

  private hubPoint(): Pt {
    const size = this.requireMap().getSize();
    return { x: size.x / 2, y: 30 + HUB_H / 2 };
  }

  private llmPoint(): Pt {
    const h = this.hubPoint();
    return { x: h.x + HUB_W / 2 + 24 + LLM_W / 2, y: h.y };
  }

  /** Screen position of a node id (agent, orchestrator, hub, llm). */
  pointOf(id: string): Pt | null {
    if (id === HUB) return this.hubPoint();
    if (id === LLM) return this.llmPoint();
    const n = this.nodes.get(id);
    if (!n || !this.map) return null;
    const p = this.map.latLngToContainerPoint([n.lat, n.lon]);
    return { x: p.x + n.offset.x, y: p.y + n.offset.y };
  }

  draw(frame: Frame): void {
    const map = this.map;
    const canvas = this.canvas;
    const ctx = this.ctx;
    if (!map || !canvas || !ctx || this.zooming) return;
    const size = map.getSize();
    const dpr = typeof window !== "undefined" ? window.devicePixelRatio || 1 : 1;
    if (canvas.width !== Math.round(size.x * dpr) || canvas.height !== Math.round(size.y * dpr)) {
      canvas.width = Math.round(size.x * dpr);
      canvas.height = Math.round(size.y * dpr);
      canvas.style.width = `${size.x}px`;
      canvas.style.height = `${size.y}px`;
    }
    L.DomUtil.setPosition(canvas, map.containerPointToLayerPoint([0, 0]));
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, size.x, size.y);

    this.drawTeams(ctx);
    this.drawPulses(ctx, frame);
    this.drawHub(ctx, frame);
    this.lastFlights = frame.flights;
    const labelBudget = frame.flights.length <= 14;
    for (const f of frame.flights) this.drawFlight(ctx, f, labelBudget);
  }

  private drawTeams(ctx: CanvasRenderingContext2D): void {
    const byScenario = new Map<string, Node[]>();
    for (const n of this.nodes.values()) {
      if (!this.visible(n.scenario)) continue;
      const g = byScenario.get(n.scenario) ?? [];
      g.push(n);
      byScenario.set(n.scenario, g);
    }
    ctx.save();
    ctx.lineWidth = 1;
    ctx.setLineDash([2, 5]);
    for (const [code, g] of byScenario) {
      const orch = g.find((n) => n.role === "orchestrator");
      if (!orch) continue;
      const a = this.pointOf(orch.id);
      if (!a) continue;
      ctx.strokeStyle = withAlpha(this.colors.get(code) ?? "#888888", 0.35);
      for (const n of g) {
        if (n === orch) continue;
        const b = this.pointOf(n.id);
        if (!b) continue;
        const c = controlPoint(a, b, 0.12);
        ctx.beginPath();
        ctx.moveTo(a.x, a.y);
        ctx.quadraticCurveTo(c.x, c.y, b.x, b.y);
        ctx.stroke();
      }
    }
    ctx.restore();
  }

  private drawPulses(ctx: CanvasRenderingContext2D, frame: Frame): void {
    for (const n of this.nodes.values()) {
      if (!this.visible(n.scenario)) continue;
      const a = frame.view.agents.get(n.id);
      if (!a) continue;
      const p = this.pointOf(n.id);
      if (!p) continue;
      if (a.activeT !== undefined) {
        const dt = frame.playhead - a.activeT;
        if (dt >= 0 && dt < 900) {
          const k = dt / 900;
          ctx.beginPath();
          ctx.arc(p.x, p.y, 12 + 16 * k, 0, Math.PI * 2);
          ctx.strokeStyle = withAlpha(n.color, 0.7 * (1 - k));
          ctx.lineWidth = 3;
          ctx.stroke();
        }
      }
      if (a.faultT !== undefined) {
        const dt = frame.playhead - a.faultT;
        if (dt >= 0 && dt < 2000) {
          ctx.beginPath();
          ctx.arc(p.x, p.y, 17, 0, Math.PI * 2);
          ctx.strokeStyle = withAlpha(STATUS.critical, 1 - dt / 2000);
          ctx.lineWidth = 3;
          ctx.stroke();
        }
      }
    }
  }

  private drawHub(ctx: CanvasRenderingContext2D, frame: Frame): void {
    const h = this.hubPoint();
    const l = this.llmPoint();
    const flash = (t: number | undefined) => (t === undefined ? 0 : Math.max(0, 1 - (frame.playhead - t) / 500));
    this.badge(ctx, h, HUB_W, "agentgateway", "A2A :3000 · LLM :3100", flash(frame.view.gatewayT));
    this.badge(ctx, l, LLM_W, "Ollama", "via gateway LLM route", flash(frame.view.llmT));
    // Dotted link hub ↔ llm.
    ctx.save();
    ctx.setLineDash([2, 3]);
    ctx.strokeStyle = "#9a9893";
    ctx.beginPath();
    ctx.moveTo(h.x + HUB_W / 2, h.y);
    ctx.lineTo(l.x - LLM_W / 2, l.y);
    ctx.stroke();
    ctx.restore();
  }

  private badge(ctx: CanvasRenderingContext2D, c: Pt, w: number, title: string, sub: string, flash: number): void {
    const x = c.x - w / 2;
    const y = c.y - HUB_H / 2;
    ctx.save();
    ctx.shadowColor = "rgba(0,0,0,0.18)";
    ctx.shadowBlur = 6;
    ctx.fillStyle = SURFACE;
    ctx.strokeStyle = flash > 0 ? withAlpha("#0b0b0b", 0.4 + 0.6 * flash) : "#9a9893";
    ctx.lineWidth = 1 + 2 * flash;
    ctx.beginPath();
    ctx.roundRect(x, y, w, HUB_H, 8);
    ctx.fill();
    ctx.shadowBlur = 0;
    ctx.stroke();
    ctx.fillStyle = "#0b0b0b";
    ctx.font = "600 12px ui-sans-serif, system-ui, sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillText(title, c.x, c.y - 6);
    ctx.fillStyle = "#52514e";
    ctx.font = "10px ui-sans-serif, system-ui, sans-serif";
    ctx.fillText(sub, c.x, c.y + 8);
    ctx.restore();
  }

  private legPoints(f: Flight): { a: Pt; c: Pt; b: Pt }[] | null {
    const out: { a: Pt; c: Pt; b: Pt }[] = [];
    for (const [from, to] of f.legs) {
      const a = this.pointOf(from);
      const b = this.pointOf(to);
      if (!a || !b) return null;
      out.push({ a, b, c: controlPoint(a, b, f.style === "push" ? 0.3 : 0.18) });
    }
    return out;
  }

  private drawFlight(ctx: CanvasRenderingContext2D, f: Flight, labels: boolean): void {
    const legs = this.legPoints(f);
    if (!legs) return;
    const color = f.style === "denied" || f.style === "fault" ? STATUS.critical : (this.colors.get(f.scenario) ?? "#555555");
    const { leg, t } = legPosition(legs.length, f.progress);
    ctx.save();
    ctx.globalAlpha = f.alpha;
    ctx.lineCap = "round";
    const dash: Record<Flight["style"], number[]> = {
      send: [],
      stream: [7, 4],
      frame: [2, 4],
      card: [1, 3],
      push: [1, 6],
      denied: [],
      fault: [3, 3],
      llm: [5, 3],
    };
    ctx.setLineDash(dash[f.style]);
    ctx.lineWidth = f.style === "frame" || f.style === "card" ? 1.5 : f.style === "push" ? 3 : 2.25;
    ctx.strokeStyle = withAlpha(color, 0.85);
    // Path drawn so far: whole earlier legs + the current leg up to t.
    ctx.beginPath();
    for (let i = 0; i <= leg; i++) {
      const { a, c, b } = legs[i];
      const end = i < leg ? 1 : t;
      ctx.moveTo(a.x, a.y);
      const steps = 20;
      for (let s = 1; s <= steps; s++) {
        const p = bezierAt(a, c, b, (end * s) / steps);
        ctx.lineTo(p.x, p.y);
      }
    }
    ctx.stroke();
    ctx.setLineDash([]);

    const head = bezierAt(legs[leg].a, legs[leg].c, legs[leg].b, t);
    if (f.progress <= 1) {
      const r = f.style === "frame" ? 3 : f.style === "push" ? 5.5 : 4.5;
      ctx.beginPath();
      if (f.style === "card") ctx.rect(head.x - 4, head.y - 4, 8, 8);
      else ctx.arc(head.x, head.y, r, 0, Math.PI * 2);
      ctx.fillStyle = color;
      ctx.fill();
      ctx.lineWidth = 2;
      ctx.strokeStyle = SURFACE;
      ctx.stroke();
    }
    if (f.blockedAt && f.progress >= 0.98) {
      const p = this.pointOf(f.blockedAt);
      if (p) this.cross(ctx, f.blockedAt === HUB ? { x: p.x, y: p.y + HUB_H / 2 + 8 } : p);
    }
    const important = f.style === "denied" || f.style === "fault" || f.style === "push";
    if (f.label && f.progress <= 1 && (labels || important) && f.style !== "frame") {
      ctx.font = "600 11px ui-sans-serif, system-ui, sans-serif";
      ctx.textAlign = "left";
      ctx.textBaseline = "middle";
      ctx.lineWidth = 3;
      ctx.strokeStyle = SURFACE;
      const text = `${f.scenario.toUpperCase()} ${f.label}`;
      ctx.strokeText(text, head.x + 8, head.y - 8);
      ctx.fillStyle = "#0b0b0b";
      ctx.fillText(text, head.x + 8, head.y - 8);
    }
    ctx.restore();
  }

  private cross(ctx: CanvasRenderingContext2D, p: Pt): void {
    ctx.save();
    ctx.globalAlpha = 1;
    ctx.beginPath();
    ctx.arc(p.x, p.y, 8, 0, Math.PI * 2);
    ctx.fillStyle = STATUS.critical;
    ctx.fill();
    ctx.strokeStyle = SURFACE;
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.moveTo(p.x - 3.5, p.y - 3.5);
    ctx.lineTo(p.x + 3.5, p.y + 3.5);
    ctx.moveTo(p.x + 3.5, p.y - 3.5);
    ctx.lineTo(p.x - 3.5, p.y + 3.5);
    ctx.stroke();
    ctx.restore();
  }

  private onClick(p: Pt): void {
    const h = this.hubPoint();
    if (Math.abs(p.x - h.x) <= HUB_W / 2 && Math.abs(p.y - h.y) <= HUB_H / 2) {
      this.emit("hub");
      return;
    }
    const l = this.llmPoint();
    if (Math.abs(p.x - l.x) <= LLM_W / 2 && Math.abs(p.y - l.y) <= HUB_H / 2) {
      this.emit("llm");
      return;
    }
    // Nearest flight path within 10 px (newest first).
    let best: { d: number; f: Flight } | null = null;
    for (const f of this.lastFlights) {
      const legs = this.legPoints(f);
      if (!legs) continue;
      for (const { a, c, b } of legs) {
        let prev = a;
        for (let s = 1; s <= 12; s++) {
          const q = bezierAt(a, c, b, s / 12);
          const d = distToSegment(p, prev, q);
          if (d < 10 && (!best || d < best.d || (d === best.d && f.event.t > best.f.event.t))) best = { d, f };
          prev = q;
        }
      }
    }
    if (best) this.emit("flight", best.f.event);
  }

  private requireMap(): L.Map {
    if (!this.map) throw new Error("AtlasMap not mounted");
    return this.map;
  }

  destroy(): void {
    this.resize?.disconnect();
    this.map?.remove();
    this.map = null;
    this.nodes.clear();
  }
}
