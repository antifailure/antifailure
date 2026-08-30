import { PageHeading, PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { ReportScene } from "@/components/home/visuals/ReportScene";
import { Hairline, Panel, StatusPill } from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

type Tone = "PASS" | "WARN" | "BLOCK";

const GATES: {
  tone: Tone;
  pr: string;
  title: string;
  evidence: string;
  merge: string;
}[] = [
  {
    tone: "PASS",
    pr: "pr/182",
    title: "expand-and-contract",
    evidence: "lock 0.4s · rollback feasible · fidelity 87%",
    merge: "Merge ready",
  },
  {
    tone: "WARN",
    pr: "pr/183",
    title: "index on access_tier",
    evidence: "p95 +18% · checkout intact · no exclusive lock",
    merge: "Merge with approval",
  },
  {
    tone: "BLOCK",
    pr: "pr/184",
    title: "add billing_status default",
    evidence: "ACCESS EXCLUSIVE 27.4s · rolling rollback unsafe",
    merge: "Merge disabled",
  },
];

const CI_CHECKS: { name: string; time: string }[] = [
  { name: "Lint / typecheck", time: "12s" },
  { name: "Unit tests", time: "1m 04s" },
  { name: "Docker build", time: "2m 11s" },
];

const EVIDENCE: { label: string; value: string; tone: Tone; pill?: string }[] = [
  { label: "Exclusive lock", value: "27.4s ACCESS EXCLUSIVE on subscriptions · policy 2s", tone: "BLOCK" },
  { label: "Unknown egress", value: "0 attempts · attempted-effect ledger clean", tone: "PASS" },
  { label: "p95 latency", value: "+18% vs baseline under equivalent traffic · policy +15%", tone: "WARN" },
  { label: "Fidelity", value: "87% reproduced · approval threshold 80%", tone: "PASS" },
  { label: "Sanitization", value: "attested inside the customer boundary", tone: "PASS" },
  { label: "Cleanup proof", value: "14/14 destroyed · sha256:c7e91a4b0e2f14f0", tone: "PASS" },
];

const POLICIES: { tone: Tone; pill: string; rule: string; title: string; body: string }[] = [
  {
    tone: "BLOCK",
    pill: "BLOCK",
    rule: "lock > 2s",
    title: "Exclusive lock on a critical table",
    body: "Block if an exclusive lock exceeds two seconds. A 27-second ACCESS EXCLUSIVE on subscriptions is a merge block, not a log line.",
  },
  {
    tone: "BLOCK",
    pill: "BLOCK",
    rule: "unknown egress",
    title: "Unknown external egress",
    body: "Block if the twin attempts an unknown destination. Fail closed. Convenience must not silently override containment.",
  },
  {
    tone: "WARN",
    pill: "WARN",
    rule: "p95 +15%",
    title: "Latency against baseline",
    body: "Warn if candidate p95 increases by more than 15% versus baseline under equivalent traffic.",
  },
  {
    tone: "WARN",
    pill: "APPROVE",
    rule: "fidelity < 80%",
    title: "Fidelity below threshold",
    body: "Require approval if twin fidelity is below 80%. A number you cannot inspect is not a fidelity score.",
  },
];

function ToneDot({ tone }: { tone: Tone }) {
  return (
    <span
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        tone === "PASS" && "bg-[#33bf00]",
        tone === "WARN" && "bg-amber-600",
        tone === "BLOCK" && "bg-red-600",
      )}
      aria-hidden
    />
  );
}

