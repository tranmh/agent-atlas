/**
 * Server-Sent Events over `fetch` + `ReadableStream`.
 *
 * `EventSource` cannot send an `Authorization` header, so the portal stream
 * (GET /api/status/stream) is read with fetch and parsed here following the
 * WHATWG event-stream rules: lines end in CRLF, LF or CR (also split across
 * chunks), `:` starts a comment, `data:` lines are joined with "\n", `id:`
 * sets the last event id (kept across events), `retry:` sets the reconnection
 * time, an empty line dispatches the event.
 */

export interface SseEvent {
  /** Event type; "message" when the server sent no `event:` field. */
  event: string;
  data: string;
  /** Last event id seen on this stream (persists across events), or null. */
  id: string | null;
}

export class SseParser {
  private buffer = "";
  private dataLines: string[] = [];
  private eventType = "";
  private started = false;
  private pendingCR = false;
  /** Last `id:` value (null until one was received). */
  lastEventId: string | null;
  /** Last valid `retry:` value in ms (null until one was received). */
  retry: number | null = null;

  constructor(
    private readonly onEvent: (e: SseEvent) => void,
    initialLastEventId: string | null = null,
  ) {
    this.lastEventId = initialLastEventId;
  }

  /** Feed decoded text; complete events are dispatched synchronously. */
  feed(chunk: string): void {
    if (!this.started) {
      if (chunk.length === 0) return;
      this.started = true;
      if (chunk.charCodeAt(0) === 0xfeff) chunk = chunk.slice(1);
    }
    // The previous chunk ended in CR: a leading LF belongs to that CRLF.
    if (this.pendingCR && chunk.startsWith("\n")) chunk = chunk.slice(1);
    this.pendingCR = false;
    this.buffer += chunk;

    let start = 0;
    const buf = this.buffer;
    for (let i = 0; i < buf.length; i++) {
      const c = buf[i];
      if (c === "\n" || c === "\r") {
        this.processLine(buf.slice(start, i));
        if (c === "\r") {
          if (i === buf.length - 1) this.pendingCR = true;
          else if (buf[i + 1] === "\n") i++;
        }
        start = i + 1;
      }
    }
    this.buffer = buf.slice(start);
  }

  /** End of stream: an unterminated event is discarded (per spec). */
  end(): void {
    this.pendingCR = false;
    this.buffer = "";
    this.dataLines = [];
    this.eventType = "";
  }

  private processLine(line: string): void {
    if (line === "") {
      this.dispatch();
      return;
    }
    if (line.startsWith(":")) return; // comment / keep-alive

    const colon = line.indexOf(":");
    let field: string;
    let value: string;
    if (colon === -1) {
      field = line;
      value = "";
    } else {
      field = line.slice(0, colon);
      value = line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
    }

    switch (field) {
      case "event":
        this.eventType = value;
        break;
      case "data":
        this.dataLines.push(value);
        break;
      case "id":
        if (!value.includes("\u0000")) this.lastEventId = value;
        break;
      case "retry":
        if (/^\d+$/.test(value)) this.retry = Number(value);
        break;
      default:
        break; // unknown fields are ignored
    }
  }

  private dispatch(): void {
    if (this.dataLines.length === 0) {
      this.eventType = "";
      return;
    }
    const e: SseEvent = {
      event: this.eventType || "message",
      data: this.dataLines.join("\n"),
      id: this.lastEventId,
    };
    this.dataLines = [];
    this.eventType = "";
    this.onEvent(e);
  }
}

/** Non-2xx answer when opening the stream. */
export class SseHttpError extends Error {
  constructor(readonly status: number) {
    super(`event stream answered HTTP ${status}`);
    this.name = "SseHttpError";
  }
}

export class SseIdleTimeoutError extends Error {
  constructor(ms: number) {
    super(`no data on event stream for ${ms} ms`);
    this.name = "SseIdleTimeoutError";
  }
}

/** Default reconnect delay: exponential 1 s → 30 s. `attempt` starts at 1. */
export function defaultBackoff(attempt: number): number {
  return Math.min(1000 * 2 ** Math.max(0, attempt - 1), 30_000);
}

export interface SseCloseInfo {
  /** undefined when the server ended the stream normally. */
  error?: unknown;
  /** Consecutive failed/closed connections since the last one that delivered an event (>= 1). */
  attempt: number;
  /**
   * Consecutive connections that ended without delivering an event (failed to
   * open, or opened and closed/idled out before the first event).
   */
  failures: number;
  /** Delay before the next reconnect. */
  delayMs: number;
}

export interface SseClientOptions {
  url: string | (() => string);
  /** Called before every (re)connect, e.g. to put a fresh bearer token in. */
  headers?: () => Promise<Record<string, string>> | Record<string, string>;
  onEvent: (e: SseEvent) => void;
  onOpen?: () => void;
  /** After every disconnect, before the reconnect is scheduled. May call stop()/reconnect(). */
  onClose?: (info: SseCloseInfo) => void;
  /** Reconnect delay; `failures` = consecutive connects that delivered no event. */
  backoff?: (attempt: number, failures: number, serverRetryMs: number | null) => number;
  /** Abort and reconnect when nothing arrived for this long (0 = off). */
  idleTimeoutMs?: number;
  fetch?: typeof fetch;
}

