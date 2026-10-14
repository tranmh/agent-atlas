import { afterEach, describe, expect, it, vi } from "vitest";
import { SseClient, SseHttpError, SseParser, defaultBackoff, type SseEvent } from "./sse";

function collect() {
  const events: SseEvent[] = [];
  const parser = new SseParser((e) => events.push(e));
  return { events, parser };
}

/** A controllable ReadableStream response. */
export function streamResponse(status = 200) {
  const enc = new TextEncoder();
  let ctrl!: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      ctrl = c;
    },
  });
  return {
    response: new Response(body, { status, headers: { "Content-Type": "text/event-stream" } }),
    push: (s: string) => ctrl.enqueue(enc.encode(s)),
    close: () => ctrl.close(),
  };
}

describe("SseParser", () => {
  it("parses events with id, event and data", () => {
    const { events, parser } = collect();
    parser.feed('retry: 3000\n\nevent: snapshot\ndata: {"a":1}\n\nid: 7\nevent: plant\ndata: {"b":2}\n\n');
    expect(parser.retry).toBe(3000);
    expect(events).toEqual([
      { event: "snapshot", data: '{"a":1}', id: null },
      { event: "plant", data: '{"b":2}', id: "7" },
    ]);
  });

  it("handles events split at arbitrary chunk boundaries", () => {
    const wire = 'id: 1842\nevent: plant\ndata: {"plantId":"c18f9d5f","status":"yellow"}\n\nevent: heartbeat\ndata: {"time":"x"}\n\n';
    for (let size = 1; size <= 7; size++) {
      const { events, parser } = collect();
      for (let i = 0; i < wire.length; i += size) parser.feed(wire.slice(i, i + size));
      expect(events.map((e) => e.event)).toEqual(["plant", "heartbeat"]);
      expect(JSON.parse(events[0].data)).toEqual({ plantId: "c18f9d5f", status: "yellow" });
      expect(events[0].id).toBe("1842");
    }
  });

  it("joins multi-line data with newlines", () => {
    const { events, parser } = collect();
    parser.feed("data: line one\ndata: line two\ndata:\ndata:no-space\n\n");
    expect(events[0].data).toBe("line one\nline two\n\nno-space");
    expect(events[0].event).toBe("message");
  });

  it("ignores comments and blocks without data; keeps the last id across events", () => {
    const { events, parser } = collect();
    parser.feed(": keep-alive\n\nid: 5\n\nevent: x\n\n: c\ndata: y\n\n");
    expect(events).toEqual([{ event: "message", data: "y", id: "5" }]);
  });

  it("accepts CRLF and CR line endings, also split between chunks", () => {
    const { events, parser } = collect();
    parser.feed("event: a\r");
    parser.feed("\ndata: 1\r\n\r");
    parser.feed("\nevent: b\rdata: 2\r\r");
    expect(events).toEqual([
      { event: "a", data: "1", id: null },
      { event: "b", data: "2", id: null },
    ]);
  });

  it("strips a leading BOM and discards an unterminated event at the end", () => {
    const { events, parser } = collect();
    parser.feed("﻿data: x\n\ndata: unterminated");
    parser.end();
    expect(events).toEqual([{ event: "message", data: "x", id: null }]);
  });

  it("ignores invalid retry values", () => {
    const { parser } = collect();
    parser.feed("retry: soon\n\n");
    expect(parser.retry).toBeNull();
  });
});

describe("defaultBackoff", () => {
  it("is exponential from 1 s and capped at 30 s", () => {
    expect([1, 2, 3, 4, 5, 6, 7].map(defaultBackoff)).toEqual([1000, 2000, 4000, 8000, 16000, 30000, 30000]);
  });
});

