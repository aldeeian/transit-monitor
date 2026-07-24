"use client";

import { useEffect, useState } from "react";
import { HealthView, getJSON, relTime } from "@/lib/api";
import { usePolledData } from "@/lib/usePolledData";

/**
 * Poller/system health, always visible in the top bar. Three honest states:
 * LIVE (green) — poller succeeded recently; STALE (amber) — API up but the
 * poller hasn't succeeded within its window; OFFLINE (red) — API gone.
 */
export default function HealthBar() {
  const { data: health, error } = usePolledData(
    () => getJSON<HealthView>("/api/health"),
    15_000,
  );
  // tick forces re-render so "Xs ago" ages between polls
  const [, setTick] = useState(0);
  useEffect(() => {
    const tick = setInterval(() => setTick((t) => t + 1), 5_000);
    return () => clearInterval(tick);
  }, []);

  if (error) {
    return (
      <span className="health">
        <span className="dot down" />
        <b className="down-text">OFFLINE</b> api unreachable
      </span>
    );
  }
  if (!health) {
    return <span className="health">health: …</span>;
  }
  const h = health;
  if (!h.ok) {
    return (
      <span className="health" title="The data poller has not succeeded recently; shown data may be outdated.">
        <span className="dot stale" />
        <b className="stale-text">STALE</b>
        last success {relTime(h.last_success_at)} · {h.consecutive_failures} failed polls
      </span>
    );
  }
  return (
    <span className="health">
      <span className="dot ok" />
      <b className="ok-text">LIVE</b>
      poll {relTime(h.last_success_at)} · {h.active_alerts} active
    </span>
  );
}
