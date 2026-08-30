import {
  Callout,
  FeatureGrid,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
} from "@/components/pages/kit";
import { OracleScene } from "@/components/home/visuals/OracleScene";
import {
  CheckRow,
  Hairline,
  MonoLabel,
  Panel,
  QueueChip,
  StatusPill,
  Ticker,
} from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

type GateTone = "PASS" | "WARN" | "BLOCK";

type CompareRow = {
  signal: string;
  unit?: string;
  baseline: string;
  candidate: string;
  delta: string;
  tone: GateTone;
  declared?: boolean;
};

const COMPARE_ROWS: CompareRow[] = [
  { signal: "HTTP", baseline: "200", candidate: "200", delta: "—", tone: "PASS" },
  { signal: "DB write", unit: "subscriptions", baseline: "1", candidate: "2", delta: "+1", tone: "BLOCK" },
  { signal: "Queue emit", baseline: "1", candidate: "2", delta: "+1", tone: "BLOCK" },
  { signal: "Stripe effect", baseline: "1", candidate: "2", delta: "+1", tone: "BLOCK" },
  { signal: "p99", unit: "ms", baseline: "820", candidate: "6900", delta: "+8.4×", tone: "BLOCK" },
  { signal: "Deserialize", unit: "errors", baseline: "0", candidate: "14", delta: "+14", tone: "BLOCK" },
  {
    signal: "access_tier",
    baseline: "—",
    candidate: "pro",
    delta: "declared",
    tone: "PASS",
    declared: true,
  },
];

const LINKAGE: { id: string; value: string; hot?: boolean }[] = [
  { id: "change_id", value: "chg_20260824" },
  { id: "twin_id", value: "tw_08f2" },
  { id: "scenario", value: "impatient_upgrade" },
  { id: "identity", value: "returning_pro_user" },
  { id: "trace", value: "tr_c91e4a" },
  { id: "txn", value: "9f2a", hot: true },
  { id: "effect_id", value: "ch_sim_08f3", hot: true },
];

const CAUSAL_PATH = ["failure", "call", "query", "lock", "migration", "effect"] as const;

function CompareBoard() {
  return (
    <Panel className="rounded-[12px] bg-white">
      <div className="grid grid-cols-2 max-xl:grid-cols-1">
        <div className="border-r border-black/10 px-6 py-5 max-xl:border-r-0 max-xl:border-b">
          <MonoLabel>BASELINE</MonoLabel>
          <div className="mt-4 flex items-end justify-between gap-4">
            <div>
              <div className="font-mono text-[11px] tracking-extra-tight text-black/40">p99</div>
              <Ticker className="text-[18px] leading-none text-black" value="820" />
              <span className="ml-1 font-mono text-[11px] text-black/35">ms</span>
            </div>
            <div className="text-right">
              <div className="font-mono text-[11px] tracking-extra-tight text-black/40">stripe</div>
              <Ticker className="text-[18px] leading-none text-black" value="1" />
            </div>
          </div>
          <div className="mt-4 font-mono text-[11px] tabular-nums tracking-extra-tight text-black/35">
            nresp 7c1a9e2b
          </div>
        </div>
        <div className="px-6 py-5">
          <div className="flex items-center justify-between gap-3">
            <MonoLabel className="text-black">CANDIDATE</MonoLabel>
            <StatusPill tone="BLOCK">BLOCK</StatusPill>
          </div>
          <div className="mt-4 flex items-end justify-between gap-4">
            <div>
              <div className="font-mono text-[11px] tracking-extra-tight text-black/40">p99</div>
              <Ticker className="text-[18px] leading-none text-red-700" value="6900" />
              <span className="ml-1 font-mono text-[11px] text-black/35">ms</span>
            </div>
            <div className="text-right">
              <div className="font-mono text-[11px] tracking-extra-tight text-black/40">stripe</div>
              <Ticker className="text-[18px] leading-none text-red-700" value="2" />
            </div>
          </div>
          <div className="mt-4 font-mono text-[11px] tabular-nums tracking-extra-tight text-red-700">
            nresp e04f3a91
          </div>
        </div>
      </div>

      <Hairline className="block" />

      <div className="grid grid-cols-[minmax(0,1.4fr)_1fr_1fr_minmax(72px,0.7fr)_auto] items-center gap-x-3 px-6 py-2.5 max-xl:grid-cols-[1fr_1fr_auto] max-md:hidden">
        <MonoLabel>signal</MonoLabel>
        <MonoLabel className="max-xl:hidden">baseline</MonoLabel>
        <MonoLabel>candidate</MonoLabel>
        <MonoLabel className="max-xl:hidden">delta</MonoLabel>
        <MonoLabel className="text-right">gate</MonoLabel>
      </div>

      <Hairline className="block max-md:hidden" />

      <ul>
        {COMPARE_ROWS.map((row) => (
          <li
            key={row.signal}
            className={cn(
              "grid grid-cols-[minmax(0,1.4fr)_1fr_1fr_minmax(72px,0.7fr)_auto] items-center gap-x-3 border-b border-black/8 px-6 py-3 last:border-0 max-xl:grid-cols-[1fr_1fr_auto] max-md:grid-cols-1 max-md:gap-y-1",
              row.declared && "bg-[#33bf00]/8",
            )}
          >
            <div className="min-w-0">
              <div className="font-mono text-[13px] tracking-extra-tight text-black">{row.signal}</div>
              {row.unit ? (
                <div className="mt-0.5 font-mono text-[10px] tracking-extra-tight text-black/35">{row.unit}</div>
              ) : null}
            </div>
            <div className="font-mono text-[13px] tabular-nums tracking-extra-tight text-black/45 max-xl:hidden">
              {row.baseline}
            </div>
            <div
              className={cn(
                "font-mono text-[13px] tabular-nums tracking-extra-tight",
                row.declared ? "text-[#0a7a56]" : row.tone === "BLOCK" ? "text-red-700" : "text-black",
              )}
            >
              {row.candidate}
            </div>
            <div
              className={cn(
                "font-mono text-[12px] tabular-nums tracking-extra-tight max-xl:hidden",
                row.declared ? "text-[#0a7a56]" : row.tone === "BLOCK" ? "text-red-700" : "text-black/35",
              )}
            >
              {row.delta}
            </div>
            <div className="justify-self-end">
              <StatusPill tone={row.tone}>{row.declared ? "declared" : row.tone}</StatusPill>
            </div>
          </li>
        ))}
      </ul>

      <div className="flex flex-wrap items-center justify-between gap-3 border-t border-black/10 px-6 py-3">
        <div className="flex flex-wrap items-center gap-5">
          <CheckRow ok={false} className="text-black/70">
            <span className="tabular-nums">5 unexpected</span>
          </CheckRow>
          <CheckRow ok={true} className="text-black/70">
            <span>
              1 declared · <span className="text-[#0a7a56]">access_tier</span>
            </span>
          </CheckRow>
        </div>
        <MonoLabel className="hidden sm:inline">intended change ≠ regression</MonoLabel>
      </div>
    </Panel>
  );
}

