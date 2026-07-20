export const API_BASE =
  process.env.NEXT_PUBLIC_API_URL ?? "http://127.0.0.1:8080";

export type Disruption = {
  id: string;
  header: string;
  description: string;
  cause: string;
  effect: string;
  routes: string[];
  stops: string[];
  starts_at: string | null;
  ends_at: string | null;
  first_seen_at: string;
  last_seen_at: string;
  resolved_at: string | null;
};

export type RouteSummary = {
  route: string;
  active_now: number;
  new_in_range: number;
};

export type HealthView = {
  ok: boolean;
  last_poll_at: string | null;
  last_success_at: string | null;
  feed_timestamp: string | null;
  active_alerts: number;
  consecutive_failures: number;
};

export type DayCount = { day: string; count: number };

export type HistoryEvent = {
  alert_id: string;
  kind: string;
  header: string;
  effect: string;
  occurred_at: string;
};

export type HistoryResponse = {
  route: string;
  days: number;
  daily: DayCount[];
  events: HistoryEvent[];
};

export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, { cache: "no-store" });
  if (!res.ok && res.status !== 503) {
    // 503 from /api/health still carries a JSON body we want to render.
    throw new Error(`${path}: HTTP ${res.status}`);
  }
  return res.json();
}

/** Maps a backend effect string to a severity bucket for status colors. */
export function severity(effect: string): "red" | "amber" | "gray" {
  if (effect.includes("no service")) return "red";
  if (
    effect.includes("delay") ||
    effect.includes("detour") ||
    effect.includes("reduced") ||
    effect.includes("stop moved") ||
    effect.includes("modified") ||
    effect.includes("accessibility")
  ) {
    return "amber";
  }
  return "gray";
}

export function relTime(iso: string | null): string {
  if (!iso) return "–";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return `${Math.floor(s)}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export function shortDate(iso: string | null): string {
  if (!iso) return "–";
  return new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
