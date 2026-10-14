"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { AtlasEvent } from "@/events/schema";
import { useAtlas, USE_FIXTURES } from "@/hooks/useAtlas";
import { kindInfo, type KindGroup } from "@/lib/format";
import type { AtlasMap } from "@/map/atlas-map";
import { deniedCorrs, FLIGHT_WINDOW_MS, flightsAt } from "@/replay/flights";
import { AgentPanel } from "./AgentPanel";
import { ConnectionIndicator } from "./ConnectionIndicator";
import { EventList } from "./EventList";
import { GatePrompt } from "./GatePrompt";
import { GatewayPanel } from "./GatewayPanel";
import { Legend } from "./Legend";
import { MapShell } from "./MapShell";
import { Narration } from "./Narration";
import { PayloadPanel } from "./PayloadPanel";
import { ReplayBar } from "./ReplayBar";
import { RunControls } from "./RunControls";
import { ScenarioChips } from "./ScenarioChips";
import { SourceMenu } from "./SourceMenu";
import { TaskStatePanel } from "./TaskStatePanel";

type Tab = "payload" | "task" | "gateway" | "agent";
const TABS: { id: Tab; label: string }[] = [
  { id: "payload", label: "Payload" },
  { id: "task", label: "Task" },
  { id: "gateway", label: "Gateway" },
  { id: "agent", label: "Agent" },
];

