"use client";

import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { EventFeed, type FeedState } from "@/events/feed";
import { parseJsonl, ScenarioListSchema, type AtlasEvent, type Scenario } from "@/events/schema";
import { ReplayEngine } from "@/replay/engine";
import { viewFold, type ViewState } from "@/replay/reduce";
import { fetchScenarios, fetchSessionEvents, VISUALIZER_URL } from "@/lib/api";

export const USE_FIXTURES = process.env.NEXT_PUBLIC_USE_FIXTURES === "1";

/** Where the events on screen come from. */
export type Source = { kind: "live" } | { kind: "session"; id: string } | { kind: "file"; name: string } | { kind: "sample" };

export interface Atlas {
  engine: ReplayEngine<ViewState>;
  snapshot: ReturnType<ReplayEngine<ViewState>["getSnapshot"]>;
  scenarios: Scenario[];
  scenarioError: string | null;
  feedState: FeedState;
  source: Source;
  goLive: () => void;
  loadSession: (id: string) => Promise<void>;
  loadFile: (file: File) => Promise<void>;
  loadSample: () => Promise<void>;
}

async function fetchSample(): Promise<{ scenarios: Scenario[]; events: AtlasEvent[] }> {
  const [s, e] = await Promise.all([fetch("/samples/scenarios.json"), fetch("/samples/demo.jsonl")]);
  return { scenarios: ScenarioListSchema.parse(await s.json()), events: parseJsonl(await e.text()) };
}

export function useAtlas(): Atlas {
  const engine = useMemo(() => new ReplayEngine(viewFold), []);
  const snapshot = useSyncExternalStore(engine.subscribe, engine.getSnapshot, engine.getSnapshot);
  const [scenarios, setScenarios] = useState<Scenario[]>([]);
  const [scenarioError, setScenarioError] = useState<string | null>(null);
  const [feedState, setFeedState] = useState<FeedState>("idle");
  const [source, setSource] = useState<Source>(USE_FIXTURES ? { kind: "sample" } : { kind: "live" });
  const feedRef = useRef<EventFeed | null>(null);
  const sourceRef = useRef(source);
  sourceRef.current = source;

  // Scenarios: from the visualizer (retry until it answers), or the bundled sample.
  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const list = USE_FIXTURES ? (await fetchSample()).scenarios : await fetchScenarios();
        if (!stop) {
          setScenarios(list);
          setScenarioError(null);
        }
      } catch (e) {
        if (stop) return;
        setScenarioError((e as Error).message);
        timer = setTimeout(load, 3000);
      }
    };
    void load();
    return () => {
      stop = true;
      if (timer) clearTimeout(timer);
    };
  }, []);

  const stopFeed = useCallback(() => {
    feedRef.current?.stop();
    feedRef.current = null;
    setFeedState("stopped");
  }, []);

  const startFeed = useCallback(() => {
    if (feedRef.current) return;
    engine.load([], "live");
    const feed = new EventFeed(
      { url: `${VISUALIZER_URL}/api/events/stream` },
      {
        onSession: () => engine.load([], engine.mode),
        onEvents: (batch) => engine.append(batch),
        onState: setFeedState,
      },
    );
    feedRef.current = feed;
    feed.start();
  }, [engine]);

  const loadSample = useCallback(async () => {
    stopFeed();
    const { scenarios: s, events } = await fetchSample();
    if (scenarios.length === 0) setScenarios(s);
    engine.load(events, "replay");
    setSource({ kind: "sample" });
  }, [engine, scenarios.length, stopFeed]);

  useEffect(() => {
    if (USE_FIXTURES) {
      void loadSample();
      return;
    }
    startFeed();
    return stopFeed;
    // Start once on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const goLive = useCallback(() => {
    if (USE_FIXTURES) return;
    if (sourceRef.current.kind !== "live") {
      setSource({ kind: "live" });
      startFeed();
    }
    engine.goLive();
  }, [engine, startFeed]);

  const loadSession = useCallback(
    async (id: string) => {
      const events = await fetchSessionEvents(id);
      stopFeed();
      engine.load(events, "replay");
      setSource({ kind: "session", id });
    },
    [engine, stopFeed],
  );

  const loadFile = useCallback(
    async (file: File) => {
      const events = parseJsonl(await file.text());
      stopFeed();
      engine.load(events, "replay");
      setSource({ kind: "file", name: file.name });
    },
    [engine, stopFeed],
  );

  return { engine, snapshot, scenarios, scenarioError, feedState, source, goLive, loadSession, loadFile, loadSample };
}
