"use client";

import "leaflet/dist/leaflet.css";
import { useEffect, useRef } from "react";
import { AtlasMap } from "@/map/atlas-map";
import type { AtlasEvent, Scenario } from "@/events/schema";

export interface MapCanvasProps {
  scenarios: Scenario[];
  visible: (code: string) => boolean;
  osm: boolean;
  onReady: (map: AtlasMap | null) => void;
  onAgent: (id: string) => void;
  onFlight: (e: AtlasEvent) => void;
  onHub: () => void;
}

/** Thin React wrapper: mounts the framework-free AtlasMap once. */
export default function MapCanvas({ scenarios, visible, osm, onReady, onAgent, onFlight, onHub }: MapCanvasProps) {
  const el = useRef<HTMLDivElement>(null);
  const mapRef = useRef<AtlasMap | null>(null);
  const handlers = useRef({ onAgent, onFlight, onHub });
  handlers.current = { onAgent, onFlight, onHub };

  useEffect(() => {
    if (!el.current) return;
    const m = new AtlasMap().mount(el.current);
    m.on("agent", (id) => handlers.current.onAgent(id));
    m.on("flight", (e) => handlers.current.onFlight(e));
    m.on("hub", () => handlers.current.onHub());
    m.on("llm", () => handlers.current.onHub());
    mapRef.current = m;
    onReady(m);
    return () => {
      onReady(null);
      m.destroy();
      mapRef.current = null;
    };
    // Mount once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!mapRef.current || scenarios.length === 0) return;
    mapRef.current.setScenarios(scenarios);
    mapRef.current.setVisibility(visible);
    mapRef.current.fit(null);
    // Only when the scenario list changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scenarios]);

  useEffect(() => {
    mapRef.current?.setVisibility(visible);
  }, [visible]);

  useEffect(() => {
    mapRef.current?.setOsm(osm);
  }, [osm]);

  return <div ref={el} className="h-full w-full" data-testid="atlas-map" aria-label="World map of agents" role="application" />;
}
