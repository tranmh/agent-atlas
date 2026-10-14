/**
 * Minimal Server-Sent Events reader for tests: fetch + Authorization (which
 * EventSource cannot send), parsed per the WHATWG algorithm, for a bounded
 * time or until a predicate matches.
 */

export interface SseEvent {
  id: string;
  event: string;
  data: string;
  /** JSON.parse(data), or undefined when data is not JSON. */
  json: any;
  /** ms since the stream was opened */
  at: number;
}

export interface SseStream {
  /** HTTP status and content type of the response. */
  status: number;
  contentType: string;
  /** Events received so far (live array). */
  events: SseEvent[];
  /** Resolves with the first event matching `pred` (also already received ones). */
  waitFor(pred: (e: SseEvent) => boolean, timeoutMs: number, what?: string): Promise<SseEvent>;
  /** Stop reading; resolves when the reader has finished. */
  close(): Promise<void>;
  /** Resolves when the server ended the stream (or close() was called). */
  ended: Promise<void>;
}

export interface OpenOptions {
  token?: string;
  headers?: Record<string, string>;
}

/** Open an SSE stream. Rejects if the response is not 200 text/event-stream. */
export async function openSse(url: string, opts: OpenOptions = {}): Promise<SseStream> {
  const ctrl = new AbortController();
  const headers: Record<string, string> = { Accept: "text/event-stream", ...opts.headers };
  if (opts.token) headers.Authorization = `Bearer ${opts.token}`;
  const started = Date.now();
  const res = await fetch(url, { headers, signal: ctrl.signal });
  const contentType = res.headers.get("content-type") ?? "";
  if (res.status !== 200 || !contentType.startsWith("text/event-stream") || !res.body) {
    const body = await res.text().catch(() => "");
    ctrl.abort();
    throw Object.assign(new Error(`SSE ${url}: HTTP ${res.status} ${contentType} ${body.slice(0, 200)}`), {
      status: res.status,
    });
  }

  const events: SseEvent[] = [];
  const waiters = new Set<{ pred: (e: SseEvent) => boolean; resolve: (e: SseEvent) => void }>();
  const reader = res.body.getReader();
  const decoder = new TextDecoder();

  const ended = (async () => {
    let buf = "";
    let id = "";
    let event = "";
    let data: string[] = [];
    try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        let nl: number;
        while ((nl = buf.search(/\r\n|\r|\n/)) >= 0) {
          if (buf[nl] === "\r" && nl === buf.length - 1) break; // maybe \r\n split across chunks
          const line = buf.slice(0, nl);
          buf = buf.slice(nl + (buf.startsWith("\r\n", nl) ? 2 : 1));
          if (line === "") {
            if (data.length > 0) {
              const raw = data.join("\n");
              let json: any;
              try {
                json = JSON.parse(raw);
              } catch {
                json = undefined;
              }
              const e: SseEvent = { id, event: event || "message", data: raw, json, at: Date.now() - started };
              events.push(e);
              for (const w of [...waiters]) {
                if (w.pred(e)) {
                  waiters.delete(w);
                  w.resolve(e);
                }
              }
            }
            event = "";
            data = [];
            continue;
          }
          if (line.startsWith(":")) continue;
          const i = line.indexOf(":");
          const field = i < 0 ? line : line.slice(0, i);
          let v = i < 0 ? "" : line.slice(i + 1);
          if (v.startsWith(" ")) v = v.slice(1);
          if (field === "data") data.push(v);
          else if (field === "event") event = v;
          else if (field === "id" && !v.includes("\0")) id = v;
        }
      }
    } catch {
      // aborted by close()
    }
  })();

  return {
    status: res.status,
    contentType,
    events,
    ended,
    waitFor(pred, timeoutMs, what = "matching event") {
      const hit = events.find(pred);
      if (hit) return Promise.resolve(hit);
      return new Promise<SseEvent>((resolve, reject) => {
        const w = { pred, resolve: (e: SseEvent) => (clearTimeout(t), resolve(e)) };
        const t = setTimeout(() => {
          waiters.delete(w);
          const seen = events.map((e) => e.event).join(",");
          reject(new Error(`SSE: no ${what} within ${timeoutMs} ms (seen ${events.length}: ${seen.slice(0, 300)})`));
        }, timeoutMs);
        waiters.add(w);
        ended.then(() => {
          if (waiters.has(w)) {
            waiters.delete(w);
            clearTimeout(t);
            reject(new Error(`SSE: stream ended before ${what}`));
          }
        });
      });
    },
    async close() {
      ctrl.abort();
      await reader.cancel().catch(() => undefined);
      await ended;
    },
  };
}
