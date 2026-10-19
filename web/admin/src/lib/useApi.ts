"use client";

import { useEffect, useState } from "react";
import { errorMessage } from "./api";

export type Loaded<T> = { data: T | null; error: string | null; loading: boolean; reload: () => void };

type Result<T> = { key: string; data: T | null; error: string | null };

/**
 * Runs an openapi-fetch call on mount and whenever deps change (or reload() is called). `loading` is derived: a result belongs to
 * the key of the request that produced it, so a stale one is never shown for a newer request.
 */
export function useApi<T>(call: () => Promise<{ data?: T; error?: unknown }>, deps: ReadonlyArray<unknown>): Loaded<T> {
  const [tick, setTick] = useState(0);
  const key = JSON.stringify([...deps, tick]);
  const [result, setResult] = useState<Result<T> | null>(null);

  useEffect(() => {
    let alive = true;
    call()
      .then((r) => {
        if (!alive) return;
        if (r.error !== undefined) setResult({ key, data: null, error: errorMessage(r.error) });
        else setResult({ key, data: r.data ?? null, error: null });
      })
      .catch((e: unknown) => {
        if (alive) setResult({ key, data: null, error: errorMessage(e) });
      });
    return () => {
      alive = false;
    };
    // `call` is a fresh closure on every render; the key says when the request is a different one.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const current = result && result.key === key ? result : null;
  return { data: current?.data ?? null, error: current?.error ?? null, loading: current === null, reload: () => setTick((t) => t + 1) };
}
