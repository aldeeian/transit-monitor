"use client";

import { useEffect, useState, type DependencyList } from "react";

/**
 * Fetches once on mount and then re-fetches on a fixed interval, keeping the
 * last successful value on screen if a later poll fails (so a transient blip
 * doesn't blank the UI). `error` reflects whether the most recent attempt
 * failed. Shared by every auto-refreshing view.
 */
export function usePolledData<T>(
  loader: () => Promise<T>,
  intervalMs: number,
  deps: DependencyList = [],
): { data: T | null; error: boolean } {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    let stopped = false;
    const load = async () => {
      try {
        const next = await loader();
        if (!stopped) {
          setData(next);
          setError(false);
        }
      } catch {
        if (!stopped) setError(true);
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
