import type { ReactNode } from "react";
import { StatusPill } from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

const GREEN_DARK = "#285D49";

function FigureShell({
  id,
  tab,
  rail,
  summary,
  children,
  className,
}: {
  id: string;
  tab: string;
  rail: string;
  summary: string;
  children: ReactNode;
  className?: string;
}) {
  const titleId = `${id}-title`;
  const summaryId = `${id}-summary`;

  return (
    <figure
      aria-labelledby={titleId}
      aria-describedby={summaryId}
      className={cn(
        "relative w-full overflow-hidden rounded-[18px] border border-[#285D49]/14 bg-[#EEF5F1] p-3 font-sans sm:p-4",
        className,
      )}
    >
      <div className="relative overflow-hidden rounded-[12px] border border-black/[0.08] bg-white shadow-[0_12px_30px_rgba(40,93,73,0.08)]">
        <div className="flex min-h-11 items-center justify-between gap-3 border-b border-black/[0.07] bg-[#F8FAF8] px-3 sm:px-4">
          <div id={titleId} className="min-w-0 text-[11px] font-medium text-black sm:text-[12px]">
            <span className="block truncate">{tab}</span>
          </div>
          <div className="flex shrink-0 items-baseline gap-2">
            <span className="font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-[#285D49] sm:text-[10px]">
              {rail}
            </span>
            <span className="hidden font-mono text-[9px] uppercase tracking-[0.12em] text-black/35 sm:inline">
              FIG. {id}
            </span>
          </div>
        </div>
        <div className="p-3 sm:p-4">{children}</div>
      </div>

      <figcaption id={summaryId} className="sr-only">
        {summary}
      </figcaption>
    </figure>
  );
}

// The green is a prop rather than something a caller adds through className.
// `cn` is a plain join, so text-[#285D49] passed in landed beside the eyebrow's
// own text-black/50 and lost the cascade: three eyebrows meant to carry the
// sage signal rendered in the same grey as the ones that carry none.
function Eyebrow({ children, className, tone = "muted" }: { children: ReactNode; className?: string; tone?: "muted" | "sage" }) {
  return (
    <span
      className={cn(
        "font-mono text-[8px] font-medium uppercase tracking-[0.12em] sm:text-[9px]",
        tone === "sage" ? "text-[#285D49]" : "text-black/50",
        className,
      )}
    >
      {children}
    </span>
  );
}

function StateDot({ tone = "muted" }: { tone?: "active" | "danger" | "muted" }) {
  return (
    <span
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        tone === "active" && "bg-[#33bf00]",
        tone === "danger" && "bg-[#B93838]",
        tone === "muted" && "bg-black/20",
      )}
      aria-hidden="true"
    />
  );
}

function Arrow({ vertical = false }: { vertical?: boolean }) {
  return (
    <svg
      viewBox={vertical ? "0 0 12 32" : "0 0 32 12"}
      className={vertical ? "h-6 w-3" : "h-3 w-7"}
      fill="none"
      aria-hidden="true"
    >
      {vertical ? (
        <>
          <path d="M6 1v28" stroke="currentColor" strokeWidth="1" strokeDasharray="2 2" />
          <path d="m2.5 25.5 3.5 4 3.5-4" stroke="currentColor" strokeWidth="1" />
        </>
      ) : (
        <>
          <path d="M1 6h28" stroke="currentColor" strokeWidth="1" strokeDasharray="2 2" />
          <path d="m25.5 2.5 4 3.5-4 3.5" stroke="currentColor" strokeWidth="1" />
        </>
      )}
    </svg>
  );
}

function StageMark({
  index,
  label,
  final,
}: {
  index: number;
  label: string;
  final?: boolean;
}) {
  return (
    <li className="relative min-w-0">
      {!final ? <span className="absolute top-[7px] right-0 left-5 h-px bg-black/12" aria-hidden="true" /> : null}
      <span className="relative z-10 flex size-3.5 items-center justify-center rounded-full border border-[#285D49]/25 bg-white font-mono text-[7px] font-semibold text-[#285D49]">
        {index}
      </span>
      <span className="mt-1.5 block font-mono text-[7px] uppercase leading-3 tracking-[0.04em] text-black/55 sm:text-[9px] sm:tracking-[0.06em]">
        {label}
      </span>
    </li>
  );
}

