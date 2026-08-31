"use client";

import { useRef, useState } from "react";
import { cn } from "@/lib/cn";
import { clamp, EASE_OUT_CUBIC, lerp } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import {
  CheckRow,
  FILM_EASE,
  Hairline,
  MonoLabel,
  Node,
  Panel,
  QueueChip,
  Receipt,
  Sparkline,
  StatusPill,
  Ticker,
  Timestamp,
} from "./primitives";

const DUR = 17;
const REDUCED_T = 12.18;
const MS_CHAR = 0.014;
const W = 1184;
const H = 580;

const SLOT = { x: 126, y: 196 };
const GW_X = 322;

const RULES = [
  { at: 0.72, key: "stripe", label: "stripe:mock" },
  { at: 0.88, key: "sendgrid", label: "sendgrid:capture" },
  { at: 1.04, key: "slack", label: "slack:capture" },
  { at: 1.2, key: "sandbox", label: "auth0:sandbox" },
  { at: 1.36, key: "deny", label: "*:block" },
] as const;

type HitKind = "bend" | "stamp" | "fragment" | "rewrite";

type PacketDef = {
  id: string;
  method: string;
  path: string;
  host: string;
  suffix: string;
  extra?: string;
  t0: number;
  travel: number;
  hitDur: number;
  kind: HitKind;
  rule: (typeof RULES)[number]["key"];
  rest: { x: number; y: number };
  ctrl: { x: number; y: number };
  hit: { x: number; y: number };
  bendCtrl?: { x: number; y: number };
  rewriteAt?: number;
};

const PACKETS: PacketDef[] = [
  {
    id: "stripe1",
    method: "POST",
    path: "/v1/charges",
    host: "api.stripe.com",
    suffix: "08f2",
    extra: "$49.00",
    t0: 1.5,
    travel: 0.56,
    hitDur: 0.12,
    kind: "bend",
    rule: "stripe",
    hit: { x: GW_X, y: 154 },
    ctrl: { x: 214, y: 168 },
    bendCtrl: { x: 214, y: 292 },
    rest: { x: 118, y: 438 },
  },
  {
    id: "stripe2",
    method: "POST",
    path: "/v1/charges",
    host: "api.stripe.com",
    suffix: "08f3",
    extra: "$49.00",
    t0: 1.8,
    travel: 0.56,
    hitDur: 0.12,
    kind: "bend",
    rule: "stripe",
    hit: { x: GW_X, y: 162 },
    ctrl: { x: 220, y: 176 },
    bendCtrl: { x: 228, y: 308 },
    rest: { x: 154, y: 452 },
  },
  {
    id: "sendgrid",
    method: "POST",
    path: "/v3/mail/send",
    host: "api.sendgrid.com",
    suffix: "2a91",
    t0: 4.0,
    travel: 0.52,
    hitDur: 0.1,
    kind: "stamp",
    rule: "sendgrid",
    hit: { x: GW_X, y: 198 },
    ctrl: { x: 216, y: 198 },
    rest: { x: 268, y: 412 },
  },
  {
    id: "slack",
    method: "POST",
    path: "/hooks",
    host: "hooks.slack.com",
    suffix: "91c0",
    t0: 6.2,
    travel: 0.5,
    hitDur: 0.09,
    kind: "bend",
    rule: "slack",
    hit: { x: GW_X, y: 232 },
    ctrl: { x: 214, y: 220 },
    bendCtrl: { x: 360, y: 320 },
    rest: { x: 408, y: 448 },
  },
  {
    // Sandbox mode, not a bucket rewrite. Nothing in the engine rewrites an S3
    // bucket name; *.amazonaws.com is classified as SES and captured. What the
    // proxy really does with a rewrite shape is swap a placeholder credential
    // for the provider's sandbox key on the way out, so the application never
    // holds a working key and the mistake cannot be made.
    id: "sandbox",
    method: "POST",
    path: "sk_live_…",
    host: "tenant.auth0.com",
    suffix: "sbx1",
    t0: 8.0,
    travel: 0.72,
    hitDur: 0.14,
    kind: "rewrite",
    rule: "sandbox",
    rewriteAt: 0.42,
    hit: { x: GW_X, y: 186 },
    ctrl: { x: 228, y: 176 },
    bendCtrl: { x: 300, y: 360 },
    rest: { x: 268, y: 512 },
  },
  {
    id: "prod",
    method: "GET",
    path: "/v1/health",
    host: "api.prod.internal",
    suffix: "prd1",
    t0: 10.2,
    travel: 0.48,
    hitDur: 0.11,
    kind: "fragment",
    rule: "deny",
    hit: { x: GW_X, y: 128 },
    ctrl: { x: 240, y: 112 },
    rest: { x: GW_X, y: 128 },
  },
  {
    // A CONNECT to an unlisted name, not a raw address. A direct-IP connection
    // never arrives at the gateway: the twin has no route to a public address,
    // so the packet fails at the network and there is no decision to draw. It
    // is blocked more strongly than this scene can show, and it produces no
    // decision-log line, which is what the scene was drawing.
    id: "tcp",
    method: "CONNECT",
    path: ":443",
    host: "example.com",
    suffix: "tcp9",
    t0: 10.85,
    travel: 0.42,
    hitDur: 0.08,
    kind: "fragment",
    rule: "deny",
    hit: { x: GW_X, y: 96 },
    ctrl: { x: 250, y: 64 },
    rest: { x: GW_X, y: 96 },
  },
  {
    id: "unknown",
    method: "POST",
    path: "/v1/ingest",
    host: "telemetry.unknown.example",
    suffix: "unk0",
    t0: 12.4,
    travel: 0.46,
    hitDur: 0.1,
    kind: "fragment",
    rule: "deny",
    hit: { x: GW_X, y: 108 },
    ctrl: { x: 236, y: 88 },
    rest: { x: GW_X, y: 108 },
  },
];

