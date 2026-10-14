"use client";

import { useCallback, useEffect, useRef, useState } from "react";

export interface AsyncState<T> {
  data: T | null;
  error: unknown;
  loading: boolean;
  reload: () => void;
}

/** Run `fn` on mount and whenever `deps` change; abortable, latest call wins. */
export function useAsync<T>(fn: (signal: AbortSignal) => Promise<T>, deps: readonly unknown[], enabled = true): AsyncState<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(enabled);
  const [tick, setTick] = useState(0);
  const fnRef = useRef(fn);
  fnRef.current = fn;

  useEffect(() => {
    if (!enabled) return;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);
    fnRef.current(ctrl.signal).then(
      (v) => {
        if (ctrl.signal.aborted) return;
        setData(v);
        setLoading(false);
      },
      (e) => {
        if (ctrl.signal.aborted) return;
        setError(e);
        setLoading(false);
      },
    );
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick, enabled]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, reload };
}