function LinkageTrace() {
  return (
    <Panel className="rounded-[12px] bg-white">
      <div className="flex items-center justify-between gap-3 border-b border-black/10 px-5 py-3">
        <MonoLabel>CAUSAL LINKAGE</MonoLabel>
        <MonoLabel>keyed artifacts · one failure</MonoLabel>
      </div>

      <div className="flex flex-wrap">
        {LINKAGE.map((field, i) => (
          <div key={field.id} className="flex min-w-[140px] flex-1 items-stretch max-xl:min-w-[50%] max-md:min-w-full">
            {i > 0 ? <Hairline vertical className="max-xl:hidden" /> : null}
            <div
              className={cn(
                "min-w-0 flex-1 px-5 py-4",
                field.hot && "bg-[#33bf00]/12",
              )}
            >
              <MonoLabel className={field.hot ? "text-black/70" : undefined}>
                {field.id}
              </MonoLabel>
              <div className="mt-1.5 truncate font-mono text-[13px] tabular-nums tracking-extra-tight text-black">
                {field.value}
              </div>
            </div>
          </div>
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-2 border-t border-black/10 px-5 py-3">
        {CAUSAL_PATH.map((step, i) => (
          <span key={step} className="flex items-center gap-2">
            {i > 0 ? (
              <span className="font-mono text-[10px] text-black/25" aria-hidden>
                →
              </span>
            ) : null}
            <QueueChip>{step}</QueueChip>
          </span>
        ))}
      </div>
    </Panel>
  );
}

export function OraclePage() {
  return (
    <PageShell inset>
      <PageHero
        path="/product/oracle"
        eyebrow="Differential Oracle"
        title="Same state. Same behavior. Two versions."
        lead="Compare normalized HTTP, status classes, database writes, events, traces, query plans, latency, journeys, and logs. Declared expected differences do not count as regressions."
        framed={false}
        visual={<OracleScene />}
      />

      <PageSection tone="white">
        <PageHeading title="<strong>Unexpected diffs block.</strong> Declared diffs do not." />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Baseline and candidate receive equivalent state and behavior. The oracle reports a delta on
          every signal, then a pass, warning, or block — not a preview URL.
        </p>
        <div className="mt-12">
          <CompareBoard />
        </div>
        <div className="mt-10">
          <Callout label="Expected differences">
            Intended product changes are declared so they do not appear as false regressions. A new
            <span className="text-black"> access_tier </span>
            field in JSON is allowed. A duplicate Stripe charge is not.
          </Callout>
        </div>
      </PageSection>

      <PageSection>
        <PageHeading title="<strong>The durable layer is the decision.</strong> Understand, reproduce, compare, attribute, recommend." />
        <FeatureGrid
          items={[
            { title: "HTTP", body: "Normalized responses, status codes, and error classes." },
            { title: "Data", body: "Database writes, events, and queue emissions." },
            { title: "Effects", body: "Third-party effects captured by the firewall ledger." },
            { title: "Runtime", body: "Trace topology, query count and plans, latency, resources." },
            { title: "Journeys", body: "User-journey outcomes, logs, and exceptions." },
            { title: "Expected diffs", body: "Intended product changes are declared so they are not false regressions." },
          ]}
        />
      </PageSection>

      <PageSection tone="sage">
        <PageHeading title="<strong>Linkage.</strong> A user-visible failure traces back through a call, query, lock, migration, and attempted effect." />
        <div className="mt-12">
          <LinkageTrace />
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/report", title: "Safety Report", description: "The oracle’s output is the gate." },
          { href: "/product/workload", title: "Workload Studio", description: "Equivalent behavior for both versions." },
          { href: "/product/twins", title: "Isolated Twin", description: "Baseline and candidate in one run." },
        ]}
      />
    </PageShell>
  );
}