type LedgerRow = {
  id: string;
  typeStart: number;
  body: string;
  receipt: string;
  tone: "ok" | "block" | "store";
  critical?: boolean;
};

const LEDGER: LedgerRow[] = [
  {
    id: "stripe1",
    typeStart: 2.18,
    body: "POST /v1/charges  $49.00  mock  clone-local  cus_sim_11",
    receipt: "ch_sim_08f2",
    tone: "ok",
  },
  {
    id: "stripe2",
    typeStart: 2.48,
    body: "POST /v1/charges  $49.00  mock  clone-local  cus_sim_11",
    receipt: "ch_sim_08f3",
    tone: "ok",
  },
  {
    id: "sendgrid",
    typeStart: 4.62,
    body: "POST /v3/mail/send  capture  never-delivered  from:alex@***",
    receipt: "msg_sim_2a",
    tone: "store",
  },
  {
    id: "slack",
    typeStart: 6.82,
    body: "POST hooks.slack.com  capture  recorded, never posted",
    receipt: "evt_sim_91",
    tone: "store",
  },
  {
    id: "sandbox",
    typeStart: 8.92,
    body: "POST tenant.auth0.com  sandbox  key swapped on the way out",
    receipt: "req_sbx_44",
    tone: "ok",
  },
  {
    id: "prod",
    typeStart: 10.78,
    body: "GET api.prod.internal/v1/health  BLOCK  production-host",
    receipt: "deny_01",
    tone: "block",
    critical: true,
  },
  {
    id: "tcp",
    typeStart: 11.38,
    body: "CONNECT example.com:443  DENY  default",
    receipt: "deny_02",
    tone: "block",
    critical: true,
  },
];

const SHARDS = [
  { x: -16, y: -9, r: -22, w: 22, h: 8 },
  { x: 14, y: -11, r: 16, w: 18, h: 7 },
  { x: -10, y: 12, r: 9, w: 16, h: 7 },
  { x: 12, y: 11, r: -18, w: 20, h: 8 },
];

type Pt = { x: number; y: number };

function bezierX(t: number, a: number, b: number) {
  return (((1 - 3 * b + 3 * a) * t + (3 * b - 6 * a)) * t + 3 * a) * t;
}

function bezierDX(t: number, a: number, b: number) {
  return 3 * (1 - 3 * b + 3 * a) * t * t + 2 * (3 * b - 6 * a) * t + 3 * a;
}

function filmEase(t: number) {
  const x = clamp(t);
  if (x <= 0) return 0;
  if (x >= 1) return 1;
  let u = x;
  for (let i = 0; i < 7; i++) {
    const d = bezierDX(u, 0.16, 0.3);
    if (Math.abs(d) < 1e-6) break;
    u -= (bezierX(u, 0.16, 0.3) - x) / d;
  }
  u = clamp(u);
  return bezierX(u, 1, 1);
}

function span(t: number, a: number, b: number) {
  if (b <= a) return t >= a ? 1 : 0;
  return clamp((t - a) / (b - a));
}

function quad(p0: Pt, p1: Pt, p2: Pt, t: number): Pt {
  const u = 1 - t;
  return {
    x: u * u * p0.x + 2 * u * t * p1.x + t * t * p2.x,
    y: u * u * p0.y + 2 * u * t * p1.y + t * t * p2.y,
  };
}

function typed(full: string, t: number, start: number) {
  if (t < start) return "";
  const n = Math.floor((t - start) / MS_CHAR);
  return full.slice(0, Math.min(full.length, n));
}

function typedDone(full: string, t: number, start: number) {
  return t >= start + full.length * MS_CHAR;
}

function qPath(p0: Pt, p1: Pt, p2: Pt) {
  return `M ${p0.x} ${p0.y} Q ${p1.x} ${p1.y} ${p2.x} ${p2.y}`;
}

function trailPath(def: PacketDef, live: LivePacket) {
  if (live.pathT >= 1 && def.bendCtrl) {
    return `${qPath(SLOT, def.ctrl, def.hit)} Q ${def.bendCtrl.x} ${def.bendCtrl.y} ${live.x} ${live.y}`;
  }
  return qPath(SLOT, def.ctrl, def.hit);
}

