import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
} from "@/components/pages/kit";
import {
  CheckRow,
  Hairline,
  LockBadge,
  MonoLabel,
  Node,
  Panel,
  StatusPill,
} from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

const DIFF_FILES = [
  { path: "api/billing.ts", plus: 42, tag: "service", hot: false },
  { path: "workers/billing.ts", plus: 18, tag: "worker", hot: false },
  { path: "migrations/20260824_add_billing_status.sql", plus: 1, tag: "migration", hot: true },
  { path: "events/subscription.ts", plus: 9, tag: "schema", hot: false },
] as const;

const SERVICES = [
  { name: "billing-api", lit: true },
  { name: "billing-worker", lit: true },
  { name: "checkout", lit: true },
  { name: "catalog", lit: false },
  { name: "recommendations", lit: false },
] as const;

const PLAN = [
  "restore sanitized 12% referential subset",
  "run migration under checkout traffic",
  "dual-read compatibility on subscriptions",
  "compile upgrade + checkout journeys",
  "destroy on expiry or PR close",
] as const;

const FIDELITY_ROWS = [
  { label: "services", value: "3/8 in scope", fill: 0.38 },
  { label: "postgres", value: "12% subset", fill: 0.12 },
  { label: "stripe", value: "simulated", fill: 1 },
  { label: "twilio", value: "skip", fill: 0 },
] as const;

const FLOW = [
  {
    title: "Analyze",
    body: "Code, infrastructure, and migration diff. Risk profile assigned.",
    kind: "analyze" as const,
  },
  {
    title: "Create",
    body: "Create Wind Tunnel, or policy triggers it. Isolated twin.",
    kind: "create" as const,
  },
  {
    title: "Exercise",
    body: "Baseline and candidate receive equivalent workloads.",
    kind: "exercise" as const,
  },
  {
    title: "Decide",
    body: "Report attached. Pass, warning, or block. Then destroy.",
    kind: "decide" as const,
  },
] as const;

function FidBar({ fill }: { fill: number }) {
  return (
    <span className="relative h-px flex-1 bg-black/10">
      {fill > 0 ? (
        <span
          className="absolute inset-y-0 left-0 bg-[#33bf00]"
          style={{ width: `${Math.round(fill * 100)}%`, height: 1 }}
        />
      ) : (
        <span className="absolute inset-y-0 left-0 w-full bg-black/[0.06]" style={{ height: 1 }} />
      )}
    </span>
  );
}

