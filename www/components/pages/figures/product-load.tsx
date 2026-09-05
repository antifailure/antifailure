import type { ReactNode } from "react";
import { FigCmd } from "./frame";
import { FloatWindow, SageWell } from "@/components/pages/solutions/well";
import { cn } from "@/lib/cn";

type RouteDatum = {
  route: string;
  share: number;
  candidate: number;
  baseline: number | null;
  delta: number | null;
};

const ROUTES: RouteDatum[] = [
  { route: "GET /api/subscriptions", share: 18, candidate: 412, baseline: 180, delta: 1.29 },
  { route: "GET /settings/billing", share: 34, candidate: 168, baseline: 150, delta: 0.12 },
  { route: "GET /", share: 27, candidate: 44, baseline: 41, delta: 0.07 },
  { route: "POST /api/search", share: 9, candidate: 228, baseline: null, delta: null },
];

const MAX_LATENCY = 450;
const P95_THRESHOLD = 0.25;

function FigureChrome({ id, tab, rail }: { id: string; tab: string; rail: string }) {
  return (
    <div className="flex items-end justify-between gap-3 border-b border-black/[0.07] bg-[#f7f7f5] px-3 pt-3">
      <div className="flex min-w-0 items-center gap-2 rounded-t-[9px] bg-[#CAE6D9] px-3 py-2 text-[12px] font-medium text-[#285D49]">
        <span className="size-1.5 shrink-0 rounded-full bg-[#33bf00]" aria-hidden />
        <span className="truncate">{tab}</span>
      </div>
      <div className="mb-2 flex shrink-0 items-baseline gap-2">
        <span className="border-b-2 border-[#33bf00] pb-0.5 font-mono text-[10px] font-medium tracking-[0.12em] text-black">
          {rail}
        </span>
        <span className="hidden font-mono text-[9px] tracking-[0.14em] text-black/30 sm:inline" aria-hidden>
          FIG. {id}
        </span>
      </div>
    </div>
  );
}

function LoadFigure({
  id,
  tab,
  rail,
  label,
  children,
  compact = false,
}: {
  id: string;
  tab: string;
  rail: string;
  label: string;
  children: ReactNode;
  compact?: boolean;
}) {
  const captionId = `${id.toLowerCase()}-caption`;

  return (
    <SageWell
      className={cn(
        "w-full self-start !min-h-0 !px-3 !py-4 sm:!px-5 sm:!py-6",
        compact && "sm:!px-4 sm:!py-5",
      )}
    >
      <figure aria-labelledby={captionId}>
        <figcaption id={captionId} className="sr-only">
          {label}
        </figcaption>
        <FloatWindow className="w-full overflow-hidden border border-black/[0.06]">
          <FigureChrome id={id} tab={tab} rail={rail} />
          <div className={cn("p-3.5 sm:p-4", compact && "sm:p-3.5")}>{children}</div>
        </FloatWindow>
      </figure>
    </SageWell>
  );
}

function RunMetric({ term, value, detail }: { term: string; value: string; detail: string }) {
  return (
    <div className="min-w-0 rounded-[9px] border border-black/[0.06] bg-white px-3 py-2.5 shadow-[0_1px_0_rgba(0,0,0,0.03)]">
      <dt className="font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/42">{term}</dt>
      <dd className="mt-1 text-[16px] font-medium tracking-extra-tight text-black">{value}</dd>
      <dd className="mt-0.5 font-mono text-[9px] leading-3.5 text-black/40">{detail}</dd>
    </div>
  );
}

function ShareMeter({ share }: { share: number }) {
  return (
    <div aria-label={`${share}% of observed requests`}>
      <div className="flex items-center justify-between gap-2 font-mono text-[10px] tabular-nums">
        <span className="text-black/42">share</span>
        <span className="text-black">{share}%</span>
      </div>
      <div className="mt-1.5 h-2 overflow-hidden rounded-full bg-black/[0.06]">
        <span
          className="block h-full rounded-full bg-[#285D49]"
          style={{ width: `${Math.min(100, (share / 40) * 100)}%` }}
        />
      </div>
    </div>
  );
}