function TopologyNode({
  label,
  detail,
  tone = "plain",
}: {
  label: string;
  detail: string;
  tone?: "plain" | "mint" | "danger";
}) {
  return (
    <div
      className={cn(
        "min-w-0 border-l-2 px-2.5 py-2",
        tone === "plain" && "border-black/[0.14] bg-[#F8FAF8]",
        tone === "mint" && "border-[#285D49] bg-[#F1F7F4]",
        tone === "danger" && "border-[#B93838] bg-[#FFF3F3]",
      )}
    >
      <div className="flex items-center gap-1.5">
        <StateDot tone={tone === "danger" ? "danger" : tone === "mint" ? "active" : "muted"} />
        <span className="min-w-0 break-words text-[10px] font-medium leading-4 text-black sm:text-[12px]">{label}</span>
      </div>
      <p className="mt-1 font-mono text-[8px] leading-3.5 text-black/48 sm:text-[9px]">{detail}</p>
    </div>
  );
}

export function POV01() {
  const stages = ["inspect", "provision", "restore", "exercise", "decide", "destroy"];

  return (
    <FigureShell
      id="P-OV-01"
      tab="disposable twin · one run"
      rail="LIFECYCLE"
      summary="A six-stage disposable twin run: inspect the repository, provision an isolated stack, restore safe state, exercise workflows behind a fail-closed egress boundary, decide, and destroy every journaled resource."
    >
      <ol className="grid grid-cols-3 gap-x-2 gap-y-3 border-b border-black/[0.07] pb-3 sm:grid-cols-6" aria-label="Twin run phases">
        {stages.map((stage, index) => (
          <StageMark key={stage} index={index + 1} label={stage} final={index === stages.length - 1} />
        ))}
      </ol>

      <div className="mt-3 grid items-stretch gap-3 sm:grid-cols-[1fr_1.55fr_1fr]">
        <section className="min-w-0" aria-label="Run inputs">
          <Eyebrow>Inputs</Eyebrow>
          <div className="mt-2 space-y-1.5">
            <TopologyNode label="Repository" detail="change + manifest" />
            <TopologyNode label="Cloud" detail="customer boundary" />
          </div>
        </section>

        <section className="relative min-w-0 border-x border-[#285D49]/16 px-3" aria-label="Isolated twin boundary">
          <div className="flex items-center justify-between gap-2">
            <Eyebrow tone="sage">Isolated run boundary</Eyebrow>
            <span className="font-mono text-[8px] uppercase tracking-[0.1em] text-[#285D49]/70">temporary</span>
          </div>

          <div className="mt-2 grid grid-cols-2 gap-1.5">
            <TopologyNode label="Candidate" detail="change under test" tone="mint" />
            <TopologyNode label="Safe state" detail="sanitized branch" tone="mint" />
            <TopologyNode label="Workers" detail="declared jobs" />
            <TopologyNode label="Load" detail="journeys + route mix" />
          </div>

          <div className="mt-2.5 flex items-center gap-2 border-t border-[#285D49]/14 pt-2.5">
            <span className="flex size-5 shrink-0 items-center justify-center text-[#285D49]" aria-hidden="true">
              <svg viewBox="0 0 16 16" className="size-3" fill="none">
                <path d="M8 1.5 13 3.6v3.6c0 3.2-2 5.7-5 7.3-3-1.6-5-4.1-5-7.3V3.6L8 1.5Z" stroke={GREEN_DARK} strokeWidth="1.2" />
                <path d="m5.7 8 1.4 1.4 3.2-3.2" stroke={GREEN_DARK} strokeWidth="1.2" />
              </svg>
            </span>
            <div className="min-w-0">
              <div className="text-[10px] font-medium text-black sm:text-[11px]">Side-effect firewall</div>
              <div className="font-mono text-[8px] text-black/50 sm:text-[9px]">all egress has an explicit mode</div>
            </div>
          </div>
        </section>

        <section className="min-w-0" aria-label="Contained outputs">
          <Eyebrow>Outputs</Eyebrow>
          <div className="mt-2 space-y-1.5">
            <TopologyNode label="Evidence" detail="trace + rows + video" tone="mint" />
            <TopologyNode label="PR gate" detail="pass or fail" tone="danger" />
          </div>
        </section>
      </div>

      <div className="mt-3 grid items-center gap-2 border-t border-black/[0.07] pt-3 sm:grid-cols-[auto_1fr_auto]">
        <div className="flex items-center gap-2">
          <span className="flex size-5 items-center justify-center text-black/55" aria-hidden="true">
            <svg viewBox="0 0 16 16" className="size-3.5" fill="none">
              <path d="M3 4h10M5 4V2.5h6V4m1 0-.6 9H4.6L4 4m2.2 2.2v4.5m3.6-4.5v4.5" stroke="currentColor" strokeWidth="1.1" />
            </svg>
          </span>
          <div>
            <div className="text-[10px] font-medium text-black sm:text-[11px]">Resource journal</div>
            <div className="font-mono text-[8px] text-black/45 sm:text-[9px]">every temporary resource recorded</div>
          </div>
        </div>
        <div className="hidden items-center text-black/25 sm:flex" aria-hidden="true">
          <span className="h-px flex-1 bg-black/12" />
          <Arrow />
          <span className="h-px flex-1 bg-black/12" />
        </div>
        <div className="flex items-center gap-2 sm:justify-end">
          <span className="font-mono text-[9px] uppercase tracking-[0.12em] text-[#285D49]">teardown proof</span>
          <span className="font-mono text-[11px] text-[#285D49]" aria-hidden="true">verified</span>
        </div>
      </div>
    </FigureShell>
  );
}