type LivePacket = {
  def: PacketDef;
  x: number;
  y: number;
  opacity: number;
  rot: number;
  visible: boolean;
  pathT: number;
  hitT: number;
  rewrite: number;
  strike: number;
  fragment: number;
  path: string;
  dash: number;
};

function livePacket(t: number, def: PacketDef): LivePacket {
  const rewind = span(t, 15.35, 16.92);
  const rewindE = filmEase(rewind);
  const local = t - def.t0;
  const hidden: LivePacket = {
    def,
    x: SLOT.x,
    y: SLOT.y,
    opacity: 0,
    rot: 0,
    visible: false,
    pathT: 0,
    hitT: 0,
    rewrite: 0,
    strike: 0,
    fragment: 0,
    path: qPath(SLOT, def.ctrl, def.hit),
    dash: 0,
  };
  if (t < def.t0 && rewind <= 0) return hidden;

  const hitT = clamp((local - def.travel) / def.hitDur);
  const hitE = EASE_OUT_CUBIC(hitT);
  const travelT = clamp(local / def.travel);
  const travelE = filmEase(travelT);

  let x = SLOT.x;
  let y = SLOT.y;
  let opacity = 1;
  let rot = 0;
  let pathT = travelE;
  let rewrite = 0;
  let strike = 0;
  let fragment = 0;
  let path = qPath(SLOT, def.ctrl, def.hit);

  if (local >= 0 && local < def.travel) {
    const p = quad(SLOT, def.ctrl, def.hit, travelE);
    x = p.x;
    y = p.y;
    if (def.kind === "rewrite" && def.rewriteAt != null) {
      rewrite = EASE_OUT_CUBIC(span(travelT, def.rewriteAt, def.rewriteAt + 0.18));
    }
  } else if (local >= def.travel) {
    pathT = 1;
    if (def.kind === "bend" && def.bendCtrl) {
      const b = filmEase(clamp((local - def.travel) / 0.42));
      const p = quad(def.hit, def.bendCtrl, def.rest, b);
      x = p.x;
      y = p.y;
      path = qPath(SLOT, def.ctrl, def.hit) + ` Q ${def.bendCtrl.x} ${def.bendCtrl.y} ${p.x} ${p.y}`;
      rot = lerp(0, def.id === "slack" ? 6 : -8, b);
      if (def.rule === "stripe") rot = lerp(0, 12, hitE);
    } else if (def.kind === "stamp") {
      const p = quad(def.hit, { x: lerp(def.hit.x, def.rest.x, 0.5), y: def.hit.y + 40 }, def.rest, filmEase(clamp((local - def.travel) / 0.38)));
      x = p.x;
      y = p.y;
      strike = hitE;
      rot = lerp(0, -3, hitE);
    } else if (def.kind === "rewrite" && def.bendCtrl) {
      rewrite = 1;
      const b = filmEase(clamp((local - def.travel) / 0.4));
      const p = quad(def.hit, def.bendCtrl, def.rest, b);
      x = p.x;
      y = p.y;
    } else if (def.kind === "fragment") {
      x = def.hit.x;
      y = def.hit.y;
      fragment = hitE;
      opacity = 1 - hitE * 0.15;
      if (local > def.travel + def.hitDur + 0.35) opacity = clamp(1 - (local - def.travel - def.hitDur - 0.35) / 0.4);
    }
  }

  if (rewindE > 0 && local > def.travel) {
    const from = { x, y };
    x = lerp(from.x, SLOT.x, rewindE);
    y = lerp(from.y, SLOT.y, rewindE);
    opacity = 1 - rewindE * 0.15;
    fragment = fragment * (1 - rewindE);
    strike = strike * (1 - rewindE);
    rot = rot * (1 - rewindE);
    if (rewindE > 0.92) opacity = (1 - rewindE) / 0.08;
  }

  if (t >= 16.95) return hidden;

  const dash = (t * 42) % 24;
  return {
    def,
    x,
    y,
    opacity,
    rot,
    visible: opacity > 0.02 && (local >= 0 || rewindE > 0),
    pathT,
    hitT: hitE,
    rewrite,
    strike,
    fragment,
    path,
    dash,
  };
}

