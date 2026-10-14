"use client";

import { Fragment, type ReactNode } from "react";

/** Pretty-printed JSON with light syntax colouring (no dependencies). */
export function JsonView({ value }: { value: unknown }) {
  const text = JSON.stringify(value, null, 2) ?? "undefined";
  const parts: ReactNode[] = [];
  const re = /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(text))) {
    if (m.index > last) parts.push(text.slice(last, m.index));
    if (m[1]) {
      parts.push(
        <span key={i++} className={m[2] ? "json-key" : "json-string"}>
          {m[1]}
        </span>,
      );
      if (m[2]) parts.push(m[2]);
    } else if (m[3]) {
      parts.push(
        <span key={i++} className="json-literal">
          {m[3]}
        </span>,
      );
    } else if (m[4]) {
      parts.push(
        <span key={i++} className="json-number">
          {m[4]}
        </span>,
      );
    }
    last = re.lastIndex;
  }
  parts.push(text.slice(last));
  return (
    <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-md border border-line bg-surface-2 p-3 font-mono text-[11.5px] leading-[1.45]" data-testid="json-view">
      {parts.map((p, k) => (
        <Fragment key={k}>{p}</Fragment>
      ))}
    </pre>
  );
}