const COVERAGE_DIMENSIONS = ["State volume", "Rare records", "Concurrency", "Isolation", "Egress", "Decision"];

export function POV02({ rows }: { rows: { miss: string; have: string }[] }) {
  return (
    <FigureShell
      id="P-OV-02"
      tab="staging vs disposable twin"
      rail="COVERAGE"
      summary="A six-dimension comparison showing that staging leaves production scale, rare records, concurrency, branch isolation, side-effect containment, and merge judgment fragmented, while a disposable twin closes the loop."
    >
      <div className="grid grid-cols-[minmax(76px,0.72fr)_minmax(0,1fr)_minmax(0,1fr)] items-end gap-2 border-b border-black/[0.08] pb-2.5 sm:grid-cols-[minmax(104px,0.72fr)_minmax(0,1fr)_24px_minmax(0,1fr)]">
        <Eyebrow>Dimension</Eyebrow>
        <div>
          <div className="text-[11px] font-medium text-black sm:text-[12px]">Shared staging</div>
          <p className="mt-0.5 font-mono text-[8px] text-black/45 sm:text-[9px]">fragmented signals</p>
        </div>
        <span className="hidden sm:block" aria-hidden="true" />
        <div>
          <div className="text-[11px] font-medium text-[#285D49] sm:text-[12px]">Disposable twin</div>
          <p className="mt-0.5 font-mono text-[8px] text-[#285D49]/70 sm:text-[9px]">one decision path</p>
        </div>
      </div>

      <ol className="divide-y divide-black/[0.06]" aria-label="Coverage comparison">
        {rows.map((row, index) => (
          <li
            key={row.miss}
            className="grid grid-cols-[minmax(76px,0.72fr)_minmax(0,1fr)_minmax(0,1fr)] items-start gap-2 py-2.5 sm:grid-cols-[minmax(104px,0.72fr)_minmax(0,1fr)_24px_minmax(0,1fr)]"
          >
            <span className="pr-1 font-mono text-[8px] uppercase leading-3 tracking-[0.08em] text-black/50 sm:text-[9px]">
              {COVERAGE_DIMENSIONS[index] ?? `Dimension ${index + 1}`}
            </span>
            <div className="flex min-w-0 items-start gap-1.5">
              <span className="mt-[5px] h-px w-3 shrink-0 bg-black/22" aria-hidden="true" />
              <span className="min-w-0 text-[9px] leading-3.5 text-black/55 sm:text-[10px] sm:leading-4">{row.miss}</span>
            </div>
            <div className="hidden justify-center text-black/22 sm:flex" aria-hidden="true">
              <Arrow />
            </div>
            <div className="flex min-w-0 items-start gap-1.5 border-l-2 border-[#285D49] pl-2">
              <span className="mt-[3px] font-mono text-[8px] font-semibold text-[#285D49]" aria-hidden="true">✓</span>
              <span className="min-w-0 text-[9px] leading-3.5 text-black/80 sm:text-[10px] sm:leading-4">{row.have}</span>
            </div>
          </li>
        ))}
      </ol>

      <section className="mt-3 border-t border-[#285D49]/18 pt-3" aria-label="Closed decision loop">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Eyebrow tone="sage">Closed decision loop</Eyebrow>
          <StatusPill tone="FAIL">PR gate</StatusPill>
        </div>
        <ol className="mt-2.5 grid grid-cols-4 gap-1 border border-[#285D49]/14 bg-[#F1F7F4] p-1.5" aria-label="Twin decision sequence">
          {["Safe state", "Workload", "Containment", "Evidence"].map((item, index) => (
            <li key={item} className="relative min-w-0 bg-white px-1.5 py-2 text-center text-[8px] leading-3 text-black/70 sm:text-[9px]">
              {item}
              {index < 3 ? <span className="absolute -right-1.5 top-1/2 z-10 -translate-y-1/2 font-mono text-[9px] text-[#285D49]" aria-hidden="true">→</span> : null}
            </li>
          ))}
        </ol>
      </section>
    </FigureShell>
  );
}

