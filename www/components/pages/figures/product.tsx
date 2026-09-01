"use client";

import { useState } from "react";
import { StatusPill } from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";
import { FigCmd, FigLabel, FigureFrame } from "./frame";
import {
  BypassSchematic,
  DiamondSchematic,
  IsoRings,
  IsoStack,
  IsoTwoPlanes,
  KeepBar,
  LockPlot,
} from "./draw";

export function POV01() {
  return (
    <FigureFrame id="P-OV-01">
      <IsoStack
        planes={[
          { label: "PLAN" },
          { label: "PROVISION" },
          { label: "RUN", accent: true },
          { label: "DESTROY" },
        ]}
      />
      <p className="mt-auto pt-2 font-mono text-[11px] tracking-extra-tight text-black/45">
        One run, then gone.
      </p>
    </FigureFrame>
  );
}

export function POV02({ rows }: { rows: { miss: string; have: string }[] }) {
  const fragments = ["Preview", "E2E", "Load", "Mirror"];
  const twin = ["State", "Contain", "Decide"];
  return (
    <FigureFrame id="P-OV-02">
      <div className="grid flex-1 grid-cols-2 gap-4">
        <div className="relative border border-black/10 p-3">
          <FigLabel>Fragments</FigLabel>
          <div className="mt-4 flex flex-wrap gap-2">
            {fragments.map((name) => (
              <span
                key={name}
                className="border border-black/15 px-2 py-1 font-mono text-[10px] tracking-extra-tight text-black/50"
              >
                {name}
              </span>
            ))}
          </div>
          <svg className="pointer-events-none absolute inset-6" viewBox="0 0 100 60" aria-hidden>
            <path d="M8 8 L92 52 M92 8 L8 52" stroke="rgba(0,0,0,0.28)" strokeWidth="1" />
          </svg>
        </div>
        <div className="border border-black/10 p-3">
          <FigLabel>Twin</FigLabel>
          <ul className="mt-4 space-y-2">
            {twin.map((name, i) => (
              <li key={name} className="flex items-center gap-2">
                <span className="size-1.5 bg-black" />
                <span className="font-mono text-[11px] tracking-extra-tight">{name}</span>
                {i < twin.length - 1 ? (
                  <span className="ml-auto font-mono text-[10px] text-black/30">↓</span>
                ) : (
                  <span className="ml-auto">
                    <StatusPill tone="FAIL">FAIL</StatusPill>
                  </span>
                )}
              </li>
            ))}
          </ul>
        </div>
      </div>
      <div className="mt-4 grid grid-cols-2 gap-x-4 border-t border-black/10 pt-3">
        <ul className="space-y-1">
          {rows.map((row) => (
            <li key={row.miss} className="font-mono text-[10px] tracking-extra-tight text-black/35">
              {row.miss}
            </li>
          ))}
        </ul>
        <ul className="space-y-1">
          {rows.map((row) => (
            <li key={row.have} className="font-mono text-[10px] tracking-extra-tight text-black/70">
              {row.have}
            </li>
          ))}
        </ul>
      </div>
    </FigureFrame>
  );
}

export function POV03() {
  return (
    <FigureFrame id="P-OV-03">
      <LockPlot peak={27.4} peakLabel="27.4s ACCESS EXCLUSIVE" tone="fail" />
    </FigureFrame>
  );
}

export function POV04() {
  const rows = [
    ["strongest lock", "ACCESS EXCLUSIVE 27.4s on subscriptions", true],
    ["blocked another", "yes, a session was seen waiting", false],
    ["table rewrite", "yes, reported by Postgres", false],
    ["plan change", "Index Scan to Seq Scan on events", false],
  ] as const;
  return (
    <FigureFrame id="P-OV-04">
      <div className="flex items-center justify-between gap-3">
        <span className="font-mono text-[11px] tracking-extra-tight text-black/55">
          af insights · migration rehearsal
        </span>
        <StatusPill tone="FAIL">FAIL</StatusPill>
      </div>
      <ul className="mt-4 space-y-2 border-t border-black/10 pt-3">
        {rows.map(([k, v, danger]) => (
          <li key={k} className="flex items-baseline justify-between gap-4">
            <span className="font-mono text-[11px] text-black/45">{k}</span>
            <span className={cn("font-mono text-[12px]", danger ? "text-red-700" : "text-black/70")}>
              {v}
            </span>
          </li>
        ))}
      </ul>
      <p className="mt-3 border-t border-black/10 pt-3 font-mono text-[11px] leading-5 text-[#285D49]">
        lint · add a second column of the new type, backfill it, then drop the old one
      </p>
    </FigureFrame>
  );
}