function GateCard({ tone, pr, title, evidence, merge }: (typeof GATES)[number]) {
  return (
    <Panel className="rounded-[12px] bg-white">
      <div className="flex items-center justify-between gap-3 px-4 py-2.5">
        <span className="font-mono text-[11px] tracking-extra-tight text-black/45">{pr}</span>
        <StatusPill tone={tone} />
      </div>
      <Hairline />
      <div className="px-4 py-4">
        <div className="font-mono text-[13px] tracking-extra-tight text-black">{title}</div>
        <p className="mt-1.5 font-mono text-[11px] leading-4 tracking-extra-tight text-black/50">{evidence}</p>
      </div>
      <Hairline />
      <div
        className={cn(
          "px-4 py-2.5 font-mono text-[10px] tracking-extra-tight",
          tone === "PASS" && "text-[#285D49]",
          tone === "WARN" && "text-amber-800",
          tone === "BLOCK" && "text-black/35",
        )}
      >
        {merge}
      </div>
    </Panel>
  );
}

function PrCheckChrome() {
  return (
    <Panel className="rounded-[12px] bg-white">
      <div className="flex items-center justify-between gap-4 px-4 py-2.5">
        <div className="flex min-w-0 items-center gap-2.5">
          <span className="inline-flex items-center border border-black/[0.08] px-1.5 py-0.5 font-mono text-[10px] tracking-extra-tight text-black/70">
            pr/184
          </span>
          <span className="truncate font-mono text-[13px] tracking-extra-tight text-black">add access_tier</span>
        </div>
        <div className="flex items-center gap-2">
          <span className="font-mono text-[10px] tracking-extra-tight text-black/40">required · 1 of 4</span>
          <StatusPill tone="BLOCK">BLOCK</StatusPill>
        </div>
      </div>
      <Hairline />

      <ul>
        {CI_CHECKS.map((check) => (
          <li key={check.name} className="flex items-center justify-between gap-3 px-4 py-2">
            <div className="flex min-w-0 items-center gap-2 font-mono text-[12px] tracking-extra-tight text-black/40">
              <ToneDot tone="PASS" />
              <span className="truncate">{check.name}</span>
            </div>
            <div className="flex items-center gap-2">
              <span className="font-mono text-[10px] tabular-nums tracking-extra-tight text-black/30">{check.time}</span>
              <StatusPill tone="PASS" />
            </div>
          </li>
        ))}
      </ul>

      <Hairline />

      <div className="px-4 py-3">
          <div className="flex items-start justify-between gap-3">
            <div className="flex min-w-0 items-start gap-2">
              <ToneDot tone="BLOCK" />
              <div className="min-w-0">
                <div className="font-mono text-[12px] tracking-extra-tight text-black">
                  Antifailure / deployment safety
                </div>
                <div className="mt-0.5 font-mono text-[10px] tracking-extra-tight text-black/40">
                  candidate vs baseline · 4m 12s
                </div>
              </div>
            </div>
            <StatusPill tone="BLOCK">BLOCK</StatusPill>
          </div>

          <p className="mt-4 font-mono text-[13px] tracking-extra-tight text-black">
            BLOCKED: unsafe schema migration
          </p>
          <p className="mt-2 max-w-[640px] font-mono text-[11px] leading-5 tracking-extra-tight text-black/65">
            Migration 20260824_add_billing_status held an ACCESS EXCLUSIVE lock on subscriptions for 27.4s.
            Checkout p99 820ms → 6.9s; 11.8% of upgrades timed out. Previous binary cannot deserialize
            candidate rows — rolling rollback is unsafe.
          </p>
          <div className="mt-3">
            <div className="font-mono text-[10px] uppercase tracking-[0.14em] text-black/35">Suggested action</div>
            <p className="mt-1 font-mono text-[11px] leading-5 tracking-extra-tight text-black">
              nullable, no default · batch backfill · dual-read compatibility · constrain later
            </p>
          </div>
        </div>

      <Hairline />

      <div className="px-4 py-2">
        <div className="font-mono text-[10px] uppercase tracking-[0.14em] text-black/35">Evidence</div>
      </div>
      <Hairline />
      {EVIDENCE.map((row, i) => (
        <div key={row.label}>
          {i > 0 ? <Hairline /> : null}
          <div className="flex items-center justify-between gap-4 px-4 py-2.5">
            <div className="min-w-0">
              <div className="font-mono text-[12px] tracking-extra-tight text-black">{row.label}</div>
              <div className="mt-0.5 font-mono text-[11px] tracking-extra-tight text-black/45">{row.value}</div>
            </div>
            <StatusPill tone={row.tone}>{row.pill ?? row.tone}</StatusPill>
          </div>
        </div>
      ))}

      <Hairline />
      <div className="flex items-center justify-between gap-3 px-4 py-3">
        <div>
          <div className="font-mono text-[12px] tracking-extra-tight text-black/35">Merge pull request</div>
          <div className="mt-0.5 font-mono text-[10px] tracking-extra-tight text-black/30">
            inert · required check BLOCKED
          </div>
        </div>
        <span className="border border-black/[0.08] bg-white px-2.5 py-1 font-mono text-[10px] tracking-extra-tight text-black/30">
          Merge
        </span>
      </div>
    </Panel>
  );
}