function TimelineLane({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="grid grid-cols-[76px_minmax(0,1fr)] items-center gap-2.5 sm:grid-cols-[104px_minmax(0,1fr)]">
      <span className="font-mono text-[8px] uppercase leading-3 tracking-[0.08em] text-black/55 sm:text-[9px]">{label}</span>
      <div className="relative h-8 border border-black/[0.06] bg-[#F8FAF8]">{children}</div>
    </div>
  );
}

export function POV03() {
  const samples = Array.from({ length: 110 }, (_, index) => index);

  return (
    <FigureShell
      id="P-OV-03"
      tab="subscriptions · migration rehearsal"
      rail="LOCK TRACE"
      summary="A sampled Postgres migration trace showing an ACCESS EXCLUSIVE lock held for 27.4 seconds, a waiting application session observed during the hold, and 110 observations taken at 250 millisecond intervals before release."
    >
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-black/[0.08] pb-3">
        <div>
          <Eyebrow>Strongest lock observed</Eyebrow>
          <div className="mt-1.5 flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <span className="text-[15px] font-medium tracking-tight text-black">ACCESS EXCLUSIVE</span>
            <span className="font-mono text-[12px] font-semibold tabular-nums text-[#B93838]">27.4s</span>
          </div>
        </div>
        <StatusPill tone="FAIL">finding</StatusPill>
      </div>

      <section className="mt-3.5" aria-label="Lock hold timeline">
        <div className="ml-[86px] grid grid-cols-5 text-center font-mono text-[8px] tabular-nums text-black/40 sm:ml-[114px] sm:text-[9px]" aria-hidden="true">
          <span className="text-left">0s</span>
          <span>8s</span>
          <span>16s</span>
          <span>24s</span>
          <span className="text-right">27.4s</span>
        </div>
        <div className="mt-1.5 space-y-2">
          <TimelineLane label="migration tx">
            <div className="absolute inset-y-1 left-1 right-1 border border-[#B93838]/35 bg-[#F7DCDC] px-2">
              <div className="flex h-full items-center justify-between gap-2">
                <span className="truncate font-mono text-[8px] font-medium text-[#8D2929] sm:text-[9px]">ACCESS EXCLUSIVE</span>
                <span className="shrink-0 font-mono text-[8px] tabular-nums text-[#8D2929] sm:text-[9px]">hold 27.4s</span>
              </div>
            </div>
          </TimelineLane>

          <TimelineLane label="app session">
            <span className="absolute left-[19%] top-1/2 h-4 w-px -translate-y-1/2 bg-[#B93838]" aria-hidden="true" />
            <div className="absolute inset-y-1 left-[20%] right-1 flex items-center border border-dashed border-[#B93838]/35 bg-white px-2">
              <span className="truncate font-mono text-[8px] text-[#8D2929] sm:text-[9px]">waiting session observed</span>
            </div>
          </TimelineLane>

          <TimelineLane label="sampler">
            <div className="absolute inset-x-2 top-1/2 h-px -translate-y-1/2 bg-black/10" aria-hidden="true" />
            {Array.from({ length: 18 }, (_, index) => (
              <span
                key={index}
                className="absolute top-1/2 size-1 -translate-x-1/2 -translate-y-1/2 rounded-full bg-[#285D49]"
                style={{ left: `${3 + (index * 94) / 17}%` }}
                aria-hidden="true"
              />
            ))}
            <span className="absolute right-2 top-1/2 -translate-y-1/2 bg-[#F8FAF8] pl-1 font-mono text-[8px] text-[#285D49] sm:text-[9px]">250ms</span>
          </TimelineLane>
        </div>
      </section>

      <section className="mt-3.5 border-t border-black/[0.07] pt-3" aria-label="Sampling receipt">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <div className="text-[10px] font-medium text-black sm:text-[11px]">Observation receipt</div>
            <p className="mt-0.5 font-mono text-[8px] text-black/45 sm:text-[9px]">110 samples · one every 250 milliseconds</p>
          </div>
          <span className="font-mono text-[9px] uppercase tracking-[0.12em] text-[#285D49]">release seen</span>
        </div>
        <div className="mt-2.5 grid grid-cols-[repeat(22,minmax(0,1fr))] gap-[2px]" aria-hidden="true">
          {samples.map((sample) => (
            <span
              key={sample}
              className={cn(
                "h-1 rounded-full",
                sample < 109 ? "bg-[#B93838]/55" : "bg-[#33bf00]",
              )}
            />
          ))}
        </div>
      </section>

      <dl className="mt-3 grid grid-cols-3 divide-x divide-black/[0.07] border border-black/[0.07] bg-white">
        {[
          ["blocked another", "yes"],
          ["table rewrite", "yes"],
          ["plan changed", "yes"],
        ].map(([term, value]) => (
          <div key={term} className="min-w-0 px-2 py-2.5 text-center sm:px-3">
            <dt className="font-mono text-[7px] uppercase leading-3 tracking-[0.08em] text-black/45 sm:text-[8px]">{term}</dt>
            <dd className="mt-1 text-[10px] font-medium text-[#B93838] sm:text-[11px]">{value}</dd>
          </div>
        ))}
      </dl>
    </FigureShell>
  );
}

