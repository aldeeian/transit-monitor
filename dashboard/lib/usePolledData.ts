"use client";

import { useEffect, useState, type DependencyList } from "react";

/**
 * Fetches once on mount and then re-fetches on a fixed interval, keeping the
 * last successful value on screen if a later poll fails (so a transient blip
 * doesn't blank the UI). `error` holds the message of the most recent failed
 * attempt (or null when the last attempt succeeded), so views can show why.
 * Shared by every auto-refreshing view.
 */
export function usePolledData<T>(
  loader: () => Promise<T>,
  intervalMs: number,
  deps: DependencyList = [],
): { data: T | null; error: string | null } {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let stopped = false;
    const load = async () => {
      try {
        const next = await loader();
        if (!stopped) {
          setData(next);
          setError(null);
        }
      } catch (e) {
        if (!stopped) setError(e instanceof Error ? e.message : String(e));
      }
    };
    load();
    const iv = setInterval(load, intervalMs);
    return () => {
      stopped = true;
      clearInterval(iv);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return { data, error };
}
