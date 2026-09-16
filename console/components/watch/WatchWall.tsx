"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Badge } from "@/components/ui";
import { useLiveAgents, type LiveSource, type Connection } from "@/lib/live-client";
import { statusChipTone, statusChipLabel, type AgentView } from "@/lib/live";
import { AgentPane } from "./panes";

const CONNECTION_TONE: Record<Connection, "pass" | "fail" | "warn" | "neutral"> = {
  connecting: "warn",
  live: "pass",
  ended: "neutral",
  lost: "fail",
};

const CONNECTION_WORD: Record<Connection, string> = {
  connecting: "connecting",
  live: "live",
  ended: "ended",
  lost: "connection lost",
};

/**
 * The fleet watch wall: every agent in a run, live, with a switcher.
 *
 * Two layouts, one keyboard model. The grid shows every pane at once and
 * reflows to a single column on a phone. Focus shows one pane large. Number
 * keys 1 through 9 jump straight to an agent, the arrows move between them, and
 * Escape leaves focus for the grid. The switcher does the same with the mouse,
 * and the focused control carries a real focus ring so a keyboard user can see
 * where they are.
 *
 * Nothing here pulses. The connection and each agent's state are static chips;
 * the only thing that moves is the frame, when a new one arrives.
 */
export function WatchWall({ source, boundaryNote }: { source: LiveSource; boundaryNote?: string }) {
  const { state, connection } = useLiveAgents(source);
  const agents = state.agents;
  const [focusId, setFocusId] = useState<string | null>(null);

  const focusIndex = useMemo(
    () => (focusId ? agents.findIndex((a) => a.id === focusId) : -1),
    [agents, focusId],
  );
  const focused = focusIndex >= 0 ? agents[focusIndex] : undefined;

  const moveFocus = useCallback(
    (delta: number) => {
      if (agents.length === 0) return;
      const base = focusIndex >= 0 ? focusIndex : 0;
      const next = (base + delta + agents.length) % agents.length;
      setFocusId(agents[next]!.id);
    },
    [agents, focusIndex],
  );

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setFocusId(null);
        return;
      }
      if (e.key === "ArrowRight" || e.key === "ArrowDown") {
        moveFocus(1);
        e.preventDefault();
        return;
      }
      if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
        moveFocus(-1);
        e.preventDefault();
        return;
      }
      if (/^[1-9]$/.test(e.key)) {
        const idx = Number(e.key) - 1;
        if (idx < agents.length) setFocusId(agents[idx]!.id);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [agents, moveFocus]);

  return (
    <div className="flex flex-col gap-4">
      <Header connection={connection} counts={state.counts} agentCount={agents.length} />

      {agents.length > 1 ? (
        <ViewSwitcher
          agents={agents}
          focusId={focusId}
          onFocus={setFocusId}
          onGrid={() => setFocusId(null)}
        />
      ) : null}

      {boundaryNote ? (
        <p className="text-[12px] leading-5 text-dim">{boundaryNote}</p>
      ) : null}

      {agents.length === 0 ? (
        <div className="rounded-lg border border-rule bg-card px-4 py-10 text-center text-[13px] text-dim">
          {connection === "lost"
            ? "The live stream could not be reached. Frames come from the runner edge, not this portal."
            : "Waiting for the run to announce its agents."}
        </div>
      ) : focused ? (
        <div className="mx-auto w-full max-w-[900px]">
          <AgentPane agent={focused} big />
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {agents.map((a) => (
            <button
              key={a.id}
              type="button"
              onClick={() => setFocusId(a.id)}
              className="cursor-pointer rounded-lg text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-ink"
              aria-label={`Focus ${a.persona ?? a.id}`}
            >
              <AgentPane agent={a} />
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function Header({
  connection,
  counts,
  agentCount,
}: {
  connection: Connection;
  counts: { passed: number; failed: number; flaky: number; blocked: number; unverified: number } | undefined;
  agentCount: number;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex items-center gap-2">
        <Badge tone={CONNECTION_TONE[connection]}>{CONNECTION_WORD[connection]}</Badge>
        <span className="text-[13px] text-muted">
          {agentCount} {agentCount === 1 ? "agent" : "agents"}
        </span>
      </div>
      {counts ? (
        <div className="flex items-center gap-3 text-[12px] tabular-nums text-muted">
          <span className="text-pass">{counts.passed} passed</span>
          <span className="text-fail">{counts.failed} failed</span>
          <span className="text-warn">{counts.flaky} flaky</span>
          <span className="text-warn">{counts.blocked} blocked</span>
          <span className="text-warn">{counts.unverified} unverified</span>
        </div>
      ) : null}
    </div>
  );
}

/** The agent switcher: a tab per agent plus a grid toggle. Real buttons, a
 *  visible focus ring, and the active one marked so a keyboard user sees it. */
function ViewSwitcher({
  agents,
  focusId,
  onFocus,
  onGrid,
}: {
  agents: readonly AgentView[];
  focusId: string | null;
  onFocus: (id: string) => void;
  onGrid: () => void;
}) {
  const activeRef = useRef<HTMLButtonElement>(null);
  return (
    <div
      role="tablist"
      aria-label="Agents"
      className="flex flex-wrap items-center gap-1.5 overflow-x-auto"
    >
      <button
        type="button"
        role="tab"
        aria-selected={focusId === null}
        onClick={onGrid}
        className={`rounded-md border px-2.5 py-1 text-[12px] font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-ink ${
          focusId === null
            ? "border-ink bg-ink text-paper"
            : "border-rule bg-card text-muted hover:bg-[rgba(16,16,16,0.035)]"
        }`}
      >
        Grid
      </button>
      {agents.map((a, i) => {
        const active = a.id === focusId;
        return (
          <button
            key={a.id}
            ref={active ? activeRef : undefined}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onFocus(a.id)}
            className={`flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-[12px] font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-ink ${
              active
                ? "border-ink bg-ink text-paper"
                : "border-rule bg-card text-muted hover:bg-[rgba(16,16,16,0.035)]"
            }`}
          >
            <span className="tabular-nums opacity-70">{i + 1}</span>
            <span className="max-w-[16ch] truncate">{a.persona ?? a.id}</span>
            <Badge tone={statusChipTone(a.state, a.verdict)}>
              {statusChipLabel(a.state, a.verdict)}
            </Badge>
          </button>
        );
      })}
    </div>
  );
}