export function PTW01() {
  return (
    <FigureFrame id="P-TW-01">
      <IsoRings />
    </FigureFrame>
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
    <FigureFrame id="P-TW-02">
      <div className="flex items-center justify-between">
        <FigLabel>environment lifecycle</FigLabel>
        <StatusPill tone="PASS">env.destroyed</StatusPill>
      </div>
      <div className="mt-8 flex flex-1 items-end gap-1">
        {states.map((s) => (
          <div key={s.name} className="min-w-0 flex-1">
            <div
              className="h-1 w-full"
              style={{
                background:
                  s.tone === "pass" ? "#33bf00" : s.tone === "fail" ? "#C43D3D" : "rgba(0,0,0,0.12)",
              }}
            />
            <div className="mt-2 text-center font-mono text-[10px] uppercase leading-tight tracking-extra-tight text-black/55">
              {s.name.split(".")[1]}
            </div>
          </div>
        ))}
      </div>
    </FigureFrame>
  );
}

export function PTW03({
  items,
}: {
  items: readonly { kicker: string; title: string; body?: string }[];
}) {
  return (
    <FigureFrame id="P-TW-03">
      <div className="flex items-center justify-between">
        <FigLabel>isolation model</FigLabel>
        <StatusPill tone="FAIL">fail closed</StatusPill>
      </div>
      <ul className="mt-4 grid flex-1 grid-cols-2 gap-px bg-black/10 max-md:grid-cols-1">
        {items.map((item) => (
          <li key={item.kicker} className="bg-[#f7f7f5] p-3">
            <div className="font-mono text-[10px] uppercase tracking-[0.14em] text-black/40">{item.kicker}</div>
            <div className="mt-1 text-[13px] tracking-extra-tight text-black">{item.title}</div>
            {item.body ? (
              <p className="mt-1 text-[12px] leading-4 tracking-extra-tight text-black/45">{item.body}</p>
            ) : null}
          </li>
        ))}
      </ul>
    </FigureFrame>
  );
}

export function PTW04() {
  return (
    <FigureFrame id="P-TW-04">
      <FigLabel>where a run answers</FigLabel>
      <div className="mt-6 flex items-center gap-2 border border-black/12 bg-white px-3 py-2.5">
        <span className="size-2 rounded-full bg-[#33bf00]" aria-hidden />
        <span className="font-mono text-[13px] tabular-nums tracking-extra-tight">http://127.0.0.1:46000</span>
      </div>
      <div className="mt-auto flex flex-wrap gap-2 pt-6">
        {["af up", "af ci", "af down"].map((cmd) => (
          <FigCmd key={cmd}>$ {cmd}</FigCmd>
        ))}
      </div>
    </FigureFrame>
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
    <FigureFrame id="P-TW-05">
      <div className="flex items-center justify-between">
        <FigLabel>journal replay</FigLabel>
        <StatusPill tone="PASS">counted</StatusPill>
      </div>
      <ul className="mt-3 flex-1">
        {rows.map(([id, at]) => (
          <li
            key={id}
            className="flex items-baseline justify-between border-b border-black/8 py-1.5 font-mono text-[11px] tracking-extra-tight"
          >
            <span className="text-black/70">destroyed · {id}</span>
            <span className="tabular-nums text-black/40">t+{at}</span>
          </li>
        ))}
      </ul>
      <div className="mt-3 flex gap-4 font-mono text-[11px] text-[#285D49]">
        <span>14 removed</span>
        <span>0 left behind</span>
      </div>
    </FigureFrame>
  );
}

