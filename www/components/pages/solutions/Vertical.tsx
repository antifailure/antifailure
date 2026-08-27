import type { ReactNode } from "react";
import { FirewallScene } from "@/components/home/visuals/FirewallScene";
import { MigrationScene } from "@/components/home/visuals/MigrationScene";
import { ReportScene } from "@/components/home/visuals/ReportScene";
import { WorkloadScene } from "@/components/home/visuals/WorkloadScene";
import {
  Callout,
  CodePanel,
  FeatureGrid,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  SpecTable,
  Split,
  Stage,
  Steps,
} from "@/components/pages/kit";

export const SOLUTION_PAGE_SLUGS = [
  "saas",
  "fintech",
  "ecommerce",
  "marketplaces",
  "devtools",
  "platform",
  "migrations",
  "release-gates",
  "workflow",
] as const;

type Slug = (typeof SOLUTION_PAGE_SLUGS)[number];

function Metrics({
  items,
}: {
  items: { value: string; label: string }[];
}) {
  return (
    <ul className="grid grid-cols-3 gap-x-12 gap-y-10 max-md:grid-cols-1">
      {items.map((item) => (
        <li key={item.label} className="min-w-0 border-t border-black/12 pt-6">
          <div className="font-title text-[52px] leading-none tracking-tighter text-black max-xl:text-[40px] max-md:text-[36px]">
            {item.value}
          </div>
          <p className="mt-4 max-w-[280px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">{item.label}</p>
        </li>
      ))}
    </ul>
  );
}

function HeroStat({
  value,
  caption,
  tone = "light",
}: {
  value: string;
  caption: string;
  tone?: "light" | "dark" | "sage";
}) {
  return (
    <div
      className={
        tone === "dark"
          ? "rounded-[12px] bg-[#151617] p-8 text-white"
          : tone === "sage"
            ? "rounded-[12px] bg-[#E4F1EB] p-8 ring-1 ring-black/8"
            : "rounded-[12px] bg-white p-8 ring-1 ring-black/10"
      }
    >
      <div
        className={`font-title text-[72px] leading-none tracking-tighter max-md:text-[48px] ${tone === "dark" ? "text-white" : "text-black"}`}
      >
        {value}
      </div>
      <p className={`mt-5 max-w-[360px] text-[15px] leading-6 tracking-extra-tight ${tone === "dark" ? "text-white/55" : "text-gray-new-40"}`}>
        {caption}
      </p>
    </div>
  );
}

