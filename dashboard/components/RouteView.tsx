"use client";

import Link from "next/link";
import {
  Disruption,
  HistoryResponse,
  getJSON,
  severity,
  shortDate,
  relTime,
} from "@/lib/api";
import { usePolledData } from "@/lib/usePolledData";
import BarChart from "@/components/BarChart";

export default function RouteView({ route }: { route: string }) {
  const { data, error } = usePolledData(
    async () => {
      const [d, h] = await Promise.all([
        getJSON<{ disruptions: Disruption[] }>(
          `/api/disruptions?route=${encodeURIComponent(route)}`,
        ),
        getJSON<HistoryResponse>(
          `/api/routes/${encodeURIComponent(route)}/history?days=14`,
        ),
      ]);
      return { active: d.disruptions, history: h };
    },
    30_000,
    [route],
  );
  const active = data?.active ?? null;
  const history = data?.history ?? null;

  if (error && !active) {
    return <p className="empty">api unreachable — is the ingest service running?</p>;
  }
  if (!active || !history) return <p className="empty">loading…</p>;

  return (
    <main>
      <h1 style={{ marginTop: 24 }}>
        Route {route}{" "}
        <span className="count-note">
          — {active.length} active disruption{active.length === 1 ? "" : "s"} ·{" "}
          <Link href="/">all routes</Link>
        </span>
      </h1>

      <h2>Current</h2>
      {active.length === 0 && (
        <p className="empty">no active disruptions on route {route}</p>
      )}
      {active.length > 0 && (
        <table>
          <thead>
            <tr>
              <th className="nowrap">effect</th>
              <th>disruption</th>
              <th className="nowrap">window</th>
            </tr>
          </thead>
          <tbody>
            {active.map((d) => (
              <tr key={d.id}>
                <td className="nowrap">
                  <span className={`badge ${severity(d.effect)}`}>{d.effect}</span>
                </td>
                <td>
                  {d.header}
                  {d.description && d.description !== d.header && (
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
          </tbody>
        </table>
      )}

      <h2>
        New disruptions per day{" "}
        <span className="count-note">— last {history.days} days</span>
      </h2>
      <BarChart daily={history.daily} days={history.days} />

      <h2>
        Event log{" "}
        <span className="count-note">
          — every state change we recorded (last {history.days} days)
        </span>
      </h2>
      {history.events.length === 0 ? (
        <p className="empty">no recorded events yet</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th className="nowrap">when</th>
              <th className="nowrap">change</th>
              <th className="nowrap">effect</th>
              <th>disruption</th>
            </tr>
          </thead>
          <tbody>
            {history.events.map((e, i) => (
              <tr key={`${e.alert_id}-${e.kind}-${i}`}>
                <td className="nowrap mono-num desc" title={e.occurred_at}>
                  {relTime(e.occurred_at)}
                </td>
                <td className="nowrap">
                  <span
                    className={`badge ${e.kind === "resolved" ? "gray" : e.kind === "new" ? "amber" : "gray"}`}
                  >
                    {e.kind}
                  </span>
                </td>
                <td className="nowrap desc">{e.effect}</td>
                <td className="desc">{e.header || e.alert_id}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </main>
  );
}
