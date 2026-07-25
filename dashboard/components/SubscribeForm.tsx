"use client";

import { useState } from "react";
import { API_BASE } from "@/lib/api";

type Result =
  | { kind: "idle" }
  | { kind: "busy" }
  | { kind: "ok"; detail: string }
  | { kind: "err"; detail: string };

export default function SubscribeForm() {
  const [email, setEmail] = useState("");
  const [routes, setRoutes] = useState("");
  const [result, setResult] = useState<Result>({ kind: "idle" });

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setResult({ kind: "busy" });
    let res: Response;
    try {
      res = await fetch(`${API_BASE}/api/subscribe`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          email,
          routes: routes.split(",").map((r) => r.trim()).filter(Boolean),
        }),
      });
    } catch {
      setResult({ kind: "err", detail: "api unreachable" });
      return;
    }
    // A gateway or proxy can answer with HTML, so a parse failure means the
    // request reached something — don't report it as "api unreachable".
    let body: { error?: string; detail?: string };
    try {
      body = await res.json();
    } catch {
      setResult({
        kind: "err",
        detail: `unexpected response from the API (HTTP ${res.status})`,
      });
      return;
    }
    if (!res.ok) {
      setResult({ kind: "err", detail: body.error ?? `HTTP ${res.status}` });
    } else {
      setResult({ kind: "ok", detail: body.detail ?? "confirmation sent" });
    }
  };

  return (
    <div className="panel">
      <form onSubmit={submit} className="controls" style={{ margin: 0 }}>
        <input
          type="email"
          required
          placeholder="you@example.com"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          aria-label="Email address"
        />
        <input
          type="text"
          required
          placeholder="routes, e.g. 3, 90, 201"
          value={routes}
          onChange={(e) => setRoutes(e.target.value)}
          aria-label="Routes, comma separated"
        />
        <button type="submit" disabled={result.kind === "busy"}>
          {result.kind === "busy" ? "…" : "subscribe"}
        </button>
      </form>
      {result.kind === "ok" && <p className="hint msg-ok">{result.detail}</p>}
      {result.kind === "err" && <p className="hint msg-err">{result.detail}</p>}
      <p className="hint">
        Double opt-in: you get a confirmation email first — no alerts until you
        click it. Every alert has a one-click unsubscribe link. Emails go out
        within ~1 minute of Calgary Transit publishing a new disruption on your
        routes.
      </p>
      <p className="hint">
        Privacy: your email is stored only to send these alerts, never shared,
        and deleted immediately when you unsubscribe.
      </p>
    </div>
  );
}
