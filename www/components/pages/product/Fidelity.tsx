import {
  Callout,
  FeatureGrid,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
  Stage,
} from "@/components/pages/kit";
import {
  CheckRow,
  Hairline,
  MonoLabel,
  Panel,
  StatusPill,
  Ticker,
} from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

type MeterTone = "complete" | "subset" | "gap";

const METERS: {
  label: string;
  value: string;
  note: string;
  fill: number;
  tone: MeterTone;
}[] = [
  { label: "Application services", value: "8/8", note: "reproduced", fill: 1, tone: "complete" },
  { label: "Postgres state", value: "12%", note: "sanitized referential subset", fill: 0.12, tone: "subset" },
  { label: "Background workers", value: "3/3", note: "active", fill: 1, tone: "complete" },
  { label: "Queues", value: "2/2", note: "simulated", fill: 1, tone: "complete" },
  { label: "Third-party providers", value: "7/9", note: "simulated", fill: 7 / 9, tone: "gap" },
  { label: "Infrastructure capacity", value: "25%", note: "scaled · normalized thresholds", fill: 0.25, tone: "subset" },
  { label: "Observed traffic coverage", value: "81%", note: "of production endpoint volume", fill: 0.81, tone: "complete" },
];

const MISSING = ["Twilio voice callbacks", "internal recommendations service"] as const;

function fillClass(tone: MeterTone) {
  if (tone === "complete") return "bg-[#33bf00]";
  if (tone === "gap") return "bg-amber-600";
  return "bg-black/45";
}