function RiskPlanMock() {
  return (
    <Panel className="h-full min-h-[400px] rounded-[12px] bg-white ring-0 max-md:min-h-0" aria-hidden>
      <div className="flex items-start justify-between gap-3 px-4 py-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <MonoLabel className="uppercase">PR risk plan</MonoLabel>
            <span className="font-mono text-[11px] tracking-extra-tight text-black/35">#184</span>
          </div>
          <div className="mt-1 truncate font-mono text-[13px] tracking-extra-tight text-black">
            add billing_status to subscriptions
          </div>
          <MonoLabel className="mt-0.5 block">acme/billing · 4 files · opened 2h ago</MonoLabel>
        </div>
        <StatusPill tone="WARN">HIGH</StatusPill>
      </div>
      <Hairline />

      <div className="px-4 py-2.5">
        <div className="mb-2 flex items-center justify-between">
          <MonoLabel className="uppercase">Diff</MonoLabel>
          <MonoLabel>migration detected</MonoLabel>
        </div>
        <ul>
          {DIFF_FILES.map((file) => (
            <li
              key={file.path}
              className="flex items-center gap-2 px-1.5 py-1 font-mono text-[11px] tracking-extra-tight"
            >
              <span className="w-px self-stretch bg-transparent" aria-hidden />
              <span className={cn("min-w-0 flex-1 truncate", file.hot ? "text-black" : "text-black/55")}>
                {file.path}
              </span>
              <span className={cn("tabular-nums", file.hot ? "text-red-700" : "text-black/35")}>
                +{file.plus}
              </span>
              {file.hot ? (
                <LockBadge exclusive />
              ) : (
                <span className="text-black/35">{file.tag}</span>
              )}
            </li>
          ))}
        </ul>
      </div>
      <Hairline />

      <div className="grid grid-cols-2 max-xl:grid-cols-1">
        <div className="px-4 py-3">
          <MonoLabel className="uppercase">Services affected</MonoLabel>
          <ul className="mt-2 flex flex-col gap-1.5">
            {SERVICES.map((svc) => (
              <li key={svc.name}>
                <Node label={svc.name} lit={svc.lit} />
              </li>
            ))}
          </ul>
        </div>
        <div className="border-l border-black/10 px-4 py-3 max-xl:border-l-0 max-xl:border-t">
          <div className="flex items-center justify-between gap-2">
            <MonoLabel className="uppercase">Fidelity recommended</MonoLabel>
            <span className="font-mono text-[13px] tabular-nums tracking-extra-tight text-black">87%</span>
          </div>
          <div className="mt-2 h-px bg-black/10">
            <div className="h-px w-[87%] bg-[#33bf00]" />
          </div>
          <ul className="mt-2.5 flex flex-col gap-1.5">
            {FIDELITY_ROWS.map((row) => (
              <li
                key={row.label}
                className="flex items-center gap-2 font-mono text-[10px] tabular-nums tracking-extra-tight text-black/50"
              >
                <span className="w-[52px] shrink-0 text-black/70">{row.label}</span>
                <FidBar fill={row.fill} />
                <span className="w-[72px] text-right">{row.value}</span>
              </li>
            ))}
          </ul>
        </div>
      </div>
      <Hairline />

      <div className="px-4 py-3">
        <MonoLabel className="uppercase">Validation plan</MonoLabel>
        <ul className="mt-2 flex flex-col gap-1">
          {PLAN.map((step) => (
            <li key={step}>
              <CheckRow ok="run" className="text-black/70">
                <span>{step}</span>
              </CheckRow>
            </li>
          ))}
        </ul>
        <div className="mt-3 flex flex-wrap items-center gap-1.5">
          <MonoLabel>workflows</MonoLabel>
          {["checkout", "upgrade", "billing worker"].map((w) => (
            <span
              key={w}
              className="inline-flex items-center border border-black/[0.08] px-1.5 py-0.5 font-mono text-[10px] tracking-extra-tight text-black/60"
            >
              {w}
            </span>
          ))}
        </div>
      </div>
    </Panel>
  );
}

function ScopePanel() {
  return (
    <Panel className="rounded-[12px] bg-white">
      <div className="flex items-center justify-between px-5 py-3">
        <MonoLabel className="uppercase">Test what this change needs</MonoLabel>
        <StatusPill tone="WARN">HIGH</StatusPill>
      </div>
      <Hairline />
      <div className="grid grid-cols-2 max-xl:grid-cols-1">
        <div className="px-5 py-4">
          <MonoLabel className="uppercase text-[#285D49]">Include</MonoLabel>
          <ul className="mt-3 flex flex-col gap-2">
            <li>
              <CheckRow ok>
                <span className="text-black/70">billing-api · billing-worker</span>
              </CheckRow>
            </li>
            <li>
              <CheckRow ok>
                <span className="text-black/70">postgres 12% referential subset</span>
              </CheckRow>
            </li>
            <li>
              <CheckRow ok>
                <span className="text-black/70">checkout + upgrade journeys</span>
              </CheckRow>
            </li>
            <li>
              <CheckRow ok>
                <span className="text-black/70">Stripe simulator</span>
              </CheckRow>
            </li>
          </ul>
        </div>
        <div className="border-l border-black/10 px-5 py-4 max-xl:border-t max-xl:border-l-0">
          <MonoLabel className="uppercase">Skip</MonoLabel>
          <ul className="mt-3 flex flex-col gap-2">
            <li>
              <CheckRow className="text-black/45">catalog · recommendations</CheckRow>
            </li>
            <li>
              <CheckRow className="text-black/45">Twilio voice callbacks</CheckRow>
            </li>
            <li>
              <CheckRow className="text-black/45">full production volume</CheckRow>
            </li>
            <li>
              <CheckRow className="text-black/45">identical infra capacity</CheckRow>
            </li>
          </ul>
        </div>
      </div>
      <Hairline />
      <div className="flex items-center justify-between gap-3 px-5 py-3">
        <MonoLabel>fidelity recommended — not a full clone</MonoLabel>
        <span className="font-mono text-[15px] tabular-nums tracking-extra-tight text-black">87%</span>
      </div>
    </Panel>
  );
}

