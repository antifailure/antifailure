"use client";

import { useState, type ReactNode } from "react";
import { StatusPill } from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";
import { FigCmd } from "./frame";
import {
  BypassSchematic,
  DiamondSchematic,
  IsoRings,
  IsoStack,
  IsoTwoPlanes,
  KeepBar,
} from "./draw";
import { FloatWindow, SageWell } from "@/components/pages/solutions/well";

export function POV01() {
  return (
    <WellFigure id="P-OV-01" tab="twin · one run" rail="CYCLE">
      <div className="[&>div]:!px-0 [&>div]:!py-0">
        <IsoStack
          compact
          planes={[
            { label: "PLAN" },
            { label: "PROVISION" },
            { label: "RUN", accent: true },
            { label: "DESTROY" },
          ]}
        />
      </div>
      <p className="mt-2 font-mono text-[11px] tracking-extra-tight text-black/45">One run, then gone.</p>
    </WellFigure>
  );
}

export function POV02({ rows }: { rows: { miss: string; have: string }[] }) {
  const fragments = ["Preview", "E2E", "Load", "Mirror"];
  const twin = ["State", "Contain", "Decide"];
  return (
    <WellFigure id="P-OV-02" tab="staging vs twin" rail="COMPARE">
      <div className="grid grid-cols-2 items-start gap-3 max-sm:grid-cols-1">
        <div className="rounded-[10px] border border-black/[0.06] bg-[#f7f7f5] p-3">
          <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-black/40">Fragments</div>
          <ul className="mt-3 space-y-2">
            {fragments.map((name) => (
              <li key={name} className="flex items-center gap-2">
                <span className="size-1.5 rounded-full bg-black/15" aria-hidden />
                <span className="font-mono text-[12px] text-black/40 line-through">{name}</span>
              </li>
            ))}
          </ul>
        </div>
        <div className="rounded-[10px] border border-black/[0.06] bg-white p-3">
          <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-black/40">Twin</div>
          <ul className="mt-3 space-y-2">
            {twin.map((name, i) => (
              <li key={name} className="flex items-center gap-2">
                <span className="size-1.5 rounded-full bg-[#33bf00]" />
                <span className="text-[13px] text-black">{name}</span>
                {i === twin.length - 1 ? (
                  <span className="ml-auto">
                    <StatusPill tone="FAIL">FAIL</StatusPill>
                  </span>
                ) : (
                  <span className="ml-auto font-mono text-[10px] text-black/30">↓</span>
                )}
              </li>
            ))}
          </ul>
        </div>
      </div>
      <div className="mt-3 overflow-x-auto rounded-[10px] border border-black/[0.06]">
        <div className="grid grid-cols-2 gap-x-4 bg-[#f7f7f5] px-3 py-1.5 font-mono text-[9px] uppercase tracking-[0.12em] text-black/40">
          <span>Staging misses</span>
          <span>Twin has</span>
        </div>
        {rows.map((row) => (
          <div
            key={row.miss}
            className="grid grid-cols-2 gap-x-4 border-t border-black/[0.06] px-3 py-2 font-mono text-[11px]"
          >
            <span className="text-black/40">{row.miss}</span>
            <span className="text-black/80">{row.have}</span>
          </div>
        ))}
      </div>
    </WellFigure>
  );
}

function FigChrome({
  id,
  tab,
  rail,
  tabs,
}: {
  id: string;
  tab: string;
  rail: string;
  tabs?: { label: string; active: boolean; onSelect: () => void }[];
}) {
  return (
    <div className="flex items-end justify-between gap-2 border-b border-black/[0.06] bg-[#f7f7f5] px-2.5 pt-2.5 sm:px-3">
      <div className="flex min-w-0 flex-1 flex-wrap items-end gap-1">
        {tabs ? (
          tabs.map((item) => (
            <button
              key={item.label}
              type="button"
              onClick={item.onSelect}
              className={cn(
                "rounded-t-[8px] px-2.5 py-1.5 text-[11px] font-medium sm:px-3 sm:py-2 sm:text-[12px]",
                item.active ? "bg-[#CAE6D9] text-[#285D49]" : "text-black/35",
              )}
            >
              {item.label}
            </button>
          ))
        ) : (
          <div className="flex min-w-0 items-center gap-1.5 rounded-t-[8px] bg-[#CAE6D9] px-2.5 py-1.5 text-[11px] font-medium text-[#285D49] sm:px-3 sm:py-2 sm:text-[12px]">
            <span className="truncate">{tab}</span>
            <span className="shrink-0 text-[#285D49]/40" aria-hidden>
              ×
            </span>
          </div>
        )}
      </div>
      <div className="mb-1.5 flex shrink-0 items-baseline gap-2">
        <div className="border-b-2 border-[#33bf00] pb-0.5 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-black">
          {rail}
        </div>
        <span className="hidden font-mono text-[9px] uppercase tracking-[0.14em] text-black/30 sm:inline">
          FIG. {id}
        </span>
      </div>
    </div>
  );
}

function WellFigure({
  id,
  tab,
  rail,
  children,
  compact,
  tabs,
}: {
  id: string;
  tab: string;
  rail: string;
  children: ReactNode;
  compact?: boolean;
  tabs?: { label: string; active: boolean; onSelect: () => void }[];
}) {
  return (
    <SageWell
      className={cn(
        "w-full self-start !min-h-0 !px-4 !py-5 md:!px-5 md:!py-6",
        compact && "!px-3 !py-4 md:!px-4 md:!py-5",
      )}
    >
      <FloatWindow className="w-full overflow-hidden">
        <FigChrome id={id} tab={tab} rail={rail} tabs={tabs} />
        <div className={cn("p-3.5 sm:p-4", compact && "!p-3 sm:!p-3.5")}>{children}</div>
      </FloatWindow>
    </SageWell>
  );
}