export function PSS01() {
  const rows = [
    ["email", "MASK", "3z8t…@example.test"],
    ["session", "DELETE", "deleted"],
    ["api_key", "HASH", "b6929ad97b7b"],
  ];
  return (
    <FigureFrame id="P-SS-01">
      <div className="flex items-center justify-between">
        <FigLabel>public.users</FigLabel>
        <span className="font-mono text-[10px] uppercase tracking-[0.14em] text-[#285D49]">unique</span>
      </div>
      <ul className="mt-4">
        {rows.map(([k, rule, v]) => (
          <li key={k} className="flex items-baseline justify-between gap-3 border-b border-black/8 py-2">
            <span className="font-mono text-[11px] text-black/45">{k}</span>
            <span className="font-mono text-[12px] text-black">{v}</span>
            <span
              className={cn(
                "font-mono text-[10px] uppercase tracking-[0.12em]",
                rule === "DELETE" ? "text-red-700" : "text-[#285D49]",
              )}
            >
              {rule}
            </span>
          </li>
        ))}
      </ul>
    </FigureFrame>
  );
}

export function PSS02() {
  return (
    <FigureFrame id="P-SS-02">
      <FigLabel>referential subset</FigLabel>
      <div className="mt-6">
        <KeepBar kept={0.12} />
      </div>
      <ul className="mt-6 space-y-2 font-mono text-[11px] tracking-extra-tight">
        <li className="flex justify-between text-[#285D49]">
          <span>u_8f2a parent kept</span>
          <span>o_441 follows</span>
        </li>
        <li className="flex justify-between text-black/40">
          <span>u_bb12 sampled out</span>
          <span>o_902 dropped</span>
        </li>
        <li className="flex justify-between text-red-700">
          <span>sessions *</span>
          <span>DELETE</span>
        </li>
      </ul>
    </FigureFrame>
  );
}

export function PSS03() {
  return (
    <FigureFrame id="P-SS-03">
      <IsoStack
        planes={[
          { label: "RESTORE" },
          { label: "SUBSET" },
          { label: "MASK", accent: true },
          { label: "DESTROY" },
        ]}
      />
      <p className="mt-auto pt-2 font-mono text-[11px] text-black/45">Postgres adapter · logical restore</p>
    </FigureFrame>
  );
}

export function PSS04() {
  return (
    <FigureFrame id="P-SS-04">
      <IsoTwoPlanes top="CONTROL PLANE" bottom="DATA PLANE" callout="ATTESTATION ONLY" />
      <div className="mt-auto flex justify-between font-mono text-[10px] uppercase tracking-[0.12em] text-black/40">
        <span>evidence · hashes</span>
        <span>snapshots · secrets</span>
      </div>
    </FigureFrame>
  );
}