function PlanNode({
  label,
  relation,
  tone,
}: {
  label: string;
  relation: string;
  tone: "baseline" | "candidate";
}) {
  const baseline = tone === "baseline";

  return (
    <div
      className={cn(
        "border-l-2 px-2.5 py-2",
        baseline ? "border-[#285D49] bg-[#F1F7F4]" : "border-[#B93838] bg-[#FFF3F3]",
      )}
    >
      <div className="flex items-center gap-1.5">
        <StateDot tone={baseline ? "active" : "danger"} />
        <span className="text-[10px] font-medium text-black sm:text-[11px]">{label}</span>
      </div>
      <div className="mt-1 pl-3 font-mono text-[8px] text-black/50 sm:text-[9px]">relation: {relation}</div>
    </div>
  );
}

export function POV04() {
  const findings = [
    { label: "strongest lock", value: "ACCESS EXCLUSIVE 27.4s on subscriptions", severe: true },
    { label: "blocked another", value: "yes, a session was seen waiting", severe: true },
    { label: "table rewrite", value: "yes, reported by Postgres", severe: false },
    { label: "plan change", value: "Index Scan to Seq Scan on events", severe: false },
  ];

  return (
    <FigureShell
      id="P-OV-04"
      tab="af insights · migration rehearsal"
      rail="EVIDENCE"
      summary="A failed migration rehearsal report that records the strongest lock, the blocked session, a table rewrite, a baseline-to-candidate query plan regression, and a concrete lint remediation."
    >
      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-black/[0.08] pb-3">
        <div>
          <Eyebrow>Deployment judgment</Eyebrow>
          <div className="mt-1 text-[14px] font-medium text-black">Migration safety report</div>
        </div>
        <div className="flex items-center gap-2">
          <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-black/45">merge gate</span>
          <StatusPill tone="FAIL">FAIL</StatusPill>
        </div>
      </header>

      <dl className="mt-3 grid grid-cols-2 divide-x divide-y divide-black/[0.06] border border-black/[0.07] sm:grid-cols-4 sm:divide-y-0">
        {[
          ["lock", "27.4s"],
          ["blocked", "yes"],
          ["rewrite", "yes"],
          ["plan", "changed"],
        ].map(([term, value], index) => (
          <div
            key={term}
            className={cn(
              "min-w-0 px-2.5 py-2",
              index < 2 ? "bg-[#FFF3F3]" : "bg-[#F8FAF8]",
            )}
          >
            <dt className="font-mono text-[8px] uppercase tracking-[0.1em] text-black/45">{term}</dt>
            <dd className={cn("mt-1 text-[11px] font-medium sm:text-[12px]", index < 2 ? "text-[#B93838]" : "text-black")}>{value}</dd>
          </div>
        ))}
      </dl>

      <section className="mt-3 overflow-hidden border border-black/[0.07]" aria-label="Recorded findings">
        <div className="grid grid-cols-[0.8fr_1.5fr_auto] gap-2 bg-[#F8FAF8] px-2.5 py-1.5 font-mono text-[8px] uppercase tracking-[0.1em] text-black/45 sm:px-3">
          <span>check</span>
          <span>evidence</span>
          <span>state</span>
        </div>
        {findings.map((finding, index) => (
          <div
            key={finding.label}
            className={cn(
              "grid grid-cols-[0.8fr_1.5fr_auto] items-center gap-2 border-t border-black/[0.06] px-2.5 py-2 sm:px-3",
              index % 2 === 0 ? "bg-white" : "bg-[#FCFDFB]",
            )}
          >
            <span className="min-w-0 font-mono text-[8px] uppercase leading-3 tracking-[0.06em] text-black/50 sm:text-[9px]">{finding.label}</span>
            <span className="min-w-0 [overflow-wrap:anywhere] text-[9px] leading-3.5 text-black/75 sm:text-[10px] sm:leading-4">{finding.value}</span>
            <span className={cn("size-2 rounded-full", finding.severe ? "bg-[#B93838]" : "bg-black/25")}>
              <span className="sr-only">{finding.severe ? "blocking evidence" : "supporting evidence"}</span>
            </span>
          </div>
        ))}
      </section>

      <section className="mt-3 border-t border-black/[0.07] pt-3" aria-label="Query plan comparison">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Eyebrow>Plan delta · events</Eyebrow>
          <span className="font-mono text-[8px] text-[#B93838]">regression observed</span>
        </div>
        <div className="mt-2.5 grid items-center gap-2 sm:grid-cols-[1fr_24px_1fr]">
          <div className="min-w-0">
            <div className="mb-1.5 font-mono text-[8px] uppercase tracking-[0.1em] text-[#285D49]">baseline</div>
            <PlanNode label="Index Scan" relation="events" tone="baseline" />
          </div>
          <div className="hidden justify-center text-black/30 sm:flex" aria-hidden="true"><Arrow /></div>
          <div className="min-w-0">
            <div className="mb-1.5 font-mono text-[8px] uppercase tracking-[0.1em] text-[#B93838]">candidate</div>
            <PlanNode label="Seq Scan" relation="events" tone="candidate" />
          </div>
        </div>
      </section>

      <aside className="mt-3 border-l-2 border-[#285D49] bg-[#F1F7F4] px-3 py-2.5" aria-label="Suggested remediation">
        <div className="flex items-start gap-2.5">
          <span className="mt-0.5 flex size-5 shrink-0 items-center justify-center font-mono text-[9px] font-semibold text-[#285D49]" aria-hidden="true">i</span>
          <div className="min-w-0">
            <Eyebrow tone="sage">lint · safer sequence</Eyebrow>
            <p className="mt-1 text-[9px] leading-4 text-[#285D49] sm:text-[10px]">
              Add a second column of the new type, backfill it, then drop the old one.
            </p>
          </div>
        </div>
      </aside>

      <div className="mt-3 flex flex-wrap items-center gap-x-2 gap-y-1 border-t border-black/[0.07] pt-2.5 font-mono text-[8px] uppercase tracking-[0.08em] text-black/45" aria-label="Evidence chain">
        <span>rehearsal</span><span aria-hidden="true">→</span><span>lock samples</span><span aria-hidden="true">→</span><span>report</span><span aria-hidden="true">→</span><span className="text-[#B93838]">pull request blocked</span>
      </div>
    </FigureShell>
  );
}
