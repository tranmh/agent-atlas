import { describe, expect, it } from "vitest";
import { parseJsonl, toAtlasEvent } from "./schema";

describe("schema", () => {
  it("uses produced_ts when set, else ts", () => {
    expect(toAtlasEvent({ id: 1, ts: "2026-10-14T10:00:01Z", produced_ts: "2026-10-14T10:00:00Z", source: "agent", kind: "agent_work" })?.t).toBe(Date.parse("2026-10-14T10:00:00Z"));
    expect(toAtlasEvent({ id: 1, ts: "2026-10-14T10:00:01Z", produced_ts: "0001-01-01T00:00:00Z", source: "agent", kind: "x" })?.t).toBe(Date.parse("2026-10-14T10:00:01Z"));
    expect(toAtlasEvent({ id: "nope" })).toBeNull();
  });

  it("parses JSONL and skips bad lines", () => {
    const text = '{"id":1,"ts":"2026-10-14T10:00:00Z","source":"a","kind":"k"}\nnot json\n\n{"id":2,"ts":"2026-10-14T10:00:01Z","source":"a","kind":"k"}\n';
    expect(parseJsonl(text).map((e) => e.id)).toEqual([1, 2]);
  });
});
