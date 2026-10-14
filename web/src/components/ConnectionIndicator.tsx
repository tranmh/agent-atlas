import type { FeedState } from "@/events/feed";
import type { Source } from "@/hooks/useAtlas";

const LOOK: Record<FeedState, { color: string; label: string }> = {
  idle: { color: "#9a9893", label: "not connected" },
  connecting: { color: "#fab219", label: "connecting…" },
  live: { color: "#0ca30c", label: "live" },
  reconnecting: { color: "#ec835a", label: "reconnecting…" },
  stopped: { color: "#9a9893", label: "replay only" },
};

export function ConnectionIndicator({ state, source }: { state: FeedState; source: Source }) {
  const look = source.kind === "live" ? LOOK[state] : { color: "#9a9893", label: source.kind === "session" ? `session ${source.id}` : source.kind === "file" ? source.name : "sample recording" };
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-ink-2" data-testid="connection" data-state={source.kind === "live" ? state : "replay"}>
      <span className="inline-block h-2.5 w-2.5 rounded-full" style={{ background: look.color }} aria-hidden />
      {look.label}
    </span>
  );
}
