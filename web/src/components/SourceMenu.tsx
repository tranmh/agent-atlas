"use client";

import { useRef, useState } from "react";
import type { SessionInfo } from "@/events/schema";
import { fetchSessions, sessionDownloadUrl } from "@/lib/api";
import type { Source } from "@/hooks/useAtlas";

export interface SourceMenuProps {
  source: Source;
  fixtures: boolean;
  onLive: () => void;
  onSession: (id: string) => Promise<void>;
  onFile: (f: File) => Promise<void>;
  onSample: () => Promise<void>;
}

/** Choose what to watch: live, a recorded session, a JSONL file, or the bundled sample. */
export function SourceMenu({ source, fixtures, onLive, onSession, onFile, onSample }: SourceMenuProps) {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [error, setError] = useState<string | null>(null);
  const file = useRef<HTMLInputElement>(null);

  const value = source.kind === "session" ? `session:${source.id}` : source.kind;
  const refresh = () => {
    if (fixtures) return;
    fetchSessions()
      .then(setSessions)
      .catch((e: Error) => setError(e.message));
  };
  const change = async (v: string) => {
    setError(null);
    try {
      if (v === "live") onLive();
      else if (v === "sample") await onSample();
      else if (v === "file") file.current?.click();
      else if (v.startsWith("session:")) await onSession(v.slice(8));
    } catch (e) {
      setError((e as Error).message);
    }
  };
  return (
    <div className="flex items-center gap-1 text-xs">
      <label className="text-ink-2" htmlFor="source">
        Watching
      </label>
      <select
        id="source"
        className="max-w-[14rem] rounded-md border border-line bg-surface-2 px-1.5 py-1 text-sm"
        value={value}
        onFocus={refresh}
        onMouseDown={refresh}
        onChange={(e) => void change(e.target.value)}
        data-testid="source-menu"
      >
        {!fixtures ? <option value="live">● Live</option> : null}
        <option value="sample">Sample recording</option>
        {source.kind === "file" ? <option value="file">File: {source.name}</option> : <option value="file">Open JSONL file…</option>}
        {sessions.length > 0 || source.kind === "session" ? (
          <optgroup label="Recorded sessions">
            {source.kind === "session" && !sessions.some((s) => s.id === source.id) ? <option value={`session:${source.id}`}>{source.id}</option> : null}
            {sessions.map((s) => (
              <option key={s.id} value={`session:${s.id}`}>
                {s.id}
                {s.current ? " (current)" : ""}
              </option>
            ))}
          </optgroup>
        ) : null}
      </select>
      {source.kind === "session" ? (
        <a className="underline" href={sessionDownloadUrl(source.id)} download>
          download
        </a>
      ) : null}
      <input
        ref={file}
        type="file"
        accept=".jsonl,.ndjson,application/x-ndjson"
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) void onFile(f).catch((err: Error) => setError(err.message));
          e.target.value = "";
        }}
      />
      {error ? <span className="text-critical">{error}</span> : null}
    </div>
  );
}