function CodePane({ source }: { source: string }) {
  return (
    <pre className="overflow-x-auto rounded-[10px] bg-[#f7f7f5] px-3 py-3 font-mono text-[12px] leading-5 tracking-extra-tight text-black/70">
      {source}
    </pre>
  );
}

function LockHoldViz({
  peak,
  peakLabel,
  tone,
}: {
  peak: number;
  peakLabel: string;
  tone: "fail" | "pass";
}) {
  const fail = tone === "fail";
  const color = fail ? "#C43D3D" : "#33bf00";
  const fill = fail ? "rgba(196,61,61,0.16)" : "rgba(51,191,0,0.18)";
  const barW = fail ? 312 : 28;
  const samples = Array.from({ length: 22 }, (_, i) => i);
  return (
    <>
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <div className="text-[13px] font-medium tracking-tight text-black">Lock hold · sampled every 250ms</div>
        <span className="shrink-0 font-mono text-[11px]" style={{ color }}>
          {peakLabel}
        </span>
      </div>
      <svg viewBox="0 0 480 148" className="mt-4 h-auto w-full" aria-hidden>
        {[28, 64, 100].map((y) => (
          <line key={y} x1="72" y1={y} x2="468" y2={y} stroke="rgba(0,0,0,0.06)" />
        ))}
        <text x="0" y="32" fill="rgba(0,0,0,0.4)" fontSize="8" fontFamily="ui-monospace, monospace">
          ACCESS EXCL.
        </text>
        <text x="0" y="68" fill="rgba(0,0,0,0.35)" fontSize="8" fontFamily="ui-monospace, monospace">
          Share
        </text>
        <text x="0" y="104" fill="rgba(0,0,0,0.35)" fontSize="8" fontFamily="ui-monospace, monospace">
          RowShare
        </text>
        <rect x="88" y="18" width={barW} height="20" rx="3" fill={fill} />
        <rect x="88" y="18" width={barW} height="20" rx="3" fill="none" stroke={color} strokeWidth="1.2" />
        <rect x="88" y="90" width="36" height="16" rx="3" fill="#CAE6D9" />
        <rect x="88" y="54" width="22" height="16" rx="3" fill="rgba(0,0,0,0.08)" />
        {fail ? <rect x="88" y="118" width="312" height="10" rx="2" fill="rgba(196,61,61,0.22)" /> : null}
        {samples.map((i) => {
          const x = 88 + i * (312 / 21);
          const inHold = fail ? i < 20 : i < 2;
          return (
            <circle key={i} cx={x} cy={inHold ? 28 : 98} r="1.6" fill={inHold ? color : "rgba(0,0,0,0.22)"} />
          );
        })}
        <line
          x1={88 + barW}
          y1="12"
          x2={88 + barW}
          y2="128"
          stroke={color}
          strokeWidth="1"
          strokeDasharray="2 3"
        />
        <text x={92 + barW} y="14" fill={color} fontSize="8" fontFamily="ui-monospace, monospace">
          {peak}s
        </text>
        <text x="88" y="144" fill="rgba(0,0,0,0.3)" fontSize="8" fontFamily="ui-monospace, monospace">
          0s
        </text>
        <text x="232" y="144" fill="rgba(0,0,0,0.3)" fontSize="8" fontFamily="ui-monospace, monospace">
          16s
        </text>
        <text x="392" y="144" fill="rgba(0,0,0,0.3)" fontSize="8" fontFamily="ui-monospace, monospace">
          32s
        </text>
      </svg>
      <div className="mt-1 flex justify-between font-mono text-[9px] text-black/35">
        <span>acquire</span>
        <span>{fail ? "110 samples" : "2 samples"}</span>
        <span>release</span>
      </div>
      <div className="mt-4 overflow-x-auto rounded-[10px] border border-black/[0.06]">
        <div className="grid grid-cols-[1fr_1.2fr_auto] gap-2 bg-[#f7f7f5] px-3 py-1.5 font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/40">
          <span>Session</span>
          <span>Mode</span>
          <span>Hold</span>
        </div>
        <div
          className={cn(
            "grid grid-cols-[1fr_1.2fr_auto] items-center gap-2 border-t border-black/[0.06] px-3 py-2.5",
            fail ? "bg-[#f8e4e4]" : "bg-[#E4F1EB]",
          )}
        >
          <span className="text-[13px] text-black">migrate</span>
          <span className="font-mono text-[11px]" style={{ color }}>
            {fail ? "ACCESS EXCLUSIVE" : "ShareUpdate"}
          </span>
          <span className="font-mono text-[12px] tabular-nums" style={{ color }}>
            {peak}s
          </span>
        </div>
        <div className="grid grid-cols-[1fr_1.2fr_auto] items-center gap-2 border-t border-black/[0.06] px-3 py-2.5">
          <span className="text-[13px] text-black">sampler</span>
          <span className="font-mono text-[11px] text-black/55">{fail ? "waiting" : "idle"}</span>
          <span className="font-mono text-[12px] text-black/55">{fail ? "yes" : "no"}</span>
        </div>
      </div>
    </>
  );
}

export function POV03() {
  return (
    <WellFigure id="P-OV-03" tab="subscriptions · ALTER" rail="LOCK">
      <LockHoldViz peak={27.4} peakLabel="27.4s ACCESS EXCLUSIVE" tone="fail" />
    </WellFigure>
  );
}