describe("SseClient", () => {
  afterEach(() => vi.useRealTimers());

  it("sends fresh headers and Last-Event-ID, and reconnects with backoff", async () => {
    vi.useFakeTimers();
    const s1 = streamResponse();
    const s2 = streamResponse();
    const fetchMock = vi.fn().mockResolvedValueOnce(s1.response).mockResolvedValueOnce(s2.response);
    let token = 0;
    const events: SseEvent[] = [];
    const closes: number[] = [];
    const client = new SseClient({
      url: "http://api/api/status/stream",
      fetch: fetchMock,
      headers: async () => ({ Authorization: `Bearer t${++token}` }),
      onEvent: (e) => events.push(e),
      onClose: (i) => closes.push(i.delayMs),
    });
    client.start();
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(fetchMock.mock.calls[0][1].headers).toMatchObject({ Authorization: "Bearer t1", Accept: "text/event-stream" });
    expect(fetchMock.mock.calls[0][1].headers["Last-Event-ID"]).toBeUndefined();

    s1.push("id: 9\nevent: plant\ndata: {}\n\n");
    await vi.waitFor(() => expect(events).toHaveLength(1));
    s1.close();
    await vi.waitFor(() => expect(closes).toEqual([1000]));

    await vi.advanceTimersByTimeAsync(1000);
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(fetchMock.mock.calls[1][1].headers).toMatchObject({ Authorization: "Bearer t2", "Last-Event-ID": "9" });
    client.stop();
  });

  it("W7: does not send an empty Last-Event-ID after an empty id field", async () => {
    vi.useFakeTimers();
    const s1 = streamResponse();
    const s2 = streamResponse();
    const fetchMock = vi.fn().mockResolvedValueOnce(s1.response).mockResolvedValueOnce(s2.response);
    const events: SseEvent[] = [];
    const client = new SseClient({ url: "http://api/s", fetch: fetchMock, onEvent: (e) => events.push(e) });
    client.start();
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    s1.push("id: 9\nevent: plant\ndata: {}\n\nid:\nevent: plant\ndata: {}\n\n");
    await vi.waitFor(() => expect(events).toHaveLength(2));
    s1.close();
    await vi.advanceTimersByTimeAsync(1000);
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(fetchMock.mock.calls[1][1].headers).not.toHaveProperty("Last-Event-ID");
    client.stop();
  });

  it("W9: cancels the response body of a non-2xx answer", async () => {
    vi.useFakeTimers();
    const cancel = vi.fn();
    const body = new ReadableStream<Uint8Array>({ cancel });
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(body, { status: 503 }));
    const closes: unknown[] = [];
    const client = new SseClient({ url: "http://api/s", fetch: fetchMock, onEvent: () => undefined, onClose: (i) => closes.push(i.error) });
    client.start();
    await vi.waitFor(() => expect(closes).toHaveLength(1));
    expect(closes[0]).toBeInstanceOf(SseHttpError);
    expect(cancel).toHaveBeenCalled();
    client.stop();
  });

  it("reports HTTP errors and grows the delay on consecutive failures", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn().mockImplementation(async () => new Response("no", { status: 503 }));
    const infos: { err: unknown; failures: number; delayMs: number }[] = [];
    const client = new SseClient({
      url: "http://api/s",
      fetch: fetchMock,
      onEvent: () => undefined,
      onClose: (i) => infos.push({ err: i.error, failures: i.failures, delayMs: i.delayMs }),
    });
    client.start();
    await vi.waitFor(() => expect(infos).toHaveLength(1));
    await vi.advanceTimersByTimeAsync(1000);
    await vi.waitFor(() => expect(infos).toHaveLength(2));
    await vi.advanceTimersByTimeAsync(2000);
    await vi.waitFor(() => expect(infos).toHaveLength(3));
    expect(infos.map((i) => i.delayMs)).toEqual([1000, 2000, 4000]);
    expect(infos.map((i) => i.failures)).toEqual([1, 2, 3]);
    expect(infos[0].err).toBeInstanceOf(SseHttpError);
    expect((infos[0].err as SseHttpError).status).toBe(503);
    client.stop();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("W2: a stream that opens and closes without an event keeps backing off; an event resets it", async () => {
    vi.useFakeTimers();
    const enc = new TextEncoder();
    let calls = 0;
    const fetchMock = vi.fn().mockImplementation(async () => {
      calls += 1;
      const withEvent = calls > 5;
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          if (withEvent) c.enqueue(enc.encode("event: heartbeat\ndata: {}\n\n"));
          c.close();
        },
      });
      return new Response(body, { status: 200 });
    });
    const infos: { attempt: number; failures: number; delayMs: number }[] = [];
    const client = new SseClient({
      url: "http://api/s",
      fetch: fetchMock,
      onEvent: () => undefined,
      onClose: (i) => infos.push({ attempt: i.attempt, failures: i.failures, delayMs: i.delayMs }),
    });
    client.start();
    await vi.waitFor(() => expect(infos).toHaveLength(1));
    for (const [wait, n] of [[1000, 2], [2000, 3], [4000, 4], [8000, 5]] as const) {
      await vi.advanceTimersByTimeAsync(wait);
      await vi.waitFor(() => expect(infos).toHaveLength(n));
    }
    expect(infos.map((i) => i.delayMs)).toEqual([1000, 2000, 4000, 8000, 16000]);
    expect(infos.map((i) => i.failures)).toEqual([1, 2, 3, 4, 5]);

    // 6th connect delivers an event before closing: the backoff starts over.
    await vi.advanceTimersByTimeAsync(16000);
    await vi.waitFor(() => expect(infos).toHaveLength(6));
    expect(infos[5]).toEqual({ attempt: 1, failures: 0, delayMs: 1000 });
    client.stop();
  });

  it("reconnects when the stream is idle for too long", async () => {
    vi.useFakeTimers();
    const s1 = streamResponse();
    const s2 = streamResponse();
    const fetchMock = vi.fn().mockResolvedValueOnce(s1.response).mockResolvedValueOnce(s2.response);
    const client = new SseClient({ url: "http://api/s", fetch: fetchMock, idleTimeoutMs: 45_000, onEvent: () => undefined });
    client.start();
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    await vi.advanceTimersByTimeAsync(45_000);
    await vi.advanceTimersByTimeAsync(1_000);
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    client.stop();
  });
});