function PacketCapsule({ live }: { live: LivePacket }) {
  const { def, x, y, opacity, rot, rewrite, strike, fragment } = live;
  const rewritten = rewrite > 0.55;
  const pathLabel = def.id === "sandbox" && rewritten ? "sk_test_…" : def.path;
  const hostLabel = def.host.length > 18 ? `${def.host.slice(0, 16)}…` : def.host;

  return (
    <g transform={`translate(${x} ${y}) rotate(${rot})`} opacity={opacity} style={{ transition: "none" }}>
      {fragment > 0
        ? SHARDS.map((s, i) => (
            <rect
              key={i}
              x={-s.w / 2 + s.x * fragment}
              y={-s.h / 2 + s.y * fragment}
              width={s.w}
              height={s.h}
              rx="1"
              fill="#fff"
              stroke={def.kind === "fragment" ? "#dc2626" : "rgba(0,0,0,0.28)"}
              strokeWidth="1"
              opacity={1 - fragment * 0.85}
              transform={`rotate(${s.r * fragment})`}
            />
          ))
        : null}
      {fragment < 0.72 ? (
        <g opacity={1 - fragment}>
          <clipPath id={`fw-cap-${def.id}`}>
            <rect x={-32} y={-9} width={64} height={18} rx={9} />
          </clipPath>
          <g clipPath={`url(#fw-cap-${def.id})`}>
          <rect
            x={-32}
            y={-9}
            width={64}
            height={18}
            rx={9}
            fill="#fff"
            stroke={def.kind === "fragment" && live.hitT > 0 ? "#dc2626" : "rgba(0,0,0,0.28)"}
            strokeWidth="1"
          />
          <text
            x={-26}
            y={-1.2}
            fill="#111"
            fontSize={5.5}
            fontFamily="var(--font-mono), ui-monospace, monospace"
            letterSpacing="-0.04em"
          >
            {def.method} {pathLabel}
          </text>
          <text
            x={-26}
            y={6.2}
            fill="rgba(0,0,0,0.55)"
            fontSize={5}
            fontFamily="var(--font-mono), ui-monospace, monospace"
            letterSpacing="-0.03em"
          >
            {hostLabel}
            {def.extra ? `  ${def.extra}` : ""}
          </text>
          <text
            x={30}
            y={-1.2}
            textAnchor="end"
            fill="rgba(0,0,0,0.4)"
            fontSize={8}
            fontFamily="var(--font-mono), ui-monospace, monospace"
            letterSpacing="-0.08em"
          >
            {def.suffix}
          </text>
          {def.id === "sandbox" && rewrite > 0 && !rewritten ? (
            <line
              x1={-8}
              y1={-2.4}
              x2={lerp(-8, 18, rewrite)}
              y2={-2.4}
              stroke="#111"
              strokeWidth="1"
            />
          ) : null}
          {strike > 0 ? (
            <line
              x1={-28}
              y1={0}
              x2={lerp(-28, 28, strike)}
              y2={0}
              stroke="#111"
              strokeWidth="1.2"
            />
          ) : null}
          </g>
        </g>
      ) : null}
    </g>
  );
}

function Trail({ live }: { live: LivePacket }) {
  if (!live.visible || live.fragment > 0.8) return null;
  const def = live.def;
  const drawn = live.pathT >= 1 && def.bendCtrl ? 1 : clamp(live.pathT);
  if (drawn <= 0.02) return null;
  const d = trailPath(def, live);
  const stroke = def.kind === "fragment" ? "rgba(220,38,38,0.55)" : "rgba(0,0,0,0.32)";
  const maskId = `fw-trail-${def.id}`;
  return (
    <g>
      <mask id={maskId}>
        <path d={d} fill="none" stroke="#fff" strokeWidth="3" pathLength={1} strokeDasharray={`${drawn} 1`} />
      </mask>
      <path
        d={d}
        fill="none"
        stroke={stroke}
        strokeWidth="1"
        pathLength={1}
        strokeDasharray={`${drawn} 1`}
        opacity={0.35}
        vectorEffect="non-scaling-stroke"
      />
      <path
        d={d}
        fill="none"
        stroke={stroke}
        strokeWidth="1"
        strokeDasharray="2.5 3.5"
        strokeDashoffset={-live.dash}
        mask={`url(#${maskId})`}
        vectorEffect="non-scaling-stroke"
      />
    </g>
  );
}