export function ReportPage() {
  return (
    <PageShell>
      <PageHero
        path="/product/report"
        eyebrow="Safety Report and Release Gate"
        title="Pass, warning, or block. With evidence."
        lead="Overall decision, fidelity, migration findings, functional and performance regressions, attempted external effects, sanitization status, and cleanup proof."
      />

      <PageSection>
        <PageHeading
          kicker="Required check"
          title="<strong>It looks like a GitHub check</strong> because that is the product surface."
        />
        <ReportScene />
      </PageSection>

      <PageSection>
        <PageHeading title="<strong>Attached to the pull request.</strong> Not a dataset. Not a preview URL." />
        <p className="mt-6 max-w-[540px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          “Error rate increased” is insufficient. The report connects the change, the behavior, the
          affected workflow, the evidence, and the safer pattern — then marks the check.
        </p>
        <div className="mt-12 grid grid-cols-3 gap-5 max-lg:grid-cols-1">
          {GATES.map((gate) => (
            <GateCard key={gate.pr} {...gate} />
          ))}
        </div>
        <div className="mt-5">
          <PrCheckChrome />
        </div>
      </PageSection>

      <PageSection tone="white">
        <PageHeading title="<strong>Policy is the product surface</strong> for platform teams." />
        <p className="mt-6 max-w-[540px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Reports that are noisy get ignored. Baseline comparisons, expected-difference declarations, and
          severity policies keep the gate useful. Enforce them organization-wide.
        </p>
        <div className="mt-12 grid grid-cols-2 gap-5 max-md:grid-cols-1">
          {POLICIES.map((policy) => (
            <Panel key={policy.rule} className="rounded-[12px] bg-white">
              <div className="flex items-center justify-between gap-3 px-5 py-3">
                <span className="font-mono text-[11px] tracking-extra-tight text-black/45">{policy.rule}</span>
                <StatusPill tone={policy.tone}>{policy.pill}</StatusPill>
              </div>
              <Hairline />
              <div className="px-5 py-4">
                <h3 className="text-[18px] leading-snug tracking-extra-tight text-black">{policy.title}</h3>
                <p className="mt-2 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">{policy.body}</p>
              </div>
            </Panel>
          ))}
        </div>
      </PageSection>

      <PageSection tone="sage">
        <PageHeading title="<strong>Center the deployment decision.</strong> Environment creation, data, agents, and load are supporting systems." />
        <div className="mt-8 flex items-center gap-2">
          <StatusPill tone="PASS" />
          <StatusPill tone="WARN" />
          <StatusPill tone="BLOCK" />
        </div>
        <p className="mt-8 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Every workflow and report answers whether this deployment is safe to ship under the conditions
          that actually matter. If the product becomes a bundle of tools, it has failed.
        </p>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/oracle", title: "Differential Oracle", description: "Where comparisons are produced." },
          { href: "/product/fidelity", title: "Fidelity Graph", description: "What the twin actually reproduced." },
          { href: "/solutions/release-gates", title: "Release gates", description: "Organization-wide ship policy." },
        ]}
      />
    </PageShell>
  );
}
