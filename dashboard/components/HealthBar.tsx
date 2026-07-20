"use client";

import { useEffect, useState } from "react";
import { API_BASE, HealthView, relTime } from "@/lib/api";

type State =
  | { kind: "loading" }
  | { kind: "unreachable" }
  | { kind: "loaded"; health: HealthView };

/**
 * Poller/system health, always visible in the top bar. Three honest states:
 * LIVE (green) — poller succeeded recently; STALE (amber) — API up but the
 * poller hasn't succeeded within its window; OFFLINE (red) — API gone.
 */
export default function HealthBar() {
  const [state, setState] = useState<State>({ kind: "loading" });
  // tick forces re-render so "Xs ago" ages between polls
  const [, setTick] = useState(0);

  useEffect(() => {
    let stop = false;
    const load = async () => {
      try {
        const res = await fetch(`${API_BASE}/api/health`, { cache: "no-store" });
        const health = (await res.json()) as HealthView;
        if (!stop) setState({ kind: "loaded", health });
      } catch {
        if (!stop) setState({ kind: "unreachable" });
      }
    };
    load();
    const poll = setInterval(load, 15_000);
    const tick = setInterval(() => setTick((t) => t + 1), 5_000);
    return () => {
      stop = true;
      clearInterval(poll);
      clearInterval(tick);
    };
  }, []);

  if (state.kind === "loading") {
    return <span className="health">health: …</span>;
  }
  if (state.kind === "unreachable") {
    return (
      <span className="health">
        <span className="dot down" />
        <b className="down-text">OFFLINE</b> api unreachable
      </span>
    );
  }
  const h = state.health;
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