function FidelityMeter() {
  return (
    <Panel className="flex flex-col rounded-[12px] bg-[#f7f7f5] ring-0">
      <header className="flex flex-wrap items-end justify-between gap-x-4 gap-y-3 px-5 py-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="size-1.5 shrink-0 bg-[#33bf00]" />
            <MonoLabel className="text-black/70">Fidelity Graph</MonoLabel>
            <span className="text-black/20">·</span>
            <MonoLabel className="tabular-nums">fix-billing-184</MonoLabel>
          </div>
          <div className="mt-3 flex items-baseline gap-3">
            <p className="font-title text-[44px] leading-none tracking-tighter text-black max-md:text-[36px]">
              87%
            </p>
            <MonoLabel className="uppercase">Environment fidelity</MonoLabel>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <StatusPill tone="PASS">gate ok</StatusPill>
          <StatusPill tone="WARN">2 missing</StatusPill>
        </div>
      </header>

      <div className="px-5 pb-3">
        <div className="relative h-1.5 bg-black/8">
          <div className="absolute inset-y-0 left-0 bg-black/70" style={{ width: "87%" }} />
          <span
            className="absolute top-0 bottom-0 w-px bg-black/40"
            style={{ left: "80%" }}
            aria-hidden
          />
        </div>
        <div className="relative mt-1.5 h-4">
          <MonoLabel className="absolute left-0 tabular-nums">0</MonoLabel>
          <MonoLabel className="absolute left-[80%] -translate-x-1/2 tabular-nums text-black/55">
            policy 80%
          </MonoLabel>
          <MonoLabel className="absolute right-0 tabular-nums">100</MonoLabel>
        </div>
      </div>

      <Hairline />

      <ul>
        {METERS.map((row, i) => (
          <li key={row.label}>
            {i > 0 ? <Hairline /> : null}
            <MeterRow {...row} />
          </li>
        ))}
      </ul>

      <Hairline />

      <div className="border-l-2 border-amber-600 bg-amber-50 px-5 py-3.5">
        <MonoLabel className="mb-2 block uppercase text-amber-800">Missing</MonoLabel>
        <ul className="space-y-1.5">
          {MISSING.map((item) => (
            <li key={item}>
              <CheckRow ok="warn" className="text-amber-900">
                {item}
              </CheckRow>
            </li>
          ))}
        </ul>
      </div>
    </Panel>
  );
}

function MeterRow({
  label,
  value,
  note,
  fill,
  tone,
}: (typeof METERS)[number]) {
  return (
    <div className="px-5 py-3">
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-[13px] tracking-extra-tight text-black">{label}</span>
        <Ticker className="text-[12px] text-black/60" value={value} />
      </div>
      <div className="relative mt-2 h-1 bg-black/8">
        <div
          className={cn("absolute inset-y-0 left-0", fillClass(tone))}
          style={{ width: `${Math.min(1, Math.max(0, fill)) * 100}%` }}
        />
      </div>
      <MonoLabel className="mt-1.5 block">{note}</MonoLabel>
    </div>
  );
}

function PolicyPanel() {
  return (
    <Panel className="flex flex-col rounded-[12px] bg-white">
      <div className="flex items-center justify-between gap-3 px-5 py-3">
        <MonoLabel className="uppercase text-black/55">Release policy</MonoLabel>
        <StatusPill tone="PASS">not fired</StatusPill>
      </div>
      <Hairline />
      <div className="px-5 py-4">
        <p className="text-[15px] leading-6 tracking-extra-tight text-black">
          Require approval if fidelity is below 80%.
        </p>
        <p className="mt-1 font-mono text-[12px] tabular-nums tracking-extra-tight text-black/45">
          this run 87% · threshold 80%
        </p>
      </div>
      <Hairline />
      <div className="border-l-2 border-amber-600 bg-amber-50 px-5 py-4">
        <MonoLabel className="mb-2 block uppercase text-amber-800">Named gaps</MonoLabel>
        <ul className="space-y-2">
          {MISSING.map((item) => (
            <li key={item}>
              <CheckRow ok="warn" className="text-[12px] text-amber-900">
                {item}
              </CheckRow>
            </li>
          ))}
        </ul>
        <p className="mt-3 text-[13px] leading-5 tracking-extra-tight text-amber-900/80">
          The score is above the gate. The missing providers are still listed, not absorbed into 87%.
        </p>
      </div>
    </Panel>
  );
}

export function FidelityPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Fidelity Graph"
        title="Say what the twin actually reproduced."
        lead="Application services, databases, volume, queues, jobs, third parties, secrets, network, capacity, and traffic shape. Fidelity is a number you can gate on — not a magical truth score."
        visual={
          <Stage className="overflow-hidden">
            <FidelityMeter />
          </Stage>
        }
      />
      <PageSection>
        <PageHeading title="<strong>Inspectable components.</strong> Reproduced, simulated, subset, or missing — named, not hidden." />
        <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          A 12% referential subset is not a failure. A missing Twilio adapter is. The graph is a
          transparent summary of what the twin covered, so a policy can require approval without
          pretending the clone is complete.
        </p>
        <FeatureGrid
          items={[
            { title: "Application services", body: "Counted as reproduced or missing — 8/8 in the example, never implied." },
            { title: "Postgres state", body: "Volume, distribution, sanitization, and subset ratio. 12% can still be referential." },
            { title: "Workers and queues", body: "Active, simulated, or absent. Background jobs are first-class." },
            { title: "Third parties", body: "Simulated, blocked, or approved read-only. 7/9 is a gap with names." },
            { title: "Capacity", body: "Scaled with normalized thresholds — not a fake identical fleet." },
            { title: "Traffic shape", body: "Observed endpoint coverage versus production volume." },
          ]}
        />
      </PageSection>
      <PageSection tone="white">
        <Split visual={<PolicyPanel />}>
          <PageHeading title="<strong>Gate the number. Inspect the rows.</strong> 87% does not hide Twilio." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Teams can require approval if fidelity is below a policy threshold. Guessing is not a
            fidelity score. The missing list is the part that keeps the percentage honest.
          </p>
        </Split>
      </PageSection>
      <PageSection tone="sage">
        <PageHeading
          kicker="Honesty"
          title="<strong>Narrow adapters should feel complete.</strong> A compatibility list is not a fidelity model."
        />
        <div className="mt-12 max-w-[720px]">
          <Callout label="Publish the gaps">
            A broad compatibility list with unreliable connectors would destroy trust. Publish a
            fidelity model rather than pretending unsupported components are reproduced.
          </Callout>
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/twins", title: "Isolated Twin", description: "What the orchestrator attempted to copy." },
          { href: "/product/report", title: "Safety Report", description: "Fidelity as a gate." },
          { href: "/product/architecture", title: "Architecture", description: "Why some things stay out of the twin." },
        ]}
      />
    </PageShell>
  );
}
