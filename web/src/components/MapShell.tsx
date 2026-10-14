"use client";

import dynamic from "next/dynamic";
import type { MapCanvasProps } from "./MapCanvas";

// Leaflet needs `window`: load the map (and its CSS) in the browser only.
const MapCanvas = dynamic(() => import("./MapCanvas"), {
  ssr: false,
  loading: () => <div className="h-full w-full animate-pulse bg-[var(--ocean)]" data-testid="map-loading" />,
});

export function MapShell(props: MapCanvasProps) {
  return <MapCanvas {...props} />;
}