export function AtlasApp() {
  const atlas = useAtlas();
  const { engine, snapshot, scenarios } = atlas;
  const view = engine.state();
  const live = atlas.source.kind === "live";

  const [solo, setSolo] = useState<string | null>(null);
  const [muted, setMuted] = useState<Set<string>>(new Set());
  const [groups, setGroups] = useState<Set<KindGroup>>(new Set<KindGroup>(["story", "wire", "agent"]));
  const [selected, setSelected] = useState<AtlasEvent | null>(null);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [agentId, setAgentId] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("payload");
  const [osm, setOsm] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const [panels, setPanels] = useState<{ aside: boolean; drawer: boolean }>({ aside: false, drawer: false });
  const [playhead, setPlayhead] = useState(0);
  const mapRef = useRef<AtlasMap | null>(null);

  const visible = useCallback((code: string) => (solo ? code === solo : !muted.has(code)), [solo, muted]);
  const visibleEvent = useCallback((e: AtlasEvent) => !e.scenario || visible(e.scenario), [visible]);
  const visibleRef = useRef(visibleEvent);
  visibleRef.current = visibleEvent;

  // One animation loop: advance the clock, draw the flights.
  useEffect(() => {
    let raf = 0;
    let lastUi = 0;
    const loop = (now: number) => {
      engine.tick(Date.now());
      const m = mapRef.current;
      if (m) {
        const ph = engine.playhead;
        const win = engine.window(ph - FLIGHT_WINDOW_MS, ph + 3000);
        m.draw({ playhead: ph, flights: flightsAt(win, ph, deniedCorrs(win), visibleRef.current), view: engine.state() });
      }
      if (now - lastUi > 100) {
        lastUi = now;
        setPlayhead(engine.playhead);
      }
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(raf);
  }, [engine]);

  useEffect(() => {
    mapRef.current?.setView(engine.state());
  }, [engine, snapshot.version]);

  const onMapReady = useCallback(
    (m: AtlasMap | null) => {
      mapRef.current = m;
      if (m) m.setView(engine.state());
    },
    [engine],
  );

  const select = useCallback(
    (e: AtlasEvent, opts: { seek?: boolean } = {}) => {
      setSelected(e);
      if (e.task_id) setTaskId(e.task_id);
      setTab("payload");
      setPanels((p) => ({ ...p, drawer: true }));
      if (opts.seek && engine.mode === "replay") engine.seek(e.t);
    },
    [engine],
  );

  const selectById = useCallback(
    (id: number) => {
      const e = engine.all().find((x) => x.id === id);
      if (e) select(e, { seek: true });
    },
    [engine, select],
  );

  // Keyboard: space play/pause, arrows step, L live.
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      const t = ev.target as HTMLElement;
      if (t && (t.tagName === "INPUT" || t.tagName === "SELECT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      if (ev.key === " ") {
        ev.preventDefault();
        engine.togglePlay();
      } else if (ev.key === "ArrowRight") {
        const e = engine.stepForward(visibleRef.current);
        if (e) select(e);
      } else if (ev.key === "ArrowLeft") {
        const e = engine.stepBack(visibleRef.current);
        if (e) select(e);
      } else if (ev.key === "l" || ev.key === "L") {
        atlas.goLive();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [engine, select, atlas]);

  const listEvents = useMemo(() => {
    const out: AtlasEvent[] = [];
    const all = engine.all();
    for (let i = snapshot.cursor; i >= 0 && out.length < 400; i--) {
      const e = all[i];
      if (visibleEvent(e) && groups.has(kindInfo(e.kind).group)) out.push(e);
    }
    return out;
    // snapshot.version changes whenever events or the cursor change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [engine, snapshot.version, snapshot.cursor, visibleEvent, groups]);

  const gatewayEvents = useMemo(() => {
    const out: AtlasEvent[] = [];
    const all = engine.all();
    for (let i = snapshot.cursor; i >= 0 && out.length < 200; i--) {
      const e = all[i];
      if (e.kind === "gateway_access" && visibleEvent(e)) out.push(e);
    }
    return out;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [engine, snapshot.version, snapshot.cursor, visibleEvent]);

  const gates = useMemo(() => [...view.gates.values()].filter((g) => visible(g.scenario)), [view, snapshot.version, visible]); // eslint-disable-line react-hooks/exhaustive-deps
  const task = taskId ? (view.tasks.get(taskId) ?? null) : null;

  const toggleGroup = (g: KindGroup) =>
    setGroups((cur) => {
      const n = new Set(cur);
      if (n.has(g)) n.delete(g);
      else n.add(g);
      return n;
    });
  const onSolo = (code: string | null) => {
    setSolo(code);
    mapRef.current?.fit(code);
  };
  const onMute = (code: string) =>
    setMuted((cur) => {
      const n = new Set(cur);
      if (n.has(code)) n.delete(code);
      else n.add(code);
      return n;
    });

  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 5000);
    return () => clearTimeout(t);
  }, [toast]);

  return (
    <div className="flex h-screen flex-col">
      <header className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line bg-surface-1 px-3 py-2">
        <button type="button" className="rounded border border-line px-2 py-1 text-sm lg:hidden" onClick={() => setPanels((p) => ({ ...p, aside: !p.aside }))} aria-label="Scenarios and events">
          ☰
        </button>
        <div className="mr-2">
          <h1 className="text-lg font-semibold leading-tight">Agent Atlas</h1>
          <p className="text-[11px] text-ink-3">A2A agents talking through agentgateway, slow enough to follow</p>
        </div>
        <RunControls scenarios={scenarios} disabled={!live || scenarios.length === 0} onStarted={setToast} />
        <div className="ml-auto flex flex-wrap items-center gap-3">
          <SourceMenu source={atlas.source} fixtures={USE_FIXTURES} onLive={atlas.goLive} onSession={atlas.loadSession} onFile={atlas.loadFile} onSample={atlas.loadSample} />
          <label className="flex items-center gap-1 text-xs text-ink-2" title="Detailed OpenStreetMap tiles (needs internet)">
            <input type="checkbox" checked={osm} onChange={(e) => setOsm(e.target.checked)} /> OSM tiles
          </label>
          <ConnectionIndicator state={atlas.feedState} source={atlas.source} />
          <button type="button" className="rounded border border-line px-2 py-1 text-sm xl:hidden" onClick={() => setPanels((p) => ({ ...p, drawer: !p.drawer }))} aria-label="Details panel">
            ⓘ
          </button>
        </div>
      </header>
      {atlas.scenarioError && scenarios.length === 0 ? (
        <div className="border-b border-line bg-surface-1 px-3 py-1 text-xs text-critical" role="alert">
          Cannot load scenarios: {atlas.scenarioError}. Is the stack up (<code>make up</code>)? Retrying…
        </div>
      ) : null}
      <div className="relative flex min-h-0 flex-1">
        <aside
          className={`${panels.aside ? "fixed inset-y-0 left-0 z-[1200] flex shadow-xl" : "hidden"} w-80 shrink-0 flex-col gap-3 overflow-hidden border-r border-line bg-surface-1 p-3 lg:static lg:z-auto lg:flex lg:shadow-none`}
          aria-label="Scenarios and events"
        >
          <ScenarioChips scenarios={scenarios} solo={solo} muted={muted} runs={view.runs} onSolo={onSolo} onMute={onMute} />
          <EventList events={listEvents} scenarios={scenarios} groups={groups} selectedId={selected?.id ?? null} onToggleGroup={toggleGroup} onSelect={(e) => select(e, { seek: true })} />
        </aside>
        <main className="flex min-w-0 flex-1 flex-col">
          <div className="relative min-h-0 flex-1">
            <MapShell
              scenarios={scenarios}
              visible={visible}
              osm={osm}
              onReady={onMapReady}
              onAgent={(id) => {
                setAgentId(id);
                setTab("agent");
                setPanels((p) => ({ ...p, drawer: true }));
              }}
              onFlight={(e) => select(e)}
              onHub={() => {
                setTab("gateway");
                setPanels((p) => ({ ...p, drawer: true }));
              }}
            />
            <div className="pointer-events-none absolute left-3 top-3 z-[1000]">
              <GatePrompt gates={gates} scenarios={scenarios} live={live && snapshot.mode === "live"} />
            </div>
            <div className="pointer-events-auto absolute right-3 top-3 z-[1000] flex flex-col gap-1">
              <button type="button" className="rounded-md border border-line bg-surface-1 px-2 py-1 text-xs shadow" onClick={() => mapRef.current?.region("world")}>
                World
              </button>
              <button type="button" className="rounded-md border border-line bg-surface-1 px-2 py-1 text-xs shadow" onClick={() => mapRef.current?.region("europe")} data-testid="zoom-europe">
                Europe
              </button>
              <button type="button" className="rounded-md border border-line bg-surface-1 px-2 py-1 text-xs shadow" onClick={() => mapRef.current?.region("central")} title="Göppingen, Munich, Graz, Vienna" data-testid="zoom-central">
                Central EU
              </button>
            </div>
            <div className="pointer-events-none absolute bottom-3 left-3 right-3 z-[1000] sm:right-[18.5rem]">
              <Narration view={view} scenarios={scenarios} visible={visible} playhead={playhead} onSelect={selectById} />
            </div>
            <div className="pointer-events-none absolute bottom-3 right-3 z-[1000] hidden sm:block">
              <Legend />
            </div>
            {toast ? (
              <div className="absolute right-20 top-3 z-[1000] rounded-md bg-ink px-3 py-1.5 text-xs text-surface-1 shadow" role="status">
                {toast}
              </div>
            ) : null}
          </div>
          <ReplayBar
            snapshot={snapshot}
            playhead={playhead}
            canLive={!USE_FIXTURES}
            onToggle={() => engine.togglePlay()}
            onStepBack={() => {
              const e = engine.stepBack(visibleEvent);
              if (e) select(e);
            }}
            onStepForward={() => {
              const e = engine.stepForward(visibleEvent);
              if (e) select(e);
            }}
            onSeek={(t) => engine.seek(t)}
            onSpeed={(s) => engine.setSpeed(s)}
            onLive={atlas.goLive}
          />
        </main>
        <section
          className={`${panels.drawer ? "fixed inset-y-0 right-0 z-[1200] flex shadow-xl" : "hidden"} w-full max-w-[28rem] shrink-0 flex-col border-l border-line bg-surface-1 xl:static xl:z-auto xl:flex xl:w-[28rem] xl:shadow-none`}
          aria-label="Details"
        >
          <div className="flex border-b border-line" role="tablist">
            {TABS.map((t) => (
              <button
                key={t.id}
                type="button"
                role="tab"
                aria-selected={tab === t.id}
                className={`flex-1 px-2 py-2 text-sm ${tab === t.id ? "border-b-2 border-ink font-semibold" : "text-ink-2"}`}
                onClick={() => setTab(t.id)}
                data-testid={`tab-${t.id}`}
              >
                {t.label}
              </button>
            ))}
            <button type="button" className="px-2 text-ink-3 xl:hidden" onClick={() => setPanels((p) => ({ ...p, drawer: false }))} aria-label="Close details">
              ✕
            </button>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto" role="tabpanel">
            {tab === "payload" ? <PayloadPanel event={selected} scenarios={scenarios} onTask={(id) => (setTaskId(id), setTab("task"))} /> : null}
            {tab === "task" ? <TaskStatePanel task={task} scenarios={scenarios} onEvent={selectById} /> : null}
            {tab === "gateway" ? <GatewayPanel events={gatewayEvents} scenarios={scenarios} onSelect={(e) => select(e, { seek: true })} /> : null}
            {tab === "agent" ? <AgentPanel id={agentId} scenarios={scenarios} view={agentId ? view.agents.get(agentId) : undefined} live={live} /> : null}
          </div>
        </section>
      </div>
    </div>
  );
}