/**
 * Reconnecting SSE client. Sends `Last-Event-ID` on reconnects and asks
 * `headers()` for fresh headers each time.
 */
export class SseClient {
  private generation = 0;
  private running = false;
  private abort: AbortController | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private idleTimer: ReturnType<typeof setTimeout> | null = null;
  private attempt = 0;
  private failures = 0;
  private serverRetry: number | null = null;
  lastEventId: string | null = null;
  connected = false;

  constructor(private readonly opts: SseClientOptions) {}

  start(): void {
    if (this.running) return;
    this.running = true;
    this.connect(++this.generation);
  }

  stop(): void {
    this.running = false;
    this.generation++;
    this.clearTimers();
    this.abort?.abort();
    this.abort = null;
    this.connected = false;
  }

  /** Drop the current connection (if any) and connect again right away. */
  reconnect(): void {
    if (!this.running) return;
    this.generation++;
    this.clearTimers();
    this.abort?.abort();
    this.abort = null;
    this.connected = false;
    this.connect(this.generation);
  }

  private clearTimers(): void {
    if (this.timer) clearTimeout(this.timer);
    if (this.idleTimer) clearTimeout(this.idleTimer);
    this.timer = null;
    this.idleTimer = null;
  }

  private armIdle(ctrl: AbortController, gen: number): void {
    const ms = this.opts.idleTimeoutMs ?? 0;
    if (ms <= 0) return;
    if (this.idleTimer) clearTimeout(this.idleTimer);
    this.idleTimer = setTimeout(() => {
      if (gen === this.generation) ctrl.abort(new SseIdleTimeoutError(ms));
    }, ms);
  }

  private async connect(gen: number): Promise<void> {
    const ctrl = new AbortController();
    this.abort = ctrl;
    const fetchImpl = this.opts.fetch ?? globalThis.fetch.bind(globalThis);
    let error: unknown;
    // A connection only counts as healthy once it delivered an event: a stream
    // that opens (2xx) and ends at once must keep backing off (and let the
    // caller fall back to polling) instead of reconnecting every second.
    let delivered = false;

    try {
      const extra = (await this.opts.headers?.()) ?? {};
      if (gen !== this.generation) return;
      // Only Accept (CORS-safelisted), Authorization and Last-Event-ID: the api's
      // CORS preflight must allow `Authorization, Last-Event-ID`.
      const headers: Record<string, string> = { Accept: "text/event-stream", ...extra };
      // An empty `id:` resets the last event id: send no header then (not an empty one).
      if (this.lastEventId) headers["Last-Event-ID"] = this.lastEventId;
      const url = typeof this.opts.url === "function" ? this.opts.url() : this.opts.url;

      this.armIdle(ctrl, gen);
      const res = await fetchImpl(url, { headers, signal: ctrl.signal, cache: "no-store" });
      if (!res.ok) {
        // Release the connection: the error body is never read.
        res.body?.cancel().catch(() => undefined);
        throw new SseHttpError(res.status);
      }
      if (!res.body) throw new Error("event stream has no body");

      this.connected = true;
      this.opts.onOpen?.();

      const parser = new SseParser((e) => {
        if (gen !== this.generation) return;
        if (!delivered) {
          delivered = true;
          this.attempt = 0;
          this.failures = 0;
        }
        this.lastEventId = e.id;
        this.opts.onEvent(e);
      }, this.lastEventId);

      const reader = res.body.getReader();
      const onAbort = () => {
        reader.cancel().catch(() => undefined);
      };
      ctrl.signal.addEventListener("abort", onAbort);
      const decoder = new TextDecoder();
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          if (gen !== this.generation) return;
          this.armIdle(ctrl, gen);
          parser.feed(decoder.decode(value, { stream: true }));
          if (parser.retry !== null) this.serverRetry = parser.retry;
        }
        parser.feed(decoder.decode());
        parser.end();
      } finally {
        ctrl.signal.removeEventListener("abort", onAbort);
      }
      if (ctrl.signal.aborted) error = ctrl.signal.reason ?? new Error("aborted");
    } catch (e) {
      error = ctrl.signal.aborted ? (ctrl.signal.reason ?? e) : e;
    }

    if (gen !== this.generation || !this.running) return;
    if (this.idleTimer) clearTimeout(this.idleTimer);
    this.idleTimer = null;
    this.connected = false;
    this.abort = null;

    this.attempt += 1;
    if (!delivered) this.failures += 1;
    const backoff = this.opts.backoff ?? ((a: number) => defaultBackoff(a));
    const delayMs = backoff(this.attempt, this.failures, this.serverRetry);
    this.opts.onClose?.({ error, attempt: this.attempt, failures: this.failures, delayMs });

    if (gen !== this.generation || !this.running) return; // onClose called stop()/reconnect()
    this.timer = setTimeout(() => {
      this.timer = null;
      if (gen === this.generation && this.running) this.connect(gen);
    }, delayMs);
  }
}
