"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import {
  Disruption,
  RouteSummary,
  getJSON,
  severity,
  shortDate,
} from "@/lib/api";
import { usePolledData } from "@/lib/usePolledData";

type Sort = "newest" | "severity" | "route";

const sevRank = { red: 0, amber: 1, gray: 2 } as const;

export default function DisruptionsTable() {
  const [routeFilter, setRouteFilter] = useState<string | null>(null);
  const [text, setText] = useState("");
  const [sort, setSort] = useState<Sort>("newest");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const { data, error } = usePolledData(async () => {
    const [d, r] = await Promise.all([
      getJSON<{ disruptions: Disruption[] }>("/api/disruptions"),
      getJSON<{ routes: RouteSummary[] }>("/api/routes"),
    ]);
    return { disruptions: d.disruptions, routes: r.routes };
  }, 30_000);
  const disruptions = data?.disruptions ?? null;
  const routes = data?.routes ?? [];

  const shown = useMemo(() => {
    if (!disruptions) return [];
    let list = disruptions;
    if (routeFilter) list = list.filter((d) => d.routes.includes(routeFilter));
    if (text.trim()) {
      const q = text.trim().toLowerCase();
      list = list.filter(
        (d) =>
          d.header.toLowerCase().includes(q) ||
          d.description.toLowerCase().includes(q) ||
          d.routes.some((r) => r === q),
      );
    }
    return [...list].sort((a, b) => {
      if (sort === "newest") return b.first_seen_at.localeCompare(a.first_seen_at);
      if (sort === "severity")
        return sevRank[severity(a.effect)] - sevRank[severity(b.effect)];
      return (a.routes[0] ?? "").localeCompare(b.routes[0] ?? "", undefined, {
        numeric: true,
      });
    });
  }, [disruptions, routeFilter, text, sort]);

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  if (error && !disruptions) {
    return <p className="empty">api unreachable — is the ingest service running?</p>;
  }
  if (!disruptions) return <p className="empty">loading…</p>;

  return (
    <>
      <div className="controls">
        <input
          type="text"
          placeholder="filter text or route…"
          value={text}
          onChange={(e) => setText(e.target.value)}
          aria-label="Filter disruptions"
        />
        <span style={{ color: "var(--faint)", fontSize: 11 }}>sort:</span>
        {(["newest", "severity", "route"] as Sort[]).map((s) => (
          <button
            key={s}
            className={sort === s ? "route-chip active" : "route-chip"}
            onClick={() => setSort(s)}
          >
            {s}
          </button>
        ))}
      </div>

      <div className="route-chips" style={{ marginBottom: 10 }}>
        {routes.slice(0, 18).map((r) => (
          <button
            key={r.route}
            className={
              routeFilter === r.route ? "route-chip active" : "route-chip"
            }
            onClick={() =>
              setRouteFilter(routeFilter === r.route ? null : r.route)
            }
            title={`${r.active_now} active / ${r.new_in_range} new in window`}
          >
            {r.route} <span className="n">{r.active_now}</span>
          </button>
        ))}
      </div>

      <table>
        <thead>
          <tr>
            <th className="nowrap">effect</th>
            <th className="nowrap">routes</th>
            <th>disruption</th>
            <th className="nowrap">window</th>
          </tr>
        </thead>
        <tbody>
          {shown.map((d) => (
            <tr key={d.id} onClick={() => toggle(d.id)} style={{ cursor: "pointer" }}>
              <td className="nowrap">
                <span className={`badge ${severity(d.effect)}`}>{d.effect}</span>
              </td>
              <td className="nowrap">
                {d.routes.map((r) => (
                  <Link
                    key={r}
                    className="route-chip"
                    href={`/route/${encodeURIComponent(r)}`}
                    onClick={(e) => e.stopPropagation()}
                  >
                    {r}
                  </Link>
                ))}
              </td>
              <td>
                {d.header || <span className="desc">(no summary)</span>}
                {expanded.has(d.id) && d.description && (
                  <pre
                    className="desc"
                    style={{ whiteSpace: "pre-wrap", margin: "8px 0 0", fontFamily: "inherit" }}
                  >
                    {d.description}
                  </pre>
                )}
              </td>
              <td className="nowrap mono-num desc">
                {shortDate(d.starts_at)}
                <br />→ {shortDate(d.ends_at)}
              </td>
            </tr>
          ))}
          {shown.length === 0 && (
            <tr>
              <td colSpan={4} className="empty">
                no disruptions match
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </>
  );
}