function LatencyComparison({
  candidate,
  baseline,
  breach,
}: {
  candidate: number;
  baseline: number | null;
  breach: boolean;
}) {
  const candidateWidth = `${Math.max(2, Math.min(100, (candidate / MAX_LATENCY) * 100))}%`;
  const baselinePosition = baseline === null ? null : `${Math.min(100, (baseline / MAX_LATENCY) * 100)}%`;

  return (
    <div
      aria-label={
        baseline === null
          ? `Candidate p95 ${candidate} milliseconds; no production baseline`
          : `Candidate p95 ${candidate} milliseconds; production baseline ${baseline} milliseconds`
      }
    >
      <div className="flex items-baseline justify-between gap-2 font-mono text-[10px] tabular-nums">
        <span className="text-black/42">p95</span>
        <span className={breach ? "text-[#A93434]" : "text-black"}>
          {candidate}ms
          <span className="ml-1.5 text-black/35">/ {baseline === null ? "no base" : `${baseline}ms`}</span>
        </span>
      </div>
      <div className="relative mt-1.5 h-2.5 rounded-full bg-black/[0.055]">
        <span
          className={cn("absolute inset-y-0 left-0 rounded-full", breach ? "bg-[#C95B5B]" : "bg-[#66A58C]")}
          style={{ width: candidateWidth }}
        />
        {baselinePosition ? (
          <span
            className="absolute -top-1 h-4 w-px bg-black shadow-[0_0_0_1px_white]"
            style={{ left: baselinePosition }}
            aria-hidden
          />
        ) : null}
      </div>
    </div>
  );
}

function Verdict({ delta }: { delta: number | null }) {
  const breach = delta !== null && delta > P95_THRESHOLD;
  const text = delta === null ? "unscored" : breach ? "breach" : "within";
  const deltaText = delta === null ? "no baseline" : `+${Math.round(delta * 100)}%`;

  return (
    <div className="flex items-center justify-between gap-2 sm:block sm:text-right">
      <span
        className={cn(
          "inline-flex rounded-[6px] border px-2 py-1 font-mono text-[9px] font-medium uppercase tracking-[0.08em]",
          delta === null && "border-black/10 bg-black/[0.025] text-black/42",
          breach && "border-[#C95B5B]/30 bg-[#F8E4E4] text-[#A93434]",
          delta !== null && !breach && "border-[#66A58C]/30 bg-[#E4F1EB] text-[#285D49]",
        )}
      >
        {text}
      </span>
      <div className="font-mono text-[10px] tabular-nums text-black/45 sm:mt-1">{deltaText}</div>
    </div>
  );
}

