/**
 * Imports Leaflet and makes sure the global `L` exists before
 * leaflet.markercluster (a UMD plugin that extends the global `L`) is loaded.
 * Import this module before `leaflet.markercluster` (see map-controller.ts).
 *
 * In an LWC, Leaflet is loaded as a static resource and `window.L` already
 * exists; this module then simply re-exports it.
 */
import L from "leaflet";

const g = globalThis as unknown as { L?: typeof L };
if (!g.L) g.L = L;
if (typeof window !== "undefined") {
  const w = window as unknown as { L?: typeof L };
  if (!w.L) w.L = L;
}

export default L;