function MiniAnalyze() {
  return (
    <div className="flex flex-col gap-1 font-mono text-[10px] tracking-extra-tight text-black/45">
      <span>api/billing.ts</span>
      <span className="flex items-center justify-between gap-1 bg-red-600/[0.06] px-1 py-0.5 text-black">
        migrate.sql
        <StatusPill tone="WARN">HIGH</StatusPill>
      </span>
      <span>events/subscription.ts</span>
    </div>
  );
}

function MiniCreate() {
  return (
    <div className="flex flex-col gap-2">
      <span className="inline-flex w-fit items-center bg-black px-2 py-1 font-mono text-[10px] tracking-extra-tight text-white">
        Create Wind Tunnel
      </span>
      <MonoLabel>TTL 4h · budget $18</MonoLabel>
    </div>
  );
}

function MiniExercise() {
  return (
    <div className="font-mono text-[10px] tracking-extra-tight text-black/55">
      <div className="flex justify-between">
        <MonoLabel className="uppercase">base</MonoLabel>
        <MonoLabel className="uppercase">cand</MonoLabel>
      </div>
      <div className="mt-1.5 flex items-center justify-between gap-1">
        <span>p99 820</span>
        <span className="text-black/25">=</span>
        <span>p99 820</span>
      </div>
      <div className="mt-1 flex items-center justify-between gap-1">
        <span>http 200</span>
        <span className="text-black/25">=</span>
        <span>http 200</span>
      </div>
    </div>
  );
}

function MiniDecide() {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <StatusPill tone="BLOCK">BLOCK</StatusPill>
        <MonoLabel>on PR #184</MonoLabel>
      </div>
      <CheckRow ok>
        <span className="text-black/60">destroyed 14/14</span>
      </CheckRow>
    </div>
  );
}

function FlowMini({ kind }: { kind: (typeof FLOW)[number]["kind"] }) {
  return (
    <div className="mt-5 min-h-[88px] border border-black/[0.08] bg-white p-3">
      {kind === "analyze" ? <MiniAnalyze /> : null}
      {kind === "create" ? <MiniCreate /> : null}
      {kind === "exercise" ? <MiniExercise /> : null}
      {kind === "decide" ? <MiniDecide /> : null}
    </div>
  );
}

function FlowRail() {
  return (
    <ol className="relative mt-16 grid grid-cols-4 gap-x-16 gap-y-10 max-xl:grid-cols-2 max-md:mt-10 max-md:grid-cols-1">
      {FLOW.map((step) => (
        <li key={step.title} className="min-w-0">
          <div className="mb-4 size-2 rounded-full bg-black" />
          <h3 className="text-[18px] tracking-extra-tight text-black">{step.title}</h3>
          <p className="mt-2 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">{step.body}</p>
          <FlowMini kind={step.kind} />
        </li>
      ))}
    </ol>
  );
}

export function ChangeIntelligencePage() {
  return (
    <PageShell inset>
      <PageHero
        path="/product/change-intelligence"
        eyebrow="Change Intelligence"
        title="Test what matters for this change."
        lead="Analyze the pull request for affected services, migrations, infrastructure, APIs, events, and critical workflows. Recommend twin fidelity and a validation plan."
        visual={<RiskPlanMock />}
      />
      <PageSection>
        <Split visual={<ScopePanel />}>
          <PageHeading title="<strong>Don’t blindly reproduce everything.</strong> Cost comes from testing what does not matter." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            The module reads the diff — services, schema, contracts, infrastructure — and names the
            workflows that can actually break. Universal cloning would consume the company. A
            recommended fidelity is more trustworthy than pretending every topology is in scope.
          </p>
        </Split>
      </PageSection>
      <PageSection tone="sage">
        <PageHeading kicker="Pull-request flow" title="<strong>From diff to destroyed environment.</strong>" />
        <FlowRail />
        <div className="mt-14">
          <Callout label="Plan before provision">
            Assign a risk profile, propose the validation plan, then create the twin — or let policy
            trigger it. The environment is destroyed when the run expires or the pull request closes.
          </Callout>
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/twins", title: "Isolated Twin", description: "What gets provisioned from the plan." },
          { href: "/product/migrations", title: "Migration Safety", description: "When the diff is a schema change." },
          { href: "/product/fidelity", title: "Fidelity Graph", description: "The fidelity the plan requested." },
        ]}
      />
    </PageShell>
  );
}