function SceneStage({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <Stage className={`[&>*]:!mt-0 ${className}`}>{children}</Stage>;
}

function SaasPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · B2B SaaS"
        title="Daily deploys. Expanding schemas. Staging that drifted years ago."
        lead="The first twin should catch the migration that locks subscriptions during peak traffic — against sanitized tenant-shaped state, not a fixture dump."
        visual={
          <div className="rounded-[12px] bg-[#E4F1EB] p-6 ring-1 ring-black/8">
            <div className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-black/45">Tenant subset</div>
            <ul className="mt-4 space-y-2">
              {[
                ["acme-prod", "12.4k seats"],
                ["northwind", "3.1k seats"],
                ["helix", "890 seats"],
              ].map(([name, seats]) => (
                <li key={name} className="flex items-center justify-between rounded-md bg-white/80 px-3 py-2.5 ring-1 ring-black/8">
                  <span className="flex items-center gap-2 text-[14px] tracking-extra-tight text-black">
                    <span className="size-1.5 rounded-full bg-[#33bf00]" />
                    {name}
                  </span>
                  <span className="font-mono text-[12px] text-black/45">{seats}</span>
                </li>
              ))}
            </ul>
            <p className="mt-4 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
              Referential accounts and billing. Production identities never enter the twin.
            </p>
          </div>
        }
      />
      <PageSection tone="sage">
        <PageHeading title="<strong>Tenant-shaped state.</strong> Checkout and seat changes against sanitized accounts." />
        <div className="mt-14">
          <Metrics
            items={[
              { value: "Daily", label: "or weekly production deploys. Staging cannot keep up with schema change." },
              { value: "N tenants", label: "Long-tail accounts and malformed historical seats that fixtures omit." },
              { value: "Old + new", label: "Application instances still running while the new column lands." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <Split
          visual={
            <SpecTable
              rows={[
                ["Restore", "Referential subset of orgs, seats, subscriptions, invoices"],
                ["Mask", "Account identifiers replaced inside the customer boundary"],
                ["Exercise", "Checkout, upgrades, and seat changes at production-shaped concurrency"],
                ["Decide", "Pass, warning, or block on the pull request — then destroy the twin"],
              ]}
            />
          }
        >
          <PageHeading title="<strong>Staging differs in too many dimensions at once.</strong>" />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            A change can pass unit, integration, end-to-end, and a manual staging check, then still fail in
            production. The twin reproduces tenant shape, concurrency, and schema coexistence — then reports
            whether the deploy is safe.
          </p>
        </Split>
        <div className="mt-16">
          <FeatureGrid
            items={[
              { title: "Tenant-shaped state", body: "Referential subsets of accounts, seats, and billing without production identities." },
              { title: "Checkout and upgrades", body: "Critical workflows under production-shaped concurrency." },
              { title: "Schema coexistence", body: "Old application instances still running while the new column lands." },
            ]}
          />
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/migrations", title: "Migration Safety", description: "The lock on subscriptions is the first finding." },
          { href: "/design-partners", title: "Design partners", description: "One real upcoming migration." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}

function FintechPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Fintech"
        title="Billing, ledgers, and side effects that must never hit live processors."
        lead="The firewall simulates Stripe. Safe State masks account identifiers. The oracle compares ledger writes. Duplicate events are incidents — they belong in a report, not in production."
        visual={
          <SceneStage className="min-h-[280px]">
            <FirewallScene />
          </SceneStage>
        }
      />
      <PageSection>
        <PageHeading title="<strong>Simulators, not live processors.</strong> Charging a card from a twin is an existential failure." />
        <FeatureGrid
          items={[
            { title: "Clone-local Stripe", body: "Payment creation is stored in a clone-local ledger. Nothing charges." },
            { title: "Email captured", body: "SendGrid and similar sinks render and store. Nothing is delivered." },
            { title: "Fail closed", body: "Unknown processors and production API hostnames are blocked and ledgered." },
            { title: "Ledger comparison", body: "The oracle compares writes, events, and third-party effects against baseline." },
            { title: "Irreversible writes", body: "Candidate billing events that old code cannot reconcile show up before ship." },
            { title: "Mid-market first", body: "Technically sophisticated billing teams. Not a regulated-enterprise procurement motion." },
          ]}
        />
      </PageSection>
      <PageSection tone="white">
        <Split
          reverse
          visual={
            <SpecTable
              rows={[
                ["Stripe payment", "Simulate and store in a clone-local ledger"],
                ["SendGrid email", "Render and capture, never deliver"],
                ["Slack webhook", "Store a message preview"],
                ["Production hostname", "Block and flag as critical"],
                ["Unknown TCP", "Deny by default"],
                ["Attempted-effect ledger", "Every outbound attempt is recorded, including denies"],
              ]}
            />
          }
        >
          <PageHeading title="<strong>Containment is the product surface.</strong>" />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            We do not claim every workflow is automatically compliant. We claim evidence under
            production-shaped conditions, with production data remaining in the customer boundary, and with
            live processors unreachable from the twin.
          </p>
        </Split>
      </PageSection>
      <PageSection tone="sage">
        <Callout label="Existential failure" tone="block">
          Charging a live processor, emailing a real customer, or invoking a production webhook from a twin
          is not a warning. It is a failed containment model.
        </Callout>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/firewall", title: "Side-Effect Firewall", description: "How egress is denied and simulated." },
          { href: "/security", title: "Security", description: "Fail closed. Data stays in the customer boundary." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}

function EcommercePage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · E-commerce"
        title="Checkout under production-shaped load."
        lead="A twin with production-shaped carts and long-tail SKUs. Payments and email stay captured. Locks on orders are measured while equivalent traffic hits baseline and candidate."
        visual={
          <HeroStat
            value="6.9s"
            caption="Checkout p99 after an exclusive lock on orders. Baseline was 820ms. The sale still looks green in staging."
          />
        }
      />
      <PageSection tone="white">
        <PageHeading title="<strong>The SKU that breaks the constraint</strong> is never in the fixture dump." />
        <p className="mt-8 max-w-[640px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          A defaulted column on orders, a full table rewrite during a sale, pool exhaustion, and plan
          regressions on the hottest checkout paths. Promotions render in the twin. They are never emailed.
        </p>
        <FeatureGrid
          items={[
            { title: "Long-tail catalogs", body: "Toy product fixtures miss the SKU, cart, and promo combination that breaks a constraint." },
            { title: "Checkout p99", body: "Equivalent traffic against baseline and candidate, with lock timing on orders." },
            { title: "Promotions and email", body: "Rendered and captured. Never delivered to customers." },
          ]}
        />
      </PageSection>
      <PageSection tone="sage">
        <Split
          visual={
            <SceneStage>
              <WorkloadScene />
            </SceneStage>
          }
        >
          <PageHeading title="<strong>Load is not a separate product.</strong> It is how checkout actually behaves." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Observed patterns, deterministic journeys, and a few exploratory users on the candidate. Production
            requests are never synchronously diverted. The report is a gate, not a flame graph.
          </p>
          <div className="mt-8">
            <Callout label="During a sale" tone="warn">
              An exclusive lock that is invisible on a laptop database becomes a checkout outage when the
              catalog is production-shaped.
            </Callout>
          </div>
        </Split>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/workload", title: "Workload Studio", description: "How production-shaped traffic is replayed." },
          { href: "/product/migrations", title: "Migration Safety", description: "Locks on orders, measured." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}

function MarketplacesPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Marketplaces"
        title="Queues, workers, dual-writes, matching logic staging never reproduces."
        lead="The twin includes workers and queues. Production webhooks are blocked. Impatient retries and multi-tab checkout become deterministic scenarios."
        visual={
          <HeroStat
            tone="dark"
            value="Dual-write"
            caption="Matching, notify, and settle workers run in the twin. Production webhooks are blocked and ledgered."
          />
        }
      />
      <PageSection tone="sage">
        <Split
          reverse
          visual={
            <CodePanel label="twin · marketplace workers">{`matching.worker     RUNNING
notify.worker       RUNNING
settle.worker       RUNNING

dual-write: listings → search index
retry storm: 3x checkout on listing 8841
production webhook  api.partners.test  BLOCKED
clone ledger        partner.event.v2   STORED`}
            </CodePanel>
          }
        >
          <PageHeading title="<strong>Timing is the bug.</strong> Services, queues, and workers are a dimension staging drops." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Rolling deploys, old and new schema coexistence, and duplicate events are exactly what a disposable
            twin is for. Matching logic that depends on queue order will not show up in a shared staging
            environment that skips workers.
          </p>
        </Split>
      </PageSection>
      <PageSection>
        <FeatureGrid
          items={[
            { title: "Queues in the twin", body: "Simulated streams so dual-writes and retries are visible." },
            { title: "Webhook containment", body: "Production partner webhooks are blocked and written to the attempted-effect ledger." },
            { title: "Retry personas", body: "Impatient users and API clients become deterministic scenarios." },
          ]}
        />
      </PageSection>
      <PageSection tone="white">
        <Steps
          items={[
            { title: "Restore both sides", body: "Buyers, sellers, listings, and in-flight orders as a referential subset." },
            { title: "Run the workers", body: "Matching, notify, and settle against clone-local queues." },
            { title: "Contain partners", body: "Outbound webhooks store a preview. Production hostnames never resolve." },
            { title: "Compare", body: "Duplicate events, missed matches, and irreversible writes in the oracle." },
          ]}
        />
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/workload", title: "Workload Studio", description: "Retries compiled into deterministic journeys." },
          { href: "/product/firewall", title: "Side-Effect Firewall", description: "Partner webhooks stay captured." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}

function DevtoolsPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Developer tools"
        title="Schema changes on large tables."
        lead="The flagship wedge, felt first by teams whose users notice p99 immediately. Measure lock duration, blocked statements, and whether old instances can still read the new schema."
        visual={
          <SceneStage className="min-h-[340px] p-4">
            <MigrationScene tab={1} playId={0} />
          </SceneStage>
        }
      />
      <PageSection tone="white">
        <Split
          reverse
          visual={
            <CodePanel label="plan regression · events">{`baseline   Index Scan  events_created_at_idx   12ms
candidate  Seq Scan    events                  410ms

rows           12,403,881
lock           ACCESS SHARE  1.1s
pool waited    84 connections
old app        cannot decode events.v2 payload`}
            </CodePanel>
          }
        >
          <PageHeading title="<strong>Users notice p99 immediately.</strong> Large tables plus frequent schema change." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            The first supported stack should be exceptional. A broad compatibility list with unreliable
            connectors would destroy trust. Start with Postgres volume, plans, and pools — then expand.
          </p>
        </Split>
      </PageSection>
      <PageSection>
        <FeatureGrid
          items={[
            { title: "Large tables", body: "Exclusive locks and rewrites that never show up on a laptop database." },
            { title: "Query plans", body: "Plan regressions under production-shaped volume." },
            { title: "Pools", body: "Connection-pool exhaustion during migrate-and-serve." },
          ]}
        />
      </PageSection>
      <PageSection tone="sage">
        <Callout label="Narrow adapters, complete stack">
          Exceptional Postgres instrumentation first. Publish what the twin reproduced. Do not pretend
          unsupported components are cloned.
        </Callout>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/migrations", title: "Migration Safety", description: "Locks, plans, pools, rollback." },
          { href: "/product/fidelity", title: "Fidelity Graph", description: "What the twin actually reproduced." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}

function PlatformPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Platform engineering"
        title="Replace fragile shared staging with policy-controlled ephemeral validation."
        lead="Give every developer an isolated environment without a platform-team ticket. Destroy temporary infrastructure automatically and cap its cost."
        visual={
          <HeroStat
            tone="sage"
            value="0 tickets"
            caption="Create Wind Tunnel from the pull request. The adapter, isolation, TTL, and budget are already policy."
          />
        }
      />
      <PageSection tone="sage">
        <PageHeading kicker="No ticket" title="<strong>Create Wind Tunnel from the pull request.</strong>" />
        <div className="mt-14">
          <Steps
            items={[
              { title: "Connect", body: "GitHub app, customer-hosted runner, Postgres source." },
              { title: "Review", body: "Sensitive fields and discovered outbound services." },
              { title: "Policy", body: "Isolation, cost, retention, and release gates — organization-wide." },
              { title: "Baseline", body: "Validate the current production version first. Then every risky PR." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <Split
          visual={
            <CodePanel label="pull request · Create Wind Tunnel">{`adapter     detected  compose + postgres
isolation   customer vpc · no default egress
ttl         4h
budget      $40
ticket      none

lifecycle   REQUESTED → READY → DESTROYED
cleanup     attested`}
            </CodePanel>
          }
        >
          <PageHeading title="<strong>Shared staging is a queue.</strong> Ephemeral twins are a policy." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            The customer currently chooses between assembling several tools, maintaining expensive staging,
            testing in production, or accepting the risk. Platform teams set isolation, cost, and retention.
            Developers press one button.
          </p>
        </Split>
        <div className="mt-16">
          <FeatureGrid
            items={[
              { title: "No ticket", body: "Create Wind Tunnel from the PR. The adapter is chosen for you." },
              { title: "Policy", body: "Isolation, cost, retention, and release gates are organization-wide." },
              { title: "Cleanup", body: "TTL, cost ceiling, independent reaper, verifiable destruction record." },
            ]}
          />
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/twins", title: "Isolated Twin", description: "Provision, isolate, destroy, attest." },
          { href: "/product/architecture", title: "Architecture", description: "Customer-hosted data plane." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}

function MigrationsPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Schema migrations"
        title="The failure mode staging never catches."
        lead="Long exclusive locks, full table rewrites, pool exhaustion, plan regressions, rare constraint failures, and rollback that is no longer safe."
        visual={
          <SceneStage className="min-h-[340px] p-4">
            <MigrationScene tab={0} playId={0} />
          </SceneStage>
        }
      />
      <PageSection tone="sage">
        <PageHeading title="<strong>A lock you can measure.</strong> A p99 you can compare. A rollback you can call unsafe." />
        <div className="mt-14">
          <Metrics
            items={[
              { value: "27.4s", label: "ACCESS EXCLUSIVE on subscriptions. Checkout stalls for the duration of the lock." },
              { value: "6.9s", label: "Checkout p99 under equivalent traffic. Baseline was 820ms." },
              { value: "Unsafe", label: "Old application instances cannot deserialize candidate writes. Rolling rollback fails." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <Split
          visual={
            <CodePanel label="BLOCKED: unsafe schema migration">{`Migration 20260824_add_billing_status
held ACCESS EXCLUSIVE on subscriptions
for 27.4 seconds.

checkout p99        820ms → 6.9s
upgrade timeouts    11.8%
old app             cannot read candidate rows
rolling rollback    unsafe

suggested: nullable column, batched backfill,
dual-read, constraint in a later migration.`}
            </CodePanel>
          }
        >
          <PageHeading title="<strong>This is the wedge.</strong> Not universal multicloud cloning." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Automated safety validation for risky Postgres-backed web deployments, especially schema
            migrations. Clear failure mode, technical buyer, measurable value. Land on one migration, expand
            to every risky pull request.
          </p>
        </Split>
      </PageSection>
      <PageSection>
        <FeatureGrid
          items={[
            { title: "Locks", body: "Acquisition, duration, blocked statements, and cumulative blocked time." },
            { title: "Rewrites", body: "Full table rewrites, index builds, constraint failures on rare rows." },
            { title: "Rollback", body: "Whether candidate writes make rolling rollback unsafe." },
          ]}
        />
        <div className="mt-14">
          <Callout label="Safer pattern">
            Expand-and-contract. Dual-read compatibility. Constraint later. The engine should generate both
            evidence and a recommended safer migration pattern.
          </Callout>
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/migrations", title: "Migration Safety Engine", description: "How locks become a GitHub check." },
          { href: "/product/report", title: "Safety Report", description: "The block, with a cause." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}

function ReleaseGatesPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Release gates"
        title="Evidence-backed pass, warning, or block."
        lead="Attach a report to the pull request. Enforce organizational release policy. Do not ship on a green preview URL alone."
        visual={
          <SceneStage>
            <ReportScene />
          </SceneStage>
        }
      />
      <PageSection tone="sage">
        <PageHeading title="<strong>The only output that matters</strong> is the deployment decision." />
        <ul className="mt-14 grid grid-cols-3 gap-5 max-md:grid-cols-1">
          <li className="rounded-[12px] bg-white p-7 ring-1 ring-black/10">
            <div className="font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-[#1f7a3a]">Pass</div>
            <p className="mt-4 text-[18px] leading-snug tracking-extra-tight text-black">Ship with evidence attached to the pull request.</p>
          </li>
          <li className="rounded-[12px] bg-amber-50 p-7 ring-1 ring-amber-200">
            <div className="font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-amber-800">Warning</div>
            <p className="mt-4 text-[18px] leading-snug tracking-extra-tight text-black">Human review. Fidelity, latency, or an expected difference.</p>
          </li>
          <li className="rounded-[12px] bg-red-50 p-7 ring-1 ring-red-200">
            <div className="font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-red-700">Block</div>
            <p className="mt-4 text-[18px] leading-snug tracking-extra-tight text-black">Do not merge. The cause, the workflow, and the remediation are in the report.</p>
          </li>
        </ul>
      </PageSection>
      <PageSection tone="white">
        <Split
          reverse
          visual={
            <ul className="space-y-4 text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
              <li className="rounded-[12px] bg-red-50 px-5 py-4 ring-1 ring-red-200">
                Block if an exclusive lock exceeds 2 seconds on a critical table.
              </li>
              <li className="rounded-[12px] bg-red-50 px-5 py-4 ring-1 ring-red-200">
                Block if unknown external egress is attempted.
              </li>
              <li className="rounded-[12px] bg-amber-50 px-5 py-4 ring-1 ring-amber-200">
                Warn if p95 latency increases by more than 15%.
              </li>
              <li className="rounded-[12px] bg-[#E4F1EB] px-5 py-4 ring-1 ring-black/10">
                Require approval if fidelity is below 80%.
              </li>
            </ul>
          }
        >
          <PageHeading title="<strong>Policy is the platform-team surface.</strong> Enforce it organization-wide." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Reduce the probability and blast radius of high-risk releases by validating them under
            production-shaped conditions. That is not a zero-rollback guarantee. Reports that are noisy get
            ignored — baseline comparison and severity policy keep the gate useful.
          </p>
        </Split>
      </PageSection>
      <PageSection tone="sage">
        <Callout label="Center the decision">
          Every workflow and report centers on the deployment decision. Environment creation, data, agents,
          and load are supporting systems. A preview URL is not the product.
        </Callout>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/report", title: "Safety Report", description: "How the gate is attached to the PR." },
          { href: "/product/oracle", title: "Differential Oracle", description: "Where comparisons are produced." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}

function WorkflowPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Workflow products"
        title="Workers, schedules, and long-tail state."
        lead="Timing among services, queues, and workers is a dimension staging drops. The twin includes background jobs and scheduled tasks against sanitized historical state."
        visual={
          <SceneStage>
            <WorkloadScene />
          </SceneStage>
        }
      />
      <PageSection tone="sage">
        <PageHeading title="<strong>Jobs in the twin.</strong> Duplicate events and irreversible writes show up in the oracle." />
        <p className="mt-8 max-w-[640px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Workflow and collaboration products share Postgres, frequent deploys, and workers that staging does
          not run the same way. Malformed historical records that fixtures never include are the ones that
          fail the constraint.
        </p>
        <FeatureGrid
          items={[
            { title: "Jobs in the twin", body: "Background workers and scheduled tasks against sanitized state." },
            { title: "Long-tail records", body: "Malformed historical state that fixtures never include." },
            { title: "Multi-tab behavior", body: "Crowdi personas that abandon, resume, and retry — compiled into deterministic scenarios." },
          ]}
        />
      </PageSection>
      <PageSection tone="white">
        <Split
          reverse
          visual={
            <CodePanel label="scenario: retry_after_abandon">{`identity_fixture: returning_editor
schedule: billing.reconcile  */15

steps:
  - open: /docs/1841
  - edit: title
  - abandon_for_ms: 40000
  - resume_other_tab: /docs/1841
  - submit: save
assertions:
  - one_version_created
  - reconcile_job_idempotent
  - no_duplicate_share_email`}
            </CodePanel>
          }
        >
          <PageHeading title="<strong>Schedules are production behavior.</strong> They belong in the proving ground." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Crowdi explores abandon-and-resume and multi-tab edits. The deterministic runner proves them at
            scale, including the reconcile job that must stay idempotent. Crowdi is a Workload Studio feature,
            not the product.
          </p>
        </Split>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/workload", title: "Workload Studio", description: "Observed, deterministic, exploratory." },
          { href: "/product/crowdi", title: "Crowdi", description: "Exploratory users inside Workload Studio." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}

const PAGES: Record<Slug, () => ReactNode> = {
  saas: SaasPage,
  fintech: FintechPage,
  ecommerce: EcommercePage,
  marketplaces: MarketplacesPage,
  devtools: DevtoolsPage,
  platform: PlatformPage,
  migrations: MigrationsPage,
  "release-gates": ReleaseGatesPage,
  workflow: WorkflowPage,
};

export function SolutionVerticalPage({ slug }: { slug: string }) {
  const Page = PAGES[slug as Slug];
  if (!Page) return null;
  return <Page />;
}
