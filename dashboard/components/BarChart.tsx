"use client";

import { useMemo, useState } from "react";
import { DayCount } from "@/lib/api";

/**
 * Daily new-disruption counts as a single-series SVG bar chart. Fills in
 * zero-count days so gaps read as real quiet days, not missing data.
 * Single series: no legend (the section title names it); per-bar hover
 * tooltip; max value direct-labeled.
 */
export default function BarChart({
  daily,
  days,
}: {
  daily: DayCount[];
  days: number;
}) {
  const [tip, setTip] = useState<{ x: number; y: number; text: string } | null>(
    null,
  );

  const series = useMemo(() => {
    const byDay = new Map(
      daily.map((d) => [new Date(d.day).toDateString(), d.count]),
    );
    const out: { label: string; count: number }[] = [];
    for (let i = days - 1; i >= 0; i--) {
      const d = new Date();
      d.setDate(d.getDate() - i);
      out.push({
        label: d.toLocaleDateString(undefined, { month: "short", day: "numeric" }),
        count: byDay.get(d.toDateString()) ?? 0,
      });
    }
    return out;
  }, [daily, days]);

  const W = 720;
  const H = 180;
  const padL = 28;
  const padB = 22;
  const padT = 14;
  const max = Math.max(1, ...series.map((s) => s.count));
  const bw = (W - padL) / series.length;
  const maxIdx = series.findIndex((s) => s.count === max);

  return (
    <div className="chart-wrap">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        role="img"
        aria-label={`New disruptions per day over the last ${days} days`}
        style={{ width: "100%", height: "auto", display: "block" }}
      >
        {/* recessive gridline at max */}
        <line
          x1={padL}
          x2={W}
          y1={padT}
          y2={padT}
          stroke="var(--border)"
          strokeDasharray="3 3"
        />
        <text x={0} y={padT + 4} fill="var(--faint)" fontSize="10">
          {max}
        </text>
        <line
          x1={padL}
          x2={W}
          y1={H - padB}
          y2={H - padB}
          stroke="var(--border)"
        />
        {series.map((s, i) => {
          const h = s.count === 0 ? 0 : ((H - padB - padT) * s.count) / max;
          const x = padL + i * bw + 1; // 2px gap between bars
          const y = H - padB - h;
          return (
            <g key={i}>
              {/* invisible full-height hit target, larger than the mark */}
              <rect
                x={padL + i * bw}
                y={0}
                width={bw}
                height={H - padB}
                fill="transparent"
                onMouseMove={(e) =>
                  setTip({
                    x: e.clientX,
                    y: e.clientY,
                    text: `${s.label}: ${s.count} new disruption${s.count === 1 ? "" : "s"}`,
                  })
                }
                onMouseLeave={() => setTip(null)}
              />
              {s.count > 0 && (
                <path
                  d={`M ${x} ${H - padB}
                      L ${x} ${y + 4}
                      Q ${x} ${y} ${x + 4} ${y}
                      L ${x + bw - 6} ${y}
                      Q ${x + bw - 2} ${y} ${x + bw - 2} ${y + 4}
                      L ${x + bw - 2} ${H - padB} Z`}
                  fill="var(--accent)"
                  pointerEvents="none"
                />
              )}
              {i === maxIdx && s.count > 0 && (
                <text
                  x={x + (bw - 2) / 2}
                  y={y - 4}
                  fill="var(--muted)"
                  fontSize="10"
                  textAnchor="middle"
                  pointerEvents="none"
                >
                  {s.count}
                </text>
              )}
              {(i === 0 || i === series.length - 1 || i % 7 === 0) && (
                <text
                  x={padL + i * bw + bw / 2}
                  y={H - 6}
                  fill="var(--faint)"
                  fontSize="10"
                  textAnchor="middle"
                >
                  {s.label}
                </text>
              )}
            </g>
          );
        })}
      </svg>
      {tip && (
        <div className="chart-tip" style={{ left: tip.x + 12, top: tip.y + 12 }}>
          {tip.text}
        </div>
      )}
    </div>
  );
}