function RouteRow({ datum, index }: { datum: RouteDatum; index: number }) {
  const breach = datum.delta !== null && datum.delta > P95_THRESHOLD;

  return (
    <li
      className={cn(
        // min-w-0, because a grid item's default min-width is auto and this row
        // then refuses to go narrower than its own content. At 320px that put
        // the row 87px wider than the track and the figure's overflow-hidden
        // cut the verdict badge and the p95 reading off mid word.
        "min-w-0 rounded-[10px] border px-3 py-3 shadow-[0_1px_0_rgba(0,0,0,0.03)]",
        breach ? "border-[#C95B5B]/24 bg-[#FFF8F7]" : "border-black/[0.06] bg-white",
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span
              className={cn("size-2 shrink-0 rounded-full", breach ? "bg-[#C95B5B]" : "bg-[#66A58C]")}
              aria-hidden
            />
            <div className="truncate font-mono text-[11px] font-medium tracking-extra-tight text-black">{datum.route}</div>
          </div>
          <p className="mt-1 pl-4 font-mono text-[9px] text-black/35">
            {index === 0 ? "largest p95 increase" : datum.baseline === null ? "allowed route without baseline" : "production baseline compared"}
          </p>
        </div>
        <Verdict delta={datum.delta} />
      </div>

      <div className="mt-3 grid gap-3 sm:grid-cols-[7rem_minmax(0,1fr)] sm:items-end">
        <ShareMeter share={datum.share} />
        <LatencyComparison candidate={datum.candidate} baseline={datum.baseline} breach={breach} />
      </div>
    </li>
  );
}

function RouteMixBand() {
  return (
    <div className="overflow-hidden rounded-[11px] border border-black/[0.06] bg-white" aria-label="Route mix">
      <div className="flex h-3">
        {ROUTES.map((route) => (
          <span
            key={route.route}
            className={cn(route.delta !== null && route.delta > P95_THRESHOLD ? "bg-[#C95B5B]" : "bg-[#66A58C]")}
            style={{ width: `${route.share}%` }}
            aria-hidden
          />
        ))}
        <span className="flex-1 bg-black/[0.06]" aria-hidden />
      </div>
      <div className="grid gap-1.5 px-3 py-2.5 sm:grid-cols-2">
        {ROUTES.map((route) => (
          <div key={route.route} className="flex min-w-0 items-center gap-2 font-mono text-[9px] text-black/45">
            <span
              className={cn("size-1.5 shrink-0 rounded-full", route.delta !== null && route.delta > P95_THRESHOLD ? "bg-[#C95B5B]" : "bg-[#66A58C]")}
              aria-hidden
            />
            <span className="truncate">
              {route.share}% · {route.route}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

export function PLD01() {
  return (
    <LoadFigure
      id="P-LD-01"
      tab="af load · shaped run"
      rail="COMPARE"
      label="A shaped OpenTelemetry load run comparing candidate route latency with production baselines and enforcing route safety."
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <FigCmd>$ af load</FigCmd>
          <h3 className="mt-1 text-[14px] font-medium tracking-tight text-black">Production-shaped route comparison</h3>
        </div>
        <div className="flex items-center gap-2 font-mono text-[10px] text-black/42">
          <span className="size-1.5 rounded-full bg-[#33bf00]" aria-hidden />
          run complete
        </div>
      </div>

      <dl className="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-4">
        <RunMetric term="source" value="OTel" detail="OTLP / JSON" />
        <RunMetric term="achieved" value="17.8/s" detail="reported, not target" />
        <RunMetric term="baselines" value="3 of 4" detail="shown routes" />
        <RunMetric term="threshold" value="+25%" detail="p95 increase" />
      </dl>

      <section className="mt-3 rounded-[12px] border border-black/[0.07] bg-[#FAFAF8] p-3" aria-labelledby="route-results-heading">
        <div className="flex flex-wrap items-end justify-between gap-2">
          <div>
            <h4 id="route-results-heading" className="font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-black/62">
              Route mix + p95 comparison
            </h4>
            <p className="mt-0.5 font-mono text-[9px] text-black/35">weighted arrivals · baseline marker in black · 0 to 450ms scale</p>
          </div>
          <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-black/35">candidate vs production</span>
        </div>
        <div className="mt-3">
          <RouteMixBand />
        </div>
        <ol className="mt-3 grid gap-2">
          {ROUTES.map((datum, index) => (
            <RouteRow key={datum.route} datum={datum} index={index} />
          ))}
        </ol>
      </section>

      <div className="mt-3 grid gap-2 sm:grid-cols-[0.9fr_1.1fr]">
        <div className="rounded-[10px] border border-[#66A58C]/25 bg-[#F1F7F4] px-3 py-2.5">
          <div className="font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-[#285D49]">allowed to send</div>
          <p className="mt-1 font-mono text-[10px] leading-4 text-black/65">GET /** · POST /api/search</p>
        </div>
        <div className="rounded-[10px] border border-[#C95B5B]/22 bg-[#FFF8F7] px-3 py-2.5">
          <div className="font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-[#A93434]">refused before send</div>
          <p className="mt-1 break-words font-mono text-[10px] leading-4 text-black/65">
            POST /billing/upgrade · POST /api/payments/intent
          </p>
        </div>
      </div>
    </LoadFigure>
  );
}

function YamlLine({ line, number }: { line: string; number: number }) {
  const match = line.match(/^(\s*)([^:]+)(:)(.*)$/);

  return (
    <div className="grid grid-cols-[2rem_minmax(0,1fr)]">
      <span className="select-none py-0.5 pr-2 text-right font-mono text-[9px] leading-[19px] text-black/24" aria-hidden>
        {number}
      </span>
      <code className="min-w-0 whitespace-pre-wrap break-words py-0.5 pr-2 pl-2.5 font-mono text-[10px] leading-[19px] tracking-extra-tight text-black/70">
        {match ? (
          <>
            {match[1]}
            <span className="font-medium text-[#285D49]">{match[2]}</span>
            <span className="text-black/35">{match[3]}</span>
            <span className="text-black/62">{match[4]}</span>
          </>
        ) : (
          line
        )}
      </code>
    </div>
  );
}

const CAPABILITIES = [
  { name: "route weights", otel: "yes", access: "yes", detail: "both sources can shape the route mix" },
  { name: "arrival rate", otel: "yes", access: "yes", detail: "achieved rate is measured after compilation" },
  { name: "p95 baseline", otel: "yes", access: "none", detail: "baseline comparison requires traced latency" },
  { name: "p95_increase", otel: "valid", access: "refused", detail: "access log manifests cannot enforce p95 increase" },
] as const;

const PIPELINE = [
  { step: "01", label: "read", detail: "repository file" },
  { step: "02", label: "validate", detail: "source + threshold" },
  { step: "03", label: "shape", detail: "mix + arrivals" },
  { step: "04", label: "send", detail: "allowlisted routes" },
] as const;

function CapabilityMark({ value }: { value: string }) {
  const negative = value === "none" || value === "refused";
  return (
    <span
      className={cn(
        "inline-flex rounded-[6px] border px-2 py-1 font-mono text-[9px] font-medium uppercase tracking-[0.08em]",
        negative ? "border-[#C95B5B]/24 bg-[#FFF8F7] text-[#A93434]" : "border-[#66A58C]/30 bg-[#E4F1EB] text-[#285D49]",
      )}
    >
      {value}
    </span>
  );
}

function CapabilityCard({ capability }: { capability: (typeof CAPABILITIES)[number] }) {
  return (
    // min-w-0 for the same reason RouteRow above carries it: a grid item's
    // default min-width is auto, so this card refused to go narrower than its
    // own header. At 320px it sat 19px wider than its track and the figure's
    // overflow-hidden cut the capability marks off rather than scrolling.
    <li className="min-w-0 rounded-[10px] border border-black/[0.06] bg-white p-3 shadow-[0_1px_0_rgba(0,0,0,0.03)]">
      <div className="flex min-w-0 items-start justify-between gap-2">
        <div className="min-w-0 font-mono text-[10px] font-medium tracking-extra-tight text-black">{capability.name}</div>
        <div className="flex shrink-0 gap-1.5">
          <CapabilityMark value={capability.otel} />
          <CapabilityMark value={capability.access} />
        </div>
      </div>
      <p className="mt-2 font-mono text-[9px] leading-4 text-black/42">{capability.detail}</p>
    </li>
  );
}

export function PLD02({ source }: { source: string }) {
  const lines = source.split("\n");

  return (
    <LoadFigure
      id="P-LD-02"
      tab="load.yml · source contract"
      rail="VALIDATE"
      label="A load manifest compiled from a repository file, showing the different baseline capabilities of OpenTelemetry traces and access logs."
      compact
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <FigCmd>$ af load --manifest load.yml</FigCmd>
          <h3 className="mt-1 text-[14px] font-medium tracking-tight text-black">Manifest compilation</h3>
        </div>
        <span className="rounded-[6px] border border-[#66A58C]/30 bg-[#E4F1EB] px-2 py-1 font-mono text-[9px] font-medium uppercase tracking-[0.1em] text-[#285D49]">
          local input
        </span>
      </div>

      <div className="mt-3 grid gap-3 sm:grid-cols-[minmax(0,1.08fr)_minmax(13rem,0.92fr)]">
        <section className="min-w-0 overflow-hidden rounded-[11px] border border-black/[0.07] bg-[#FAFAF8]" aria-labelledby="manifest-source-heading">
          <div className="flex items-center justify-between gap-2 border-b border-black/[0.065] bg-[#F2F3F0] px-3 py-2.5">
            <h4 id="manifest-source-heading" className="font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/58">
              workload manifest
            </h4>
            <span className="font-mono text-[9px] text-black/32">repository</span>
          </div>
          <div className="py-2">
            {lines.map((line, index) => (
              <YamlLine key={`${index}-${line}`} line={line} number={index + 1} />
            ))}
          </div>
        </section>

        <section className="min-w-0 rounded-[11px] border border-black/[0.07] bg-[#FAFAF8] p-3" aria-labelledby="source-capabilities-heading">
          <div className="flex items-center justify-between gap-2">
            <h4 id="source-capabilities-heading" className="font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/58">
              source capabilities
            </h4>
            <div className="flex gap-1.5 font-mono text-[8px] uppercase tracking-[0.1em] text-black/35">
              <span>otel</span>
              <span>access</span>
            </div>
          </div>
          <ol className="mt-3 grid gap-2">
            {CAPABILITIES.map((capability) => (
              <CapabilityCard key={capability.name} capability={capability} />
            ))}
          </ol>
          <div className="mt-2 rounded-[10px] border border-[#C95B5B]/22 bg-[#FFF8F7] px-3 py-2.5">
            <div className="flex items-start gap-2">
              <span className="mt-1 block size-1.5 shrink-0 rounded-full bg-[#C95B5B]" aria-hidden />
              <p className="font-mono text-[9px] leading-4 text-[#7E3434]">
                AF-MAN-002 · access_log + p95_increase is refused before anything is built.
              </p>
            </div>
          </div>
        </section>
      </div>

      <section className="mt-3 rounded-[11px] border border-black/[0.07] bg-[#FAFAF8] p-3" aria-labelledby="compile-path-heading">
        <div className="flex items-center justify-between gap-2">
          <h4 id="compile-path-heading" className="font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black/58">
            compile path
          </h4>
          <span className="font-mono text-[9px] text-black/32">no outbound lookup</span>
        </div>
        <ol className="mt-3 grid gap-2 sm:grid-cols-4">
          {PIPELINE.map((item, index) => (
            <li
              key={item.step}
              className="relative min-w-0 rounded-[10px] border border-black/[0.06] bg-white px-3 py-2.5 shadow-[0_1px_0_rgba(0,0,0,0.03)]"
            >
              <div className="flex items-center gap-2">
                <span className="flex size-5 shrink-0 items-center justify-center rounded-full border border-[#66A58C]/45 bg-[#E4F1EB] font-mono text-[8px] font-medium text-[#285D49]">
                  {item.step}
                </span>
                {index < PIPELINE.length - 1 ? (
                  <span className="absolute top-1/2 right-[-8px] z-[1] hidden h-px w-3 bg-black/18 sm:block" aria-hidden />
                ) : null}
                <span className="font-mono text-[9px] font-medium uppercase tracking-[0.1em] text-black/65">{item.label}</span>
              </div>
              <p className="mt-1.5 pl-7 font-mono text-[9px] leading-4 text-black/38">{item.detail}</p>
            </li>
          ))}
        </ol>
      </section>
    </LoadFigure>
  );
}
