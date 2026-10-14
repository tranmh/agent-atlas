/**
 * Live event feed: the visualizer's SSE stream (GET /api/events/stream).
 * The first frame ("hello") names the session; "atlas" frames carry events.
 * Reconnects resume with Last-Event-ID; a new session (visualizer restarted)
 * is reported so the app can reset its buffer.
 */
import { SseClient, type SseEvent } from "@/map/sse";
import { toAtlasEvent, type AtlasEvent } from "./schema";

export type FeedState = "idle" | "connecting" | "live" | "reconnecting" | "stopped";

export interface FeedHandlers {
  onSession?: (session: string) => void;
  onEvents: (events: AtlasEvent[]) => void;
  onState?: (state: FeedState) => void;
}

export interface FeedOptions {
  url: string;
  /** Events are delivered in batches at most this often (ms). */
  flushMs?: number;
  fetch?: typeof fetch;
}

export class EventFeed {
  private client: SseClient | null = null;
  private pending: AtlasEvent[] = [];
  private timer: ReturnType<typeof setTimeout> | null = null;
  private session: string | null = null;
  state: FeedState = "idle";

  constructor(
    private readonly opts: FeedOptions,
    private readonly handlers: FeedHandlers,
  ) {}

  start(): void {
    if (this.client) return;
    this.setState("connecting");
    this.client = new SseClient({
      url: this.opts.url,
      fetch: this.opts.fetch,
      idleTimeoutMs: 45_000, // the server sends a heartbeat every 15 s
      onEvent: (e) => this.onEvent(e),
      onOpen: () => this.setState("live"),
      onClose: () => this.setState("reconnecting"),
    });
    this.client.start();
  }

  stop(): void {
    this.client?.stop();
    this.client = null;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.flush();
    this.setState("stopped");
  }

  private setState(s: FeedState): void {
    if (this.state === s) return;
    this.state = s;
    this.handlers.onState?.(s);
  }

  private onEvent(e: SseEvent): void {
    if (e.event === "hello") {
      try {
        const hello = JSON.parse(e.data) as { session?: string };
        if (hello.session && hello.session !== this.session) {
          this.flush();
          this.session = hello.session;
          this.handlers.onSession?.(hello.session);
        }
      } catch {
        // ignore malformed hello
      }
      return;
    }
    if (e.event !== "atlas") return;
    try {
      const ev = toAtlasEvent(JSON.parse(e.data));
      if (ev) this.pending.push(ev);
    } catch {
      return;
    }
    if (!this.timer) {
      this.timer = setTimeout(() => {
        this.timer = null;
        this.flush();
      }, this.opts.flushMs ?? 100);
    }
  }

  private flush(): void {
    if (this.pending.length === 0) return;
    const batch = this.pending;
    this.pending = [];
    this.handlers.onEvents(batch);
  }
}