export function FirewallScene() {
  const ref = useRef<HTMLDivElement>(null);
  const { idle, reduced, story } = useInViewPlay(ref, 0.18);
  const [t, setT] = useState(0);

  usePausedRaf(idle, (_now, elapsed) => {
    setT((elapsed / 1000) % DUR);
  });

  const clock = reduced ? REDUCED_T : idle || story ? t : 0;
  const lives = PACKETS.map((p) => livePacket(clock, p));
  const spine = filmEase(span(clock, 0.42, 1.18));
  const bound = filmEase(span(clock, 0.04, 0.55));
  const netErase = filmEase(span(clock, 0.28, 0.92));
  const egressLabel = filmEase(span(clock, 0.62, 1.05));
  const cpuOn = clock >= 2.1;
  const dnsOn = clock >= 4.4;
  const overlayIn = filmEase(span(clock, 12.88, 13.05));
  const overlayHold = clock >= 12.88 && clock < 13.68;
  const overlayOut = 1 - filmEase(span(clock, 13.68, 14.72));
  const overlay = overlayHold ? 1 : clock < 13.68 ? overlayIn : overlayOut;
  const expand = filmEase(span(clock, 14.82, 15.12));
  const tcpHit = 10.85 + 0.42;
  const tcpFrame = Math.floor((clock - tcpHit) * 24);
  const denyFlash = tcpFrame === 0 || tcpFrame === 1;
  const click1 = span(clock, 1.5, 1.68);
  const click2 = span(clock, 1.8, 1.98);
  const caretOn = (clock % 1.05) < 0.52;
  const dupNote = typedDone(LEDGER[1].body, clock, LEDGER[1].typeStart) && clock >= 2.92 && clock < 14.8;
  const mimeOn = clock >= 4.62 && clock < 15.2;
  const mimeStrike = EASE_OUT_CUBIC(span(clock, 4.7, 4.82));
  const slackGhost = filmEase(span(clock, 6.78, 7.15));
  const crit = LEDGER.filter((r) => r.critical && clock >= r.typeStart).length;
  const escaped = 0;
  const rowCount = LEDGER.filter((r) => clock >= r.typeStart).length;
  const activeRule = (() => {
    for (const live of lives) {
      if (!live.visible) continue;
      const local = clock - live.def.t0;
      if (local >= live.def.travel && local < live.def.travel + 0.28) return live.def.rule;
    }
    return denyFlash ? "deny" : null;
  })();

  return (
    <div ref={ref} className="relative w-full">
      <Panel className="overflow-hidden max-xl:hidden">
        <div className="pointer-events-none relative select-none" aria-hidden>
          <svg viewBox={`0 0 ${W} ${H}`} className="block h-auto w-full" aria-hidden>
            <rect x="0.5" y="0.5" width={W - 1} height={H - 1} fill="none" stroke="rgba(0,0,0,0.06)" />

            <text
              x="16"
              y="20"
              fill="rgba(0,0,0,0.45)"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              letterSpacing="0.14em"
            >
              SIDE-EFFECT FIREWALL
            </text>
            <text
              x="560"
              y="20"
              fill="rgba(0,0,0,0.4)"
              fontSize="11"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              className="tabular-nums"
            >
              {clock.toFixed(1)}s / {DUR.toFixed(1)}s
            </text>
            <text
              x="1168"
              y="20"
              textAnchor="end"
              fill={overlay > 0.4 ? "#b91c1c" : "#285D49"}
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              letterSpacing="0.12em"
            >
              FAIL CLOSED
            </text>
            <line x1="16" y1="28" x2="1168" y2="28" stroke="rgba(0,0,0,0.1)" strokeWidth="1" />

            <rect
              x="16"
              y="40"
              width="216"
              height="300"
              rx="2"
              fill="#f7f7f5"
              stroke="rgba(0,0,0,0.22)"
              strokeWidth="1.4"
              strokeDasharray="540"
              strokeDashoffset={540 * (1 - bound)}
              opacity={0.35 + bound * 0.65}
            />
            <text
              x="28"
              y="58"
              fill="rgba(0,0,0,0.45)"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              letterSpacing="0.1em"
            >
              TWIN
            </text>
            <rect x="28" y="70" width="192" height="252" rx="2" fill="#fff" stroke="rgba(0,0,0,0.1)" />
            <text
              x="40"
              y="90"
              fill="rgba(0,0,0,0.4)"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
            >
              checkout · app
            </text>
            <text
              x="40"
              y="128"
              fill="#111"
              fontSize="13"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              className="tabular-nums"
            >
              $49.00
            </text>
            <text
              x="40"
              y="146"
              fill="rgba(0,0,0,0.45)"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
            >
              card · cus_sim_11
            </text>
            <rect x="40" y="168" width="88" height="22" rx="2" fill="#111" />
            <text
              x="84"
              y="183"
              textAnchor="middle"
              fill="#f7f7f5"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
            >
              charge
            </text>
            {click1 > 0 && click1 < 1 ? (
              <rect
                x={40 - 4 * click1}
                y={168 - 4 * click1}
                width={88 + 8 * click1}
                height={22 + 8 * click1}
                rx="2"
                fill="none"
                stroke="rgba(0,0,0,0.35)"
                opacity={1 - click1}
              />
            ) : null}
            {click2 > 0 && click2 < 1 ? (
              <rect
                x={40 - 4 * click2}
                y={168 - 4 * click2}
                width={88 + 8 * click2}
                height={22 + 8 * click2}
                rx="2"
                fill="none"
                stroke="rgba(0,0,0,0.35)"
                opacity={1 - click2}
              />
            ) : null}
            <rect
              x="118"
              y="188"
              width="12"
              height="16"
              rx="1"
              fill="none"
              stroke="rgba(0,0,0,0.2)"
              strokeDasharray="2 2"
            />
            <text
              x="40"
              y="230"
              fill="rgba(0,0,0,0.35)"
              fontSize="9"
              fontFamily="var(--font-mono), ui-monospace, monospace"
            >
              egress slot
            </text>
            {dnsOn ? (
              <g>
                <rect x="40" y="292" width="168" height="18" rx="2" fill="#E4F1EB" stroke="rgba(40,93,73,0.35)" />
                <text
                  x="50"
                  y="305"
                  fill="#285D49"
                  fontSize="10"
                  fontFamily="var(--font-mono), ui-monospace, monospace"
                >
                  DNS · clone-local
                </text>
              </g>
            ) : (
              <text
                x="40"
                y="305"
                fill="rgba(0,0,0,0.28)"
                fontSize="10"
                fontFamily="var(--font-mono), ui-monospace, monospace"
              >
                resolver waiting
              </text>
            )}

            <line
              x1={GW_X}
              y1={40}
              x2={GW_X}
              y2={40 + 318 * spine}
              stroke="#111"
              strokeWidth="2"
              vectorEffect="non-scaling-stroke"
            />
            <rect x={GW_X - 5} y={38} width="10" height="6" fill="#111" opacity={spine} />
            <text
              x={GW_X + 14}
              y="52"
              fill="rgba(0,0,0,0.4)"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              letterSpacing="0.12em"
              opacity={spine}
            >
              GATEWAY
            </text>
            {RULES.map((rule, i) => {
              const on = filmEase(span(clock, rule.at, rule.at + 0.22));
              const lit = activeRule === rule.key;
              const denyLit = rule.key === "deny" && (denyFlash || activeRule === "deny");
              return (
                <g key={rule.key} opacity={on} transform={`translate(0 ${ (1 - on) * 6 })`}>
                  <rect
                    x={GW_X + 12}
                    y={64 + i * 22}
                    width="168"
                    height="18"
                    rx="1"
                    fill={denyLit ? "rgba(220,38,38,0.12)" : lit ? "rgba(51,191,0,0.1)" : "transparent"}
                    stroke={denyLit ? "#dc2626" : lit ? "rgba(51,191,0,0.55)" : "rgba(0,0,0,0.08)"}
                  />
                  <text
                    x={GW_X + 20}
                    y={77 + i * 22}
                    fill={denyLit ? "#b91c1c" : lit ? "#285D49" : "rgba(0,0,0,0.72)"}
                    fontSize="11"
                    fontFamily="var(--font-mono), ui-monospace, monospace"
                  >
                    {rule.label}
                  </text>
                </g>
              );
            })}
            {cpuOn ? (
              <g>
                <text
                  x={GW_X + 12}
                  y="186"
                  fill="rgba(0,0,0,0.4)"
                  fontSize="10"
                  fontFamily="var(--font-mono), ui-monospace, monospace"
                >
                  cpu  2%
                </text>
              </g>
            ) : null}

            <line
              x1={GW_X}
              y1="108"
              x2={700}
              y2="108"
              stroke="rgba(0,0,0,0.28)"
              strokeWidth="1"
              strokeDasharray="5 4"
              strokeDashoffset={netErase * 220}
              opacity={1 - netErase}
              vectorEffect="non-scaling-stroke"
            />
            <text
              x="430"
              y="100"
              fill="rgba(0,0,0,0.35)"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              opacity={1 - netErase}
            >
              default public egress
            </text>
            <text
              x="348"
              y="214"
              fill="#111"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              opacity={egressLabel}
            >
              no default public egress
            </text>

            <rect
              x="528"
              y="40"
              width="168"
              height="196"
              rx="2"
              fill="rgba(0,0,0,0.035)"
              stroke="rgba(0,0,0,0.16)"
            />
            <text
              x="612"
              y="62"
              textAnchor="middle"
              fill="rgba(0,0,0,0.28)"
              fontSize="11"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              letterSpacing="0.16em"
            >
              PRODUCTION
            </text>
            <text
              x="612"
              y="78"
              textAnchor="middle"
              fill="rgba(0,0,0,0.22)"
              fontSize="18"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              letterSpacing="0.28em"
            >
              VOID
            </text>
            <rect
              x="544"
              y="96"
              width="136"
              height="36"
              rx="2"
              fill="rgba(247,247,245,0.6)"
              stroke="#dc2626"
              strokeWidth="1"
            />
            <text
              x="612"
              y="112"
              textAnchor="middle"
              fill="#b91c1c"
              fontSize="9"
              fontFamily="var(--font-mono), ui-monospace, monospace"
            >
              api.stripe.com · live
            </text>
            <text
              x="612"
              y="126"
              textAnchor="middle"
              fill="rgba(185,28,28,0.7)"
              fontSize="8"
              fontFamily="var(--font-mono), ui-monospace, monospace"
            >
              no packet enters
            </text>
            <text
              x="544"
              y="154"
              fill="rgba(0,0,0,0.28)"
              fontSize="9"
              fontFamily="var(--font-mono), ui-monospace, monospace"
            >
              api.prod.internal
            </text>
            <text
              x="544"
              y="168"
              fill="rgba(0,0,0,0.28)"
              fontSize="9"
              fontFamily="var(--font-mono), ui-monospace, monospace"
            >
              example.com:443
            </text>
            <text
              x="544"
              y="204"
              fill="rgba(0,0,0,0.22)"
              fontSize="9"
              fontFamily="var(--font-mono), ui-monospace, monospace"
            >
              greyed · unreachable
            </text>

            <rect x="16" y="356" width="680" height="208" rx="2" fill="#f7f7f5" stroke="rgba(0,0,0,0.1)" />
            <text
              x="28"
              y="376"
              fill="rgba(0,0,0,0.4)"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              letterSpacing="0.12em"
            >
              SIMULATORS
            </text>

            <rect x="28" y="388" width="150" height="72" rx="2" fill="#fff" stroke="rgba(0,0,0,0.12)" />
            <text x="40" y="408" fill="rgba(0,0,0,0.45)" fontSize="10" fontFamily="var(--font-mono), ui-monospace, monospace">
              stripe · mock
            </text>
            <text x="40" y="428" fill="#111" fontSize="11" fontFamily="var(--font-mono), ui-monospace, monospace">
              clone-local ledger
            </text>
            <text x="40" y="446" fill="rgba(0,0,0,0.4)" fontSize="10" fontFamily="var(--font-mono), ui-monospace, monospace">
              not live
            </text>

            <rect x="190" y="388" width="196" height="96" rx="2" fill="#fff" stroke="rgba(0,0,0,0.12)" />
            <text x="202" y="408" fill="rgba(0,0,0,0.45)" fontSize="10" fontFamily="var(--font-mono), ui-monospace, monospace">
              sendgrid · capture
            </text>

            <rect
              x="400"
              y="388"
              width="160"
              height="72"
              rx="2"
              fill="#fff"
              stroke="rgba(0,0,0,0.1)"
              strokeDasharray="3 3"
              opacity={0.35 + slackGhost * 0.65}
            />
            <text
              x="412"
              y="408"
              fill="rgba(0,0,0,0.45)"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              opacity={0.4 + slackGhost * 0.6}
            >
              slack · captured
            </text>
            <text
              x="412"
              y="428"
              fill="rgba(0,0,0,0.4)"
              fontSize="10"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              opacity={slackGhost}
            >
              preview only
            </text>
            <text
              x="412"
              y="444"
              fill="rgba(0,0,0,0.35)"
              fontSize="9"
              fontFamily="var(--font-mono), ui-monospace, monospace"
              opacity={slackGhost}
            >
              no chat surface
            </text>

            <rect x="190" y="496" width="196" height="52" rx="2" fill="#fff" stroke="rgba(0,0,0,0.12)" />
            <text x="202" y="516" fill="rgba(0,0,0,0.45)" fontSize="10" fontFamily="var(--font-mono), ui-monospace, monospace">
              auth0 · sandbox
            </text>
            <text x="202" y="534" fill="#111" fontSize="10" fontFamily="var(--font-mono), ui-monospace, monospace">
              <tspan fill="rgba(0,0,0,0.35)" style={{ textDecoration: clock >= 8.55 ? "line-through" : "none" }}>
                sk_live_…
              </tspan>
              <tspan fill="#111">{clock >= 8.62 ? "  sk_test_…" : ""}</tspan>
            </text>

            {lives.map((live) => (
              <Trail key={`${live.def.id}-trail`} live={live} />
            ))}
            {lives.map((live) => (live.visible ? <PacketCapsule key={live.def.id} live={live} /> : null))}
          </svg>

          <div className="absolute top-[32px] right-[16px] bottom-[16px] w-[38.4%] min-w-0">
            <div className="flex h-full flex-col bg-white ring-1 ring-black/10">
              <div className="flex items-center justify-between px-3 py-2">
                <MonoLabel>ATTEMPTED-EFFECT LEDGER</MonoLabel>
                <Timestamp value={`${clock.toFixed(1)}s`} />
              </div>
              <Hairline />
              <div className="flex items-center gap-2 px-3 py-1.5">
                  <span className="relative flex min-w-0 flex-1 items-center bg-[#f4f7f5] px-2 py-1 ring-1 ring-black/10">
                  <MonoLabel>filter</MonoLabel>
                  <span
                    className="ml-2 inline-block h-3 w-px bg-black/70"
                    style={{ opacity: caretOn ? 1 : 0 }}
                  />
                </span>
                <StatusPill tone={crit > 0 ? "FAIL" : "PASS"}>{crit > 0 ? "DENY" : "PASS"}</StatusPill>
              </div>
              <Hairline />
              <div className="flex items-center gap-4 px-3 py-1.5 tabular-nums">
                <span className="flex items-center gap-1.5">
                  <MonoLabel>rows</MonoLabel>
                  <Ticker className="text-[12px]" value={rowCount} />
                </span>
                <span className="flex items-center gap-1.5">
                  <MonoLabel>escaped</MonoLabel>
                  <Ticker className="text-[12px] text-[#285D49]" value={escaped} />
                </span>
                <span className="flex items-center gap-1.5">
                  <MonoLabel>critical</MonoLabel>
                  <Ticker className={cn("text-[12px]", crit > 0 ? "text-red-700" : "text-black/50")} value={crit} />
                </span>
              </div>
              <Hairline />
              <div className="min-h-0 flex-1 overflow-hidden px-2 py-1.5">
                {LEDGER.map((row, i) => {
                  const body = typed(row.body, clock, row.typeStart);
                  if (!body && clock < row.typeStart) return null;
                  const done = typedDone(row.body, clock, row.typeStart);
                  const isExpand = i === 0 && expand > 0;
                  return (
                    <div
                      key={row.id}
                      className="mb-1 overflow-hidden px-1.5"
                      style={{
                        height: isExpand ? lerp(22, 46, expand) : 22,
                        boxShadow:
                          row.tone === "block"
                            ? "inset 2px 0 0 #dc2626"
                            : row.tone === "ok"
                              ? "inset 2px 0 0 #33bf00"
                              : "inset 2px 0 0 rgba(0,0,0,0.2)",
                      }}
                    >
                      <CheckRow
                        ok={row.tone === "block" ? false : row.tone === "ok" ? true : "run"}
                        className="h-[22px] text-[10px]"
                      >
                        <span className="min-w-0 flex-1 truncate tabular-nums text-black/70">{body}</span>
                        {done ? (
                          <span
                            className={cn(
                              "shrink-0 tabular-nums",
                              row.tone === "block" ? "text-red-700" : "text-black/45",
                            )}
                          >
                            {row.receipt}
                          </span>
                        ) : null}
                      </CheckRow>
                      {isExpand ? (
                        <pre
                          className="pl-4 font-mono text-[9px] leading-3 tracking-extra-tight text-black/45"
                          style={{ opacity: expand }}
                        >
                          {`{ "amount": "***", "customer": "***", "source": "***" }`}
                        </pre>
                      ) : null}
                    </div>
                  );
                })}
                {dupNote ? (
                  <div className="mt-1 px-1.5">
                    <QueueChip className="text-[10px]">duplicate would have been live.</QueueChip>
                  </div>
                ) : null}
              </div>
              <Hairline />
              <div className="flex items-center justify-between px-3 py-1.5">
                <Node label="clone-local Stripe" lit={clock >= 2.18} />
                <Node label="SendGrid capture" lit={clock >= 4.62} />
              </div>
              {reduced ? (
                <div className="px-3 pb-2">
                  <StatusPill tone="FAIL">api.prod.internal</StatusPill>
                </div>
              ) : null}
            </div>
          </div>

          {mimeOn ? (
            <div
              className="absolute"
              style={{
                left: "16.2%",
                top: "68.4%",
                width: "16.2%",
                opacity: mimeOn ? 1 : 0,
                transition: `opacity 140ms ${FILM_EASE}`,
              }}
            >
              <Receipt className="relative bg-[#fbfaf6]">
                <div className="text-black/40">MIME · captured copy</div>
                <div>Subject: Order #4182</div>
                <div>From: alex@***</div>
                <div>Your card was charged $49.00.</div>
                <div>This is a capture. Not sent.</div>
                <svg className="pointer-events-none absolute inset-0 size-full" aria-hidden>
                  <line
                    x1="6%"
                    y1="58%"
                    x2={`${6 + mimeStrike * 88}%`}
                    y2="42%"
                    stroke="#111"
                    strokeWidth="1.4"
                  />
                </svg>
                <div className="relative mt-0.5 text-black/70" style={{ opacity: mimeStrike }}>
                  NEVER DELIVERED
                </div>
              </Receipt>
            </div>
          ) : null}

          {cpuOn ? (
            <div className="absolute" style={{ left: "28.4%", top: "33%" }}>
              <Sparkline d="M0 22 L20 21 L48 22 L80 20 L120 21 L160 20" width={120} height={28} className="opacity-50" />
            </div>
          ) : null}

          {overlay > 0.02 ? (
            <div
              className="absolute inset-0 flex items-center justify-center bg-[#f7f7f5]/72"
              style={{ opacity: overlay }}
            >
              <div className="bg-white px-5 py-3 ring-1 ring-red-600/50">
                <div className="font-mono text-[12px] tracking-extra-tight text-red-700">
                  unknown destination · denied inside the twin.
                </div>
                <div className="mt-1 font-mono text-[10px] tracking-extra-tight text-black/40">
                  telemetry.unknown.example · unresolved
                </div>
              </div>
            </div>
          ) : null}
        </div>
      </Panel>
      <Panel className="hidden overflow-hidden max-xl:block">
        <div className="flex items-center justify-between gap-3 border-b border-black/10 px-4 py-2.5">
          <MonoLabel className="uppercase tracking-[0.12em]">side-effect firewall</MonoLabel>
          <StatusPill tone="FAIL">DENY</StatusPill>
        </div>
        <ul>
          {LEDGER.map((row) => (
            <li
              key={row.id}
              className="flex items-start justify-between gap-3 border-b border-black/[0.06] px-4 py-2.5 last:border-0"
            >
              <span className="min-w-0 font-mono text-[12px] leading-5 tracking-extra-tight text-black/70">
                {row.body}
              </span>
              <span
                className={cn(
                  "shrink-0 font-mono text-[11px] tabular-nums tracking-extra-tight",
                  row.tone === "block" ? "text-red-700" : "text-black/40",
                )}
              >
                {row.receipt}
              </span>
            </li>
          ))}
        </ul>
        <div className="border-t border-red-600/40 bg-white px-4 py-3 font-mono text-[12px] tracking-extra-tight text-red-700">
          unknown destination · denied inside the twin.
        </div>
      </Panel>
    </div>
  );
}