export function POV04() {
  const rows = [
    { k: "strongest lock", v: "ACCESS EXCLUSIVE 27.4s on subscriptions", tone: "block" as const },
    { k: "blocked another", v: "yes, a session was seen waiting", tone: "block" as const },
    { k: "table rewrite", v: "yes, reported by Postgres", tone: "warn" as const },
    { k: "plan change", v: "Index Scan to Seq Scan on events", tone: "warn" as const },
  ];
  return (
    <WellFigure id="P-OV-04" tab="af insights · rehearsal" rail="REPORT">
      <div className="grid grid-cols-4 gap-2 max-sm:grid-cols-2">
        {[
          ["Lock", "27.4s"],
          ["Blocked", "yes"],
          ["Rewrite", "yes"],
        ].map(([k, v]) => (
          <div key={k} className="rounded-[8px] border border-black/[0.06] bg-[#f7f7f5] px-3 py-2">
            <div className="font-mono text-[9px] uppercase tracking-[0.12em] text-black/40">{k}</div>
            <div className="mt-0.5 text-[14px] font-medium tabular-nums text-black">{v}</div>
          </div>
        ))}
        <div className="flex items-center justify-center rounded-[8px] bg-[#f8e4e4] px-3 py-2">
          <StatusPill tone="FAIL">FAIL</StatusPill>
        </div>
      </div>
      <div className="mt-3 overflow-x-auto rounded-[10px] border border-black/[0.06]">
        {rows.map((row, i) => (
          <div
            key={row.k}
            className={cn(
              "flex items-start justify-between gap-4 px-3 py-2.5",
              i > 0 && "border-t border-black/[0.06]",
              i % 2 === 0 ? "bg-[#f7f7f5]" : "bg-white",
            )}
          >
            <span className="shrink-0 font-mono text-[10px] uppercase tracking-[0.12em] text-black/40">
              {row.k}
            </span>
            <span
              className={cn(
                "text-right text-[12px] leading-4 tracking-extra-tight",
                row.tone === "block" ? "text-[#C43D3D]" : "text-black/70",
              )}
            >
              {row.v}
            </span>
          </div>
        ))}
      </div>
      <div className="mt-3 hidden grid-cols-2 gap-2 sm:grid">
        <div className="rounded-[8px] border border-[#285D49]/25 bg-[#E4F1EB] px-3 py-2.5">
          <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-[#285D49]">baseline</div>
          <div className="mt-1 text-[13px] text-black">Index Scan</div>
          <div className="mt-0.5 font-mono text-[10px] text-black/40">events</div>
        </div>
        <div className="rounded-[8px] border border-[#C43D3D]/30 bg-[#f8e4e4] px-3 py-2.5">
          <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-[#C43D3D]">candidate</div>
          <div className="mt-1 text-[13px] text-black">Seq Scan</div>
          <div className="mt-0.5 font-mono text-[10px] text-black/40">events</div>
        </div>
      </div>
      <p className="mt-3 rounded-[8px] bg-[#E4F1EB] px-3 py-2.5 font-mono text-[11px] leading-5 text-[#285D49]">
        lint · add a second column of the new type, backfill it, then drop the old one
      </p>
    </WellFigure>
  );
}

export function PTW01() {
  return (
    <WellFigure id="P-TW-01" tab="prod · twin" rail="ISOLATE">
      <div className="[&>div]:!px-0 [&>div]:!py-0">
        <IsoRings />
      </div>
      <div className="mt-2 grid grid-cols-2 gap-2">
        <div className="rounded-[8px] bg-[#f7f7f5] px-3 py-2">
          <div className="font-mono text-[9px] uppercase tracking-[0.12em] text-black/40">Production</div>
          <div className="mt-0.5 text-[13px] text-black">untouched</div>
        </div>
        <div className="rounded-[8px] bg-[#E4F1EB] px-3 py-2">
          <div className="font-mono text-[9px] uppercase tracking-[0.12em] text-[#285D49]">Twin</div>
          <div className="mt-0.5 text-[13px] text-black">disposable</div>
        </div>
      </div>
    </WellFigure>
  );
}

export function PTW02() {
  const states = [
    { name: "env.creating", tone: "mid" },
    { name: "env.ready", tone: "mid" },
    { name: "env.destroying", tone: "mid" },
    { name: "env.destroyed", tone: "pass" },
    { name: "env.failed", tone: "fail" },
  ] as const;
  return (
    <WellFigure id="P-TW-02" tab="environment lifecycle" rail="CYCLE">
      <ol className="overflow-hidden rounded-[10px] border border-black/[0.06]">
        {states.map((s, i) => (
          <li
            key={s.name}
            className={cn(
              "flex items-center gap-3 px-3 py-2.5",
              i > 0 && "border-t border-black/[0.06]",
              s.tone === "pass" && "bg-[#E4F1EB]",
              s.tone === "fail" && "bg-[#f8e4e4]",
            )}
          >
            <span
              className="size-1.5 shrink-0 rounded-full"
              style={{
                background:
                  s.tone === "pass" ? "#33bf00" : s.tone === "fail" ? "#C43D3D" : "rgba(0,0,0,0.22)",
              }}
              aria-hidden
            />
            <span className="min-w-0 font-mono text-[12px] tracking-extra-tight text-black">{s.name}</span>
          </li>
        ))}
      </ol>
    </WellFigure>
  );
}

export function PTW03({
  items,
}: {
  items: readonly { kicker: string; title: string; body?: string }[];
}) {
  return (
    <WellFigure id="P-TW-03" tab="isolation model" rail="BOUNDARY">
      <div className="flex items-center justify-end">
        <StatusPill tone="FAIL">fail closed</StatusPill>
      </div>
      <ul className="mt-3 grid grid-cols-2 gap-2 max-md:grid-cols-1">
        {items.map((item, i) => (
          <li
            key={item.kicker}
            className={cn(
              "rounded-[10px] border border-black/[0.06] bg-[#f7f7f5] p-2.5",
              i === items.length - 1 && "max-md:col-span-1 col-span-2",
            )}
          >
            <div className="font-mono text-[10px] uppercase tracking-[0.14em] text-black/40">{item.kicker}</div>
            <div className="mt-1 text-[13px] tracking-extra-tight text-black">{item.title}</div>
            {item.body ? (
              <p className="mt-1 text-[12px] leading-4 tracking-extra-tight text-black/45">{item.body}</p>
            ) : null}
          </li>
        ))}
      </ul>
    </WellFigure>
  );
}

export function PTW04() {
  const cmds = ["af up", "af ci", "af down"];
  return (
    <WellFigure id="P-TW-04" tab="where a run answers" rail="LOCAL">
      <div className="overflow-hidden rounded-[10px] border border-black/[0.06]">
        <div className="flex items-center gap-2 border-b border-black/[0.06] bg-[#f7f7f5] px-3 py-2.5">
          <span className="size-2 shrink-0 rounded-full bg-[#33bf00]" aria-hidden />
          <span className="min-w-0 truncate font-mono text-[12px] tabular-nums tracking-extra-tight text-black">
            http://127.0.0.1:46000
          </span>
        </div>
        <ol>
          {cmds.map((cmd, i) => (
            <li
              key={cmd}
              className={cn(
                "flex items-center gap-3 px-3 py-2.5",
                i > 0 && "border-t border-black/[0.06]",
                i === cmds.length - 1 && "bg-[#E4F1EB]",
              )}
            >
              <span className="w-5 shrink-0 font-mono text-[9px] text-black/30">
                {String(i + 1).padStart(2, "0")}
              </span>
              <FigCmd>$ {cmd}</FigCmd>
            </li>
          ))}
        </ol>
      </div>
    </WellFigure>
  );
}

export function PTW05() {
  const rows = [
    ["workers-08f2", "16:35"],
    ["app-08f2", "16:44"],
    ["sim-stripe", "16:53"],
    ["dns-clone", "17:02"],
    ["postgres-sub", "17:12"],
    ["vpc-iso", "17:21"],
    ["proxy-08f2", "17:30"],
  ];
  return (
    <WellFigure id="P-TW-05" tab="journal replay" rail="CLEANUP">
      <div className="overflow-x-auto rounded-[10px] border border-black/[0.06]">
        <div className="grid grid-cols-[1fr_auto] gap-2 bg-[#f7f7f5] px-3 py-1.5 font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/40">
          <span>Destroyed</span>
          <span>t+</span>
        </div>
        {rows.map(([id, at], i) => (
          <div
            key={id}
            className={cn(
              "grid grid-cols-[1fr_auto] items-baseline gap-2 border-t border-black/[0.06] px-3 py-1.5 font-mono text-[11px] tracking-extra-tight",
              i % 2 === 0 ? "bg-white" : "bg-[#f7f7f5]",
            )}
          >
            <span className="text-black/70">{id}</span>
            <span className="tabular-nums text-black/40">{at}</span>
          </div>
        ))}
      </div>
      <div className="mt-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 rounded-[8px] bg-[#E4F1EB] px-3 py-2">
        <span className="font-mono text-[11px] text-[#285D49]">14 removed</span>
        <span className="font-mono text-[11px] text-[#285D49]">0 left behind</span>
        <StatusPill tone="PASS">counted</StatusPill>
      </div>
    </WellFigure>
  );
}

export function PSS01() {
  const rows = [
    ["email", "MASK", "3z8t…@example.test"],
    ["session", "DELETE", "deleted"],
    ["api_key", "HASH", "b6929ad97b7b"],
  ];
  return (
    <WellFigure id="P-SS-01" tab="public.users" rail="SANITIZE">
      <div className="-m-3.5 overflow-hidden sm:-m-4">
        <div className="flex items-center justify-between gap-3 bg-[#E4F1EB] px-3 py-1">
          <span className="font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/40">
            Column
          </span>
          <span className="font-mono text-[10px] uppercase tracking-[0.14em] text-[#285D49]">unique</span>
        </div>
        {rows.map(([k, rule, v]) => (
          <div
            key={k}
            className="flex items-baseline justify-between gap-3 border-t border-black/[0.06] px-3 py-1.5"
          >
            <div className="min-w-0">
              <span className="font-mono text-[10px] text-black/40">{k}</span>
              <span className="ml-2 truncate font-mono text-[12px] tracking-extra-tight text-black">{v}</span>
            </div>
            <span
              className={cn(
                "shrink-0 font-mono text-[10px] uppercase tracking-[0.12em]",
                rule === "DELETE" ? "text-[#C43D3D]" : "text-[#285D49]",
              )}
            >
              {rule}
            </span>
          </div>
        ))}
      </div>
    </WellFigure>
  );
}

export function PSS02() {
  return (
    <WellFigure id="P-SS-02" tab="referential subset" rail="KEEP">
      <div className="-m-3.5 overflow-hidden sm:-m-4">
        <div className="px-3 pt-1.5 pb-0.5">
          <KeepBar kept={0.12} />
        </div>
        <div className="flex items-center justify-between border-t border-black/[0.06] bg-[#E4F1EB] px-3 py-1 font-mono text-[11px]">
          <span className="text-[#285D49]">u_8f2a parent kept</span>
          <span className="text-[#285D49]">o_441 follows</span>
        </div>
        <div className="flex items-center justify-between border-t border-black/[0.06] px-3 py-1 font-mono text-[11px] text-black/40">
          <span>u_bb12 sampled out</span>
          <span>o_902 dropped</span>
        </div>
        <div className="flex items-center justify-between border-t border-black/[0.06] bg-[#f8e4e4] px-3 py-1 font-mono text-[11px] text-[#C43D3D]">
          <span>sessions *</span>
          <span>DELETE</span>
        </div>
      </div>
    </WellFigure>
  );
}

export function PSS03() {
  return (
    <WellFigure id="P-SS-03" tab="restore · subset · mask" rail="ADAPTER">
      <div className="-m-3.5 overflow-hidden sm:-m-4">
        <div className="-my-16 max-sm:-my-10 [&>div]:!py-0">
          <IsoStack
            planes={[
              { label: "RESTORE" },
              { label: "SUBSET" },
              { label: "MASK", accent: true },
              { label: "DESTROY" },
            ]}
          />
        </div>
        <div className="border-t border-black/[0.06] bg-[#f7f7f5] px-3 py-1.5 font-mono text-[11px] tracking-extra-tight text-black/45">
          Postgres adapter · logical restore
        </div>
      </div>
    </WellFigure>
  );
}

export function PSS04() {
  return (
    <WellFigure id="P-SS-04" tab="control vs data" rail="ATTEST">
      <div className="-m-3.5 overflow-hidden sm:-m-4">
        <div className="-my-10 max-sm:-my-7 [&>div]:!py-0">
          <IsoTwoPlanes top="CONTROL PLANE" bottom="DATA PLANE" callout="ATTESTATION ONLY" />
        </div>
        <div className="grid grid-cols-2 gap-2 border-t border-black/[0.06] bg-[#f7f7f5] px-3 py-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-black/40">
          <span>evidence · hashes</span>
          <span className="text-right">snapshots · secrets</span>
        </div>
      </div>
    </WellFigure>
  );
}

export function PFW01() {
  return (
    <WellFigure id="P-FW-01" tab="sim · capture · deny" rail="EGRESS">
      <div className="-m-3.5 overflow-hidden sm:-m-4">
        <div className="[&>div]:!p-0">
          <DiamondSchematic
            nodes={[
              { label: "SIM" },
              { label: "CAPTURE" },
              { label: "DENY", cmd: "$ deny" },
              { label: "LEDGER" },
            ]}
          />
        </div>
      </div>
    </WellFigure>
  );
}

function ProviderBoard({
  id,
  provider,
  op,
  body,
  tone,
  chip,
  receipt,
}: {
  id: string;
  provider: string;
  op: string;
  body: string;
  tone: "PASS" | "FAIL";
  chip: string;
  receipt: string;
}) {
  const lines = receipt.split("\n");
  const rail = chip.toLowerCase();
  return (
    <WellFigure id={id} tab={provider} rail={chip} compact>
      <div className="-m-3 overflow-hidden sm:-m-3.5">
      <div className="flex items-center justify-between gap-2 px-3 pt-2.5">
        <span className="min-w-0 truncate font-mono text-[11px] tracking-extra-tight text-black">{op}</span>
        <StatusPill tone={tone}>{tone}</StatusPill>
      </div>
      <p className="mt-1 px-3 text-[12px] leading-4 tracking-extra-tight text-black/50">{body}</p>
      <div className="mt-2 border-t border-black/[0.06]">
        {lines.map((line, i) => (
          <div
            key={line}
            className={cn(
              "truncate px-2.5 py-1 font-mono text-[10px] leading-4",
              i === 0 ? "text-black" : "border-t border-black/[0.06] text-black/55",
              i === 0 && rail === "mock" && "bg-[#E4F1EB] text-[#285D49]",
              i === 0 && rail === "capture" && "bg-[#f7f7f5] text-black",
              i === 0 && tone === "FAIL" && "bg-[#f8e4e4] text-[#C43D3D]",
              i > 0 && rail === "capture" && i === lines.length - 1 && "text-[#285D49]",
            )}
          >
            {line}
          </div>
        ))}
      </div>
      </div>
    </WellFigure>
  );
}

export function PFW02() {
  return (
    <ProviderBoard
      id="P-FW-02"
      provider="Stripe"
      op="POST /v1/charges"
      body="Answered from the stateful pack that ships with the engine. Clone-local, not live."
      tone="PASS"
      chip="mock"
      receipt={"ch_sim_08f2\n$49.00 · cus_sim_11\nclone-local · not live"}
    />
  );
}

export function PFW03() {
  return (
    <ProviderBoard
      id="P-FW-03"
      provider="SendGrid"
      op="POST /v3/mail/send"
      body="Render and capture. Never deliver."
      tone="PASS"
      chip="capture"
      receipt={"MIME · captured copy\nSubject: Order #4182\nNEVER DELIVERED"}
    />
  );
}

export function PFW04() {
  return (
    <ProviderBoard
      id="P-FW-04"
      provider="Unknown host"
      op="CONNECT example.com:443"
      body="No rule names it, and the default is block. Refused at the gateway with a row in the log."
      tone="FAIL"
      chip="DENY"
      receipt={"deny_02\nno rule matches · default block\nfail closed"}
    />
  );
}

const LEDGER = [
  ["POST", "api.stripe.com/v1/charges", "mock", "ch_sim_08f2", "PASS"],
  ["POST", "api.sendgrid.com/v3/mail/send", "capture", "msg_sim_2a91", "PASS"],
  ["POST", "hooks.slack.com/services/T0/B0", "capture", "req_sim_91c0", "PASS"],
  ["POST", "api.openai.com/v1/chat/completions", "mock", "mock_5b12", "PASS"],
  ["GET", "api.prod.internal/v1/health", "production-host", "deny_01", "FAIL"],
  ["CONNECT", "example.com:443", "DENY", "deny_02", "FAIL"],
] as const;

export function PFW05() {
  const cols = "grid-cols-[3.25rem_minmax(7rem,1fr)_7.75rem_6.75rem]";
  return (
    <WellFigure id="P-FW-05" tab="effect ledger" rail="LEDGER">
      <div className="-m-3.5 min-w-0 max-w-none overflow-x-auto overscroll-x-contain sm:-m-4">
        <div className="min-w-[520px]">
          <div
            className={cn(
              "grid gap-x-2 bg-[#E4F1EB] px-2.5 py-1 font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/40",
              cols,
            )}
          >
            <span className="whitespace-nowrap">Verb</span>
            <span className="whitespace-nowrap">Host</span>
            <span className="whitespace-nowrap">Mode</span>
            <span className="whitespace-nowrap">Receipt</span>
          </div>
          {LEDGER.map(([method, dest, action, receipt, tone], i) => (
            <div
              key={receipt}
              className={cn(
                "grid items-center gap-x-2 border-t border-black/[0.06] px-2.5 py-1 font-mono text-[10px] tracking-extra-tight",
                cols,
                tone === "FAIL"
                  ? "bg-[#f8e4e4] shadow-[inset_2px_0_0_#C43D3D]"
                  : i % 2 === 0
                    ? "bg-white"
                    : "bg-[#f7f7f5]",
              )}
            >
              <span className="whitespace-nowrap text-black/35">{method}</span>
              <span className="min-w-0 truncate text-black/80">{dest}</span>
              <span
                className={cn(
                  "whitespace-nowrap",
                  tone === "FAIL" ? "text-[#C43D3D]" : "text-[#285D49]",
                )}
              >
                {action}
              </span>
              <span className="whitespace-nowrap text-black/35">{receipt}</span>
            </div>
          ))}
          <div className="border-t border-black/[0.06] bg-[#E4F1EB] px-2.5 py-1 font-mono text-[10px] text-[#285D49]">
            escaped 0
          </div>
        </div>
      </div>
    </WellFigure>
  );
}

export function PFW06() {
  return (
    <WellFigure id="P-FW-06" tab="bypass blocked" rail="DENY">
      <div className="-m-3.5 overflow-hidden sm:-m-4">
        <div className="overflow-hidden">
          <div className="-my-12 max-sm:-my-9 [&>div]:!py-0">
            <BypassSchematic />
          </div>
        </div>
        <div className="flex items-center justify-between gap-3 bg-[#f8e4e4] px-3 py-2">
          <span className="font-mono text-[12px] tracking-extra-tight text-black">TCP 18.4.2.9:443</span>
          <span className="shrink-0 font-mono text-[11px] text-[#C43D3D]">ENETUNREACH</span>
        </div>
      </div>
    </WellFigure>
  );
}

/** A shaped result, worst regression first, and an object per row rather than
 *  a tuple on purpose. figurecheck masks a bracketed run containing a percent
 *  sign, because that is also how a Tailwind arbitrary value is written, so
 *  `["GET /", "27%"]` is invisible to it and `{ share: "27%" }` is not. As
 *  tuples these four shares silently left the gate's view and their rows in
 *  figure-exemptions.tsv went stale, which is how the absence showed up.
 *
 *  POST /api/search was seen too few times in the export to earn a baseline,
 *  which is what its row shows. */
const ROUTES: { route: string; share: string; p95: string; base: string | null; delta: number | null }[] = [
  { route: "GET /api/subscriptions", share: "18%", p95: "412ms", base: "180ms", delta: 1.29 },
  { route: "GET /settings/billing", share: "34%", p95: "168ms", base: "150ms", delta: 0.12 },
  { route: "GET /", share: "27%", p95: "44ms", base: "41ms", delta: 0.07 },
  { route: "POST /api/search", share: "9%", p95: "228ms", base: null, delta: null },
];

const PLD_COLS =
  "grid-cols-[minmax(0,1fr)_2.75rem_3.25rem_3.75rem_4.75rem]";

export function PLD01() {
  return (
    <WellFigure id="P-LD-01" tab="af load" rail="SHAPE">
      <div className="flex items-center justify-between gap-3">
        <div className="text-[13px] font-medium tracking-tight text-black">shaped routes</div>
        <span className="font-mono text-[11px] text-black/40">source otel · 17.8/s</span>
      </div>
      <div className="mt-4 flex min-h-[220px] min-w-0 flex-col overflow-x-auto rounded-[10px] border border-black/[0.06]">
        <div
          className={cn(
            "hidden bg-[#f7f7f5] px-3 py-2 font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/40 sm:grid sm:gap-x-3",
            PLD_COLS,
          )}
        >
          <span>Route</span>
          <span className="text-right">Share</span>
          <span className="text-right">p95</span>
          <span className="text-right">Base</span>
          <span className="text-right">Δ</span>
        </div>
        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 bg-[#f7f7f5] px-3 py-2 font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/40 sm:hidden">
          <span>Route</span>
          <span className="text-right">Δ</span>
        </div>
        <div className="flex flex-1 flex-col">
          {ROUTES.map(({ route, share, p95, base, delta }, i) => {
            const hot = delta !== null && delta > 0.5;
            const deltaText = delta === null ? "no baseline" : `+${Math.round(delta * 100)}%`;
            const deltaCls =
              delta === null ? "text-black/30" : hot ? "text-[#C43D3D]" : "text-[#285D49]";
            const bg = hot ? "bg-[#f8e4e4]" : i % 2 === 0 ? "bg-white" : "bg-[#f7f7f5]";
            return (
              <div
                key={route}
                className={cn("flex flex-1 flex-col justify-center border-t border-black/[0.06]", bg)}
              >
                <div
                  className={cn(
                    "hidden items-baseline px-3 py-3 font-mono text-[11px] sm:grid sm:gap-x-3",
                    PLD_COLS,
                  )}
                >
                  <span className="min-w-0 truncate">{route}</span>
                  <span className="text-right text-black/40">{share}</span>
                  <span className="text-right">{p95}</span>
                  <span className="text-right text-black/40">{base ?? "no base"}</span>
                  <span className={cn("text-right tabular-nums", deltaCls)}>{deltaText}</span>
                </div>
                <div className="px-3 py-3 sm:hidden">
                  <div className="flex items-baseline justify-between gap-3">
                    <span className="min-w-0 truncate font-mono text-[11px]">{route}</span>
                    <span className={cn("shrink-0 font-mono text-[11px] tabular-nums", deltaCls)}>
                      {deltaText}
                    </span>
                  </div>
                  <div className="mt-1 font-mono text-[10px] text-black/40">
                    {share}
                    <span className="mx-1.5 text-black/20">·</span>
                    {p95}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      </div>
      <p className="mt-3 font-mono text-[10px] uppercase tracking-[0.12em] text-black/35">
        refused · POST /billing/upgrade · POST /api/payments/intent
      </p>
    </WellFigure>
  );
}

export function PLD02({ source }: { source: string }) {
  const lines = source.split("\n");
  return (
    <WellFigure id="P-LD-02" tab="load.yml" rail="MANIFEST" compact>
      <div className="flex items-center justify-between gap-3">
        <FigCmd>$ af load</FigCmd>
        <span className="font-mono text-[11px] text-black/40">otel</span>
      </div>
      <div className="mt-3 min-w-0 overflow-x-auto rounded-[10px] border border-black/[0.06] bg-[#f7f7f5]">
        <div className="flex min-w-max">
          <div
            className="select-none border-r border-black/[0.06] px-2 py-3 text-right font-mono text-[11px] leading-5 text-black/25"
            aria-hidden
          >
            {lines.map((_, i) => (
              <div key={i}>{i + 1}</div>
            ))}
          </div>
          <pre className="whitespace-pre px-3 py-3 font-mono text-[12px] leading-5 tracking-extra-tight text-black/70">
            {source}
          </pre>
        </div>
      </div>
    </WellFigure>
  );
}

export function PMG01({ captions }: { captions: readonly [string, string] }) {
  const [tab, setTab] = useState<0 | 1>(0);
  return (
    <div>
      <WellFigure
        id="P-MG-01"
        tab={tab === 0 ? "unsafe" : "expand-and-contract"}
        rail="LOCK"
        tabs={[
          { label: "unsafe", active: tab === 0, onSelect: () => setTab(0) },
          { label: "expand-and-contract", active: tab === 1, onSelect: () => setTab(1) },
        ]}
      >
        {tab === 0 ? (
          <LockHoldViz peak={27.4} peakLabel="27.4s ACCESS EXCLUSIVE" tone="fail" />
        ) : (
          <LockHoldViz peak={0.4} peakLabel="0.4s · nothing waiting" tone="pass" />
        )}
      </WellFigure>
      <p className="mt-5 max-w-[640px] text-[15px] leading-6 tracking-extra-tight text-black max-md:mt-4 max-md:text-[14px]">
        {captions[tab]}
      </p>
    </div>
  );
}

export function PMG02() {
  return (
    <WellFigure id="P-MG-02" tab="pg_locks · subscriptions" rail="HOLD" compact>
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="text-[13px] font-medium tracking-tight text-black">subscriptions</div>
          <p className="mt-0.5 font-mono text-[10px] text-black/40">
            second session · pg_locks every 250ms
          </p>
        </div>
        <StatusPill tone="FAIL">FAIL</StatusPill>
      </div>
      <div className="mt-3">
        <LockHoldViz peak={27.4} peakLabel="27.4s ACCESS EXCLUSIVE" tone="fail" />
      </div>
    </WellFigure>
  );
}

export function PMG03({ source }: { source: string }) {
  return (
    <WellFigure id="P-MG-03" tab="af insights" rail="REPORT" compact>
      <div className="flex items-center justify-between gap-3">
        <div className="text-[13px] font-medium tracking-tight text-black">rehearsal report</div>
        <StatusPill tone="FAIL">FAIL</StatusPill>
      </div>
      <div className="mt-3 min-w-0 overflow-hidden rounded-[10px] border border-black/[0.06] bg-[#f7f7f5]">
        <div className="border-b border-black/[0.06] px-3 py-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-black/40">
          internal/insights
        </div>
        <pre className="min-w-0 whitespace-pre-wrap break-words px-3 py-3 font-mono text-[12px] leading-5 tracking-extra-tight text-black/70">
          {source}
        </pre>
      </div>
    </WellFigure>
  );
}

export function PMG04() {
  return (
    <WellFigure id="P-MG-04" tab="ShareUpdate · subscriptions" rail="PASS" compact>
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="text-[13px] font-medium tracking-tight text-black">after expand-and-contract</div>
          <p className="mt-0.5 font-mono text-[10px] text-black/40">same table · nothing waiting</p>
        </div>
        <StatusPill tone="PASS">PASS</StatusPill>
      </div>
      <div className="mt-3">
        <LockHoldViz peak={0.4} peakLabel="0.4s · nothing waiting" tone="pass" />
      </div>
    </WellFigure>
  );
}

export function PRP01({
  tone,
  pr,
  title,
  evidence,
  merge,
}: {
  tone: "PASS" | "FAIL" | "UNVERIFIED";
  pr: string;
  title: string;
  evidence: string;
  merge: string;
}) {
  return (
    <WellFigure id="P-RP-01" tab={pr} rail="CHECK">
      <div className="flex items-center justify-between gap-3">
        <span className="font-mono text-[11px] text-black/45">{pr}</span>
        <StatusPill tone={tone} />
      </div>
      <div className="mt-4 text-[13px] font-medium tracking-tight text-black">{title}</div>
      <p className="mt-2 font-mono text-[11px] leading-4 text-black/45">{evidence}</p>
      <p
        className={cn(
          "mt-4 rounded-[8px] px-3 py-2.5 font-mono text-[10px] tracking-extra-tight",
          tone === "PASS" && "bg-[#E4F1EB] text-[#285D49]",
          tone === "FAIL" && "bg-[#f8e4e4] text-[#C43D3D]",
          tone === "UNVERIFIED" && "bg-[#f7f7f5] text-black/45",
        )}
      >
        {merge}
      </p>
    </WellFigure>
  );
}

export function PRP02() {
  return (
    <WellFigure id="P-RP-02" tab="pr/184 · add access_tier" rail="FAIL">
      <div className="flex items-center justify-between gap-3">
        <div className="text-[13px] font-medium tracking-tight text-black">pr/184 · add access_tier</div>
        <StatusPill tone="FAIL">required · FAIL</StatusPill>
      </div>
      <p className="mt-4 text-[13px] text-black">1 workflow failed, and 1 invariant did not hold.</p>
      <p className="mt-1 font-mono text-[11px] text-black/45">
        Invariant `one_active_subscription` does not hold.
      </p>
      <div className="mt-4 overflow-hidden rounded-[10px] border border-black/[0.06]">
        <div className="grid grid-cols-3 gap-2 bg-[#f7f7f5] px-3 py-1.5 font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/40">
          <span>account_id</span>
          <span>active</span>
          <span>latest</span>
        </div>
        {[
          ["acct_00418", "2", "sub_9c41"],
          ["acct_02277", "2", "sub_a180"],
          ["acct_09903", "3", "sub_b774"],
        ].map((row, i) => (
          <div
            key={row[0]}
            className={cn(
              "grid grid-cols-3 gap-2 border-t border-black/[0.06] px-3 py-1.5 font-mono text-[11px] tabular-nums",
              i === 2 ? "bg-[#f8e4e4] text-[#C43D3D]" : "text-black/70",
            )}
          >
            <span>{row[0]}</span>
            <span>{row[1]}</span>
            <span>{row[2]}</span>
          </div>
        ))}
      </div>
      <div className="mt-4 flex items-center justify-between rounded-[8px] bg-[#f7f7f5] px-3 py-2.5">
        <span className="font-mono text-[11px] text-black/35">Merge pull request · inert</span>
        <span className="rounded-[6px] border border-black/15 px-2 py-0.5 font-mono text-[10px] text-black/35">
          Merge
        </span>
      </div>
    </WellFigure>
  );
}

export function PAR01() {
  return (
    <WellFigure id="P-AR-01" tab="control plane" rail="STACK">
      <IsoStack
        planes={[
          { label: "CONTROL PLANE" },
          { label: "SNAPSHOTS" },
          { label: "EGRESS", accent: true },
          { label: "CLEANUP" },
        ]}
      />
      <p className="mt-2 font-mono text-[11px] text-black/45">outbound-only · bearer token over TLS</p>
    </WellFigure>
  );
}

export function PAR02() {
  return (
    <WellFigure id="P-AR-02" tab="evidence vs records" rail="BOUNDARY">
      <IsoTwoPlanes top="EVIDENCE" bottom="RECORDS" callout="does not enter" />
      <div className="mt-2 flex justify-between font-mono text-[10px] uppercase tracking-[0.12em] text-black/40">
        <span>reports · sha256</span>
        <span>snapshots · secrets</span>
      </div>
    </WellFigure>
  );
}

export function PAR03({
  inForce,
  planned,
}: {
  inForce: readonly string[];
  planned: readonly string[];
}) {
  return (
    <WellFigure id="P-AR-03" tab="in force · designed" rail="SCOPE">
      <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
        <div className="rounded-[10px] border border-black/[0.06] bg-white p-3">
          <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-[#285D49]">in force today</div>
          <ul className="mt-3 space-y-2">
            {inForce.map((item) => (
              <li key={item} className="flex gap-2 text-[12px] leading-5 tracking-extra-tight text-black">
                <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-[#33bf00]" aria-hidden />
                {item}
              </li>
            ))}
          </ul>
        </div>
        <div className="rounded-[10px] border border-black/[0.06] bg-[#f7f7f5] p-3">
          <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-black/40">designed, not built</div>
          <ul className="mt-3 space-y-2">
            {planned.map((item) => (
              <li key={item} className="font-mono text-[12px] leading-5 tracking-extra-tight text-black/45">
                {item}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </WellFigure>
  );
}