export function PFW01() {
  return (
    <FigureFrame id="P-FW-01">
      <DiamondSchematic
        nodes={[
          { label: "SIM" },
          { label: "CAPTURE" },
          { label: "DENY", cmd: "$ deny" },
          { label: "LEDGER" },
        ]}
      />
    </FigureFrame>
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
  return (
    <FigureFrame id={id}>
      <div className="flex items-center justify-between">
        <FigLabel>{provider}</FigLabel>
        <StatusPill tone={tone}>{tone}</StatusPill>
      </div>
      <div className="mt-4 font-mono text-[13px] tracking-extra-tight">{op}</div>
      <p className="mt-2 text-[13px] leading-5 tracking-extra-tight text-black/50">{body}</p>
      <div className="mt-auto border-t border-black/10 pt-3 font-mono text-[11px] leading-5 text-black/55">
        {receipt}
        <div className="mt-2 uppercase tracking-[0.12em] text-black/35">{chip}</div>
      </div>
    </FigureFrame>
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
  return (
    <FigureFrame id="P-FW-05" dark>
      <div className="flex items-center justify-between gap-3">
        <span className="font-mono text-[11px] uppercase tracking-[0.14em] text-white/40">
          attempted-effect ledger
        </span>
        <span className="font-mono text-[11px] text-[#33bf00]">escaped 0</span>
      </div>
      <ul className="mt-3 flex-1 font-mono text-[11px] tracking-extra-tight">
        {LEDGER.map(([method, dest, action, receipt, tone]) => (
          <li
            key={receipt}
            className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-white/10 py-1.5"
          >
            <span className="w-16 shrink-0 text-white/35">{method}</span>
            <span className="min-w-0 flex-1 truncate text-white/80">{dest}</span>
            <span className={tone === "FAIL" ? "text-red-400" : "text-[#33bf00]"}>{action}</span>
            <span className="text-white/30">{receipt}</span>
          </li>
        ))}
      </ul>
    </FigureFrame>
  );
}

export function PFW06() {
  return (
    <FigureFrame id="P-FW-06">
      <div className="flex items-center justify-between">
        <FigLabel>bypass blocked</FigLabel>
        <StatusPill tone="FAIL">DENY</StatusPill>
      </div>
      <div className="mt-4 flex flex-1 flex-col items-center justify-center">
        <BypassSchematic />
        <p className="mt-2 font-mono text-[12px] tracking-extra-tight">TCP 18.4.2.9:443</p>
        <span className="mt-2 border border-red-700/40 px-2 py-0.5 font-mono text-[11px] text-red-700">
          ENETUNREACH
        </span>
      </div>
    </FigureFrame>
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

export function PLD01() {
  return (
    <FigureFrame id="P-LD-01">
      <div className="flex items-center justify-between">
        <FigLabel>af load</FigLabel>
        <span className="font-mono text-[11px] text-black/40">source otel · 17.8/s</span>
      </div>
      <ul className="mt-4 flex-1 font-mono text-[11px]">
        {ROUTES.map(({ route, share, p95, base, delta }) => (
          <li key={route} className="flex items-baseline justify-between gap-3 border-b border-black/8 py-1.5">
            <span className="min-w-0 truncate">{route}</span>
            <span className="text-black/40">{share}</span>
            <span>{p95}</span>
            <span className="hidden text-black/40 sm:inline">{base ?? "no base"}</span>
            <span
              className={cn(
                "tabular-nums",
                delta === null ? "text-black/30" : delta > 0.5 ? "text-red-700" : "text-[#285D49]",
              )}
            >
              {delta === null ? "no baseline" : `+${Math.round(delta * 100)}%`}
            </span>
          </li>
        ))}
      </ul>
      <p className="mt-3 font-mono text-[10px] uppercase tracking-[0.12em] text-black/35">
        refused · POST /billing/upgrade · POST /api/payments/intent
      </p>
    </FigureFrame>
  );
}

export function PLD02({ source }: { source: string }) {
  return (
    <FigureFrame id="P-LD-02" dark>
      <FigCmd dark>$ af load</FigCmd>
      <pre className="mt-3 flex-1 overflow-x-auto font-mono text-[12px] leading-5 tracking-extra-tight text-white/70">
        {source}
      </pre>
    </FigureFrame>
  );
}

export function PMG01({ captions }: { captions: readonly [string, string] }) {
  const [tab, setTab] = useState<0 | 1>(0);
  return (
    <div>
      <div className="mb-3 flex gap-2">
        {(["unsafe", "expand-and-contract"] as const).map((label, i) => (
          <button
            key={label}
            type="button"
            onClick={() => setTab(i as 0 | 1)}
            className={cn(
              "border px-2.5 py-1 font-mono text-[11px] tracking-extra-tight",
              tab === i ? "border-black bg-black text-white" : "border-black/15 text-black/50",
            )}
          >
            {label}
          </button>
        ))}
      </div>
      <FigureFrame id="P-MG-01">
        <FigLabel>{tab === 0 ? "unsafe schema migration" : "expand-and-contract"}</FigLabel>
        <div className="mt-4 flex-1">
          {tab === 0 ? (
            <LockPlot peak={27.4} peakLabel="27.4s ACCESS EXCLUSIVE" tone="fail" />
          ) : (
            <LockPlot peak={0.4} peakLabel="0.4s · nothing waiting" tone="pass" />
          )}
        </div>
      </FigureFrame>
      <p className="mt-4 max-w-[640px] text-[15px] leading-6 tracking-extra-tight text-black max-md:text-[14px]">
        {captions[tab]}
      </p>
    </div>
  );
}

export function PMG02() {
  return (
    <FigureFrame id="P-MG-02">
      <LockPlot peak={27.4} peakLabel="27.4s ACCESS EXCLUSIVE" tone="fail" />
    </FigureFrame>
  );
}

export function PMG03({ source }: { source: string }) {
  return (
    <FigureFrame id="P-MG-03" dark>
      <span className="font-mono text-[11px] uppercase tracking-[0.14em] text-white/40">af insights</span>
      <pre className="mt-3 flex-1 overflow-x-auto font-mono text-[12px] leading-5 text-white/70">{source}</pre>
    </FigureFrame>
  );
}

export function PMG04() {
  return (
    <FigureFrame id="P-MG-04">
      <LockPlot peak={0.4} peakLabel="0.4s · nothing waiting" tone="pass" />
    </FigureFrame>
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
    <FigureFrame id="P-RP-01">
      <div className="flex items-center justify-between">
        <span className="font-mono text-[11px] text-black/45">{pr}</span>
        <StatusPill tone={tone} />
      </div>
      <div className="mt-4 font-mono text-[13px] tracking-extra-tight">{title}</div>
      <p className="mt-2 font-mono text-[11px] leading-4 text-black/45">{evidence}</p>
      <p
        className={cn(
          "mt-auto pt-4 font-mono text-[10px] tracking-extra-tight",
          tone === "PASS" && "text-[#285D49]",
          tone === "FAIL" && "text-red-700",
          tone === "UNVERIFIED" && "text-black/45",
        )}
      >
        {merge}
      </p>
    </FigureFrame>
  );
}

export function PRP02() {
  return (
    <FigureFrame id="P-RP-02" dark>
      <div className="flex items-center justify-between gap-3">
        <span className="font-mono text-[12px] text-white/80">pr/184 · add access_tier</span>
        <span className="font-mono text-[11px] text-red-400">required · FAIL</span>
      </div>
      <p className="mt-4 font-mono text-[12px] text-white">1 workflow failed, and 1 invariant did not hold.</p>
      <p className="mt-1 font-mono text-[11px] text-white/45">
        Invariant `one_active_subscription` does not hold.
      </p>
      <table className="mt-4 w-full font-mono text-[11px] tabular-nums text-white/70">
        <thead>
          <tr className="text-white/35">
            <th className="py-1 text-left font-normal">account_id</th>
            <th className="py-1 text-left font-normal">active</th>
            <th className="py-1 text-left font-normal">latest</th>
          </tr>
        </thead>
        <tbody>
          {[
            ["acct_00418", "2", "sub_9c41"],
            ["acct_02277", "2", "sub_a180"],
            ["acct_09903", "3", "sub_b774"],
          ].map((row) => (
            <tr key={row[0]} className="border-t border-white/10">
              <td className="py-1">{row[0]}</td>
              <td className="py-1">{row[1]}</td>
              <td className="py-1">{row[2]}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="mt-auto flex items-center justify-between border-t border-white/10 pt-3">
        <span className="font-mono text-[11px] text-white/30">Merge pull request · inert</span>
        <span className="border border-white/20 px-2 py-0.5 font-mono text-[10px] text-white/35">Merge</span>
      </div>
    </FigureFrame>
  );
}

export function PAR01() {
  return (
    <FigureFrame id="P-AR-01">
      <IsoStack
        planes={[
          { label: "CONTROL PLANE" },
          { label: "SNAPSHOTS" },
          { label: "EGRESS", accent: true },
          { label: "CLEANUP" },
        ]}
      />
      <p className="mt-auto pt-2 font-mono text-[11px] text-black/45">outbound-only · bearer token over TLS</p>
    </FigureFrame>
  );
}

export function PAR02() {
  return (
    <FigureFrame id="P-AR-02">
      <IsoTwoPlanes top="EVIDENCE" bottom="RECORDS" callout="does not enter" />
      <div className="mt-auto flex justify-between font-mono text-[10px] uppercase tracking-[0.12em] text-black/40">
        <span>reports · sha256</span>
        <span>snapshots · secrets</span>
      </div>
    </FigureFrame>
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
    <FigureFrame id="P-AR-03">
      <div className="grid flex-1 grid-cols-2 gap-4 max-md:grid-cols-1">
        <div>
          <FigLabel>in force today</FigLabel>
          <ul className="mt-3 space-y-2">
            {inForce.map((item) => (
              <li key={item} className="font-mono text-[12px] leading-5 tracking-extra-tight text-black">
                {item}
              </li>
            ))}
          </ul>
        </div>
        <div>
          <FigLabel>designed, not built</FigLabel>
          <ul className="mt-3 space-y-2">
            {planned.map((item) => (
              <li key={item} className="font-mono text-[12px] leading-5 tracking-extra-tight text-black/45">
                {item}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </FigureFrame>
  );
}
