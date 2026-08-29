import {
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
  Steps,
} from "@/components/pages/kit";
import { TwinLifecycleScene } from "@/components/home/visuals/TwinLifecycleScene";
import { MigrationScene } from "@/components/home/visuals/MigrationScene";
import {
  CheckRow,
  Hairline,
  MonoLabel,
  Panel,
  StatusPill,
} from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

const MODULES: {
  title: string;
  body: string;
  verdict?: "PASS" | "WARN" | "BLOCK";
}[] = [
  {
    title: "Twin",
    body: "An isolated, temporary copy of the relevant application stack.",
  },
  {
    title: "State",
    body: "A safe, referentially consistent, production-shaped dataset.",
  },
  {
    title: "Containment",
    body: "No charging cards, emailing users, or invoking production webhooks.",
  },
  {
    title: "Behavior",
    body: "Captured workloads, deterministic scenarios, and exploratory AI users.",
  },
  {
    title: "Comparison",
    body: "Current and proposed versions against equivalent state and behavior.",
  },
  {
    title: "Judgment",
    body: "Functional, database, performance, integration, and behavioral regressions.",
  },
  {
    title: "Evidence",
    body: "An auditable pass, warning, or block report on the pull request.",
    verdict: "BLOCK",
  },
  {
    title: "Cleanup",
    body: "Destroy temporary resources and prove that cleanup completed.",
  },
];

const FRAGMENTS = ["Preview", "Test data", "E2E", "Load", "Mirror", "Observability"] as const;

const STAGING_ROWS: { miss: string; have: string }[] = [
  { miss: "Fixture volume", have: "Production-shaped subset" },
  { miss: "No long-tail rows", have: "Referential rare records" },
  { miss: "Quiet concurrency", have: "Equivalent workload" },
  { miss: "One shared schema", have: "Baseline and candidate" },
  { miss: "Live Stripe and email", have: "Fail-closed simulators" },
  { miss: "A preview URL", have: "Pass, warning, or block" },
];

const VERDICTS: { tone: "PASS" | "WARN" | "BLOCK"; title: string; body: string }[] = [
  { tone: "PASS", title: "Ship", body: "Highest-risk conditions we can observe look safe enough to proceed." },
  { tone: "WARN", title: "Eyes open", body: "A difference is real, but policy allows merge with explicit review." },
  { tone: "BLOCK", title: "Do not merge", body: "Evidence shows a dangerous lock, regression, or uncontained effect." },
];

const BLOCK_METRICS: { k: string; v: string }[] = [
  { k: "lock", v: "ACCESS EXCLUSIVE 27.4s" },
  { k: "checkout p99", v: "820ms → 6.9s" },
  { k: "upgrade timeouts", v: "11.8%" },
  { k: "rollback", v: "unsafe" },
];

export function OverviewPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Product"
        title="A disposable production twin that proves whether a deployment is safe."
        lead="Connect a repository and cloud environment. For every risky change, the platform creates an isolated production twin, fills it with safe production-shaped state, exercises it, and reports whether the deployment is safe to ship."
        visual={<TwinLifecycleScene />}
      />

      <PageSection>
        <PageHeading title="<strong>Eight pieces, one decision.</strong> None of these is the product on its own." />
        <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Twin, state, containment, behavior, comparison, judgment, evidence, and cleanup. The output is
          pass, warning, or block — then the environment is destroyed.
        </p>

        <div className="relative mt-16 max-md:mt-10">
          <ul className="grid grid-cols-4 gap-x-12 gap-y-14 max-lg:grid-cols-2 max-md:grid-cols-1 max-md:gap-y-8">
            {MODULES.map((m) => (
              <li key={m.title} className="min-w-0">
                <svg viewBox="0 0 16 16" className="mb-4 size-4 text-black" fill="none" aria-hidden>
                  <rect x="1.5" y="1.5" width="13" height="13" stroke="currentColor" strokeWidth="1.2" />
                </svg>
                <div className="flex items-center justify-between gap-3">
                  <h3 className="text-[18px] leading-snug tracking-extra-tight text-black">{m.title}</h3>
                  {m.verdict ? <StatusPill tone={m.verdict}>{m.verdict}</StatusPill> : null}
                </div>
                <p className="mt-2 max-w-[280px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                  {m.body}
                </p>
              </li>
            ))}
          </ul>
          <span className="pointer-events-none absolute inset-y-0 left-[calc(25%-24px)] w-px bg-black/12 max-lg:hidden" />
          <span className="pointer-events-none absolute inset-y-0 left-1/2 w-px bg-black/12 max-md:hidden" />
          <span className="pointer-events-none absolute inset-y-0 right-[calc(25%-24px)] w-px bg-black/12 max-lg:hidden" />
        </div>
      </PageSection>

      <PageSection tone="sage">
        <Split
          visual={
            <Panel className="rounded-[12px] bg-white">
              <div className="flex items-center justify-between gap-3 px-5 py-3">
                <MonoLabel className="text-black/55">What teams assemble today</MonoLabel>
                <MonoLabel>no single decision</MonoLabel>
              </div>
              <Hairline />
              <div className="flex flex-wrap gap-2 px-5 py-4">
                {FRAGMENTS.map((name) => (
                  <span
                    key={name}
                    className="border border-black/[0.08] px-2 py-1 font-mono text-[10px] tracking-extra-tight text-black/45"
                  >
                    {name}
                  </span>
                ))}
              </div>
              <Hairline />
              <div className="grid grid-cols-2 max-sm:grid-cols-1">
                <div className="border-r border-black/8 px-5 py-5 max-sm:border-r-0 max-sm:border-b max-sm:border-black/8">
                  <div className="flex items-center justify-between gap-2">
                    <MonoLabel>Shared staging</MonoLabel>
                    <span className="border border-black/[0.08] px-1.5 py-0.5 font-mono text-[10px] tracking-extra-tight text-black/35">
                      drifted
                    </span>
                  </div>
                  <ul className="mt-4 space-y-2.5">
                    {STAGING_ROWS.map((row) => (
                      <li key={row.miss}>
                        <CheckRow ok={false} className="text-black/45">
                          {row.miss}
                        </CheckRow>
                      </li>
                    ))}
                  </ul>
                </div>
                <div className="px-5 py-5">
                  <div className="flex items-center justify-between gap-2">
                    <MonoLabel className="text-black/55">Disposable twin</MonoLabel>
                    <StatusPill tone="BLOCK">BLOCK</StatusPill>
                  </div>
                  <ul className="mt-4 space-y-2.5">
                    {STAGING_ROWS.map((row) => (
                      <li key={row.have}>
                        <CheckRow ok className="text-black/70">
                          {row.have}
                        </CheckRow>
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
            </Panel>
          }
        >
          <PageHeading title="<strong>The question staging cannot answer.</strong> What happens when this change meets real data, concurrency, workers, and the deploy process." />
          <p className="mt-8 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Preview tools, test-data platforms, E2E suites, load tests, packet mirrors, and observability
            each cover one fragment. The wind tunnel unifies the minimum set required to validate a real
            deployment.
          </p>
        </Split>
      </PageSection>

      <PageSection>
        <PageHeading kicker="Wedge" title="<strong>Postgres migrations first.</strong> Not universal multicloud cloning." />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Exclusive locks, table rewrites, pool exhaustion, query-plan regressions, and old binaries that
          cannot read candidate writes. Conventional tests miss them. The first complete wedge is
          automated safety validation for risky Postgres-backed web deployments.
        </p>

        <div className="mt-14">
          <MigrationScene tab={0} playId={0} />
        </div>

        <div className="mt-8 grid grid-cols-2 gap-5 max-lg:grid-cols-1">
          <Panel className="rounded-[12px] bg-white">
            <div className="flex items-center justify-between gap-3 px-5 py-3">
              <MonoLabel className="text-black/55">pr · 184 · safety check</MonoLabel>
              <StatusPill tone="BLOCK">BLOCK</StatusPill>
            </div>
            <Hairline />
            <div className="px-5 py-5">
              <h3 className="text-[18px] tracking-extra-tight text-black">Unsafe schema migration</h3>
              <p className="mt-2 font-mono text-[12px] tracking-extra-tight text-black/45">
                20260824_add_billing_status
              </p>
              <dl className="mt-5 space-y-2.5">
                {BLOCK_METRICS.map((row) => (
                  <div key={row.k} className="flex items-baseline justify-between gap-4">
                    <dt className="font-mono text-[11px] tracking-extra-tight text-black/40">{row.k}</dt>
                    <dd
                      className={cn(
                        "font-mono text-[12px] tracking-extra-tight",
                        row.k === "rollback" ? "text-red-700" : "text-black/70",
                      )}
                    >
                      {row.v}
                    </dd>
                  </div>
                ))}
              </dl>
            </div>
            <div className="border-t border-black/[0.08] px-5 py-3 font-mono text-[11px] leading-5 tracking-extra-tight text-[#285D49]">
              suggested · nullable, no default · batch backfill · dual-read · constrain later
            </div>
          </Panel>

          <div className="flex min-w-0 flex-col justify-center rounded-[12px] border border-black/[0.08] bg-white px-8 py-8 max-md:px-5 max-md:py-6">
            <h3 className="text-[28px] leading-dense tracking-tighter text-gray-new-40 max-lg:text-[22px] [&>strong]:font-normal [&>strong]:text-black">
              <strong>A 27-second lock is a block.</strong> Not a warning you can ignore.
            </h3>
            <p className="mt-5 max-w-[440px] text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
              During equivalent traffic, checkout p99 moved from 820ms to 6.9s and 11.8% of upgrade
              attempts timed out. The previous application version failed to deserialize candidate
              rows, so rolling rollback is unsafe.
            </p>
          </div>
        </div>

        <div className="mt-16">
          <MonoLabel className="text-black/55">How a run decides</MonoLabel>
          <div className="mt-8">
            <Steps
              items={[
                { title: "Understand the change", body: "Services, migrations, APIs, and workflows that actually matter." },
                { title: "Reproduce safely", body: "Isolated twin, sanitized state, fail-closed egress." },
                { title: "Compare", body: "Baseline and candidate against equivalent behavior." },
                { title: "Decide", body: "Pass, warning, or block — then destroy the environment." },
              ]}
            />
          </div>
        </div>
      </PageSection>

      <PageSection tone="white">
        <PageHeading title="<strong>The output is a decision.</strong> Not a dataset. Not a preview URL alone." />
        <ul className="relative mt-16 grid grid-cols-3 gap-x-16 max-md:grid-cols-1 max-md:gap-y-10">
          {VERDICTS.map((item) => (
            <li key={item.tone} className="min-w-0">
              <StatusPill tone={item.tone}>{item.tone}</StatusPill>
              <h3 className="mt-5 text-[22px] leading-dense tracking-tighter text-black max-lg:text-[18px]">
                {item.title}
              </h3>
              <p className="mt-2 max-w-[280px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                {item.body}
              </p>
            </li>
          ))}
          <span className="pointer-events-none absolute inset-y-0 left-[calc(33.333%-32px)] w-px bg-black/12 max-md:hidden" />
          <span className="pointer-events-none absolute inset-y-0 right-[calc(33.333%-32px)] w-px bg-black/12 max-md:hidden" />
        </ul>
        <div className="mt-16 max-w-[640px] border-t border-black/10 pt-8">
          <MonoLabel>What we will not claim</MonoLabel>
          <p className="mt-3 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
            Zero rollback. No deployment can ever fail. Thousands of AI agents behave exactly like humans.
            One click perfectly clones every cloud. Exploratory users live in Workload Studio, not as the category.
          </p>
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/twins", title: "Isolated Twin", description: "How the orchestrator provisions and tears down." },
          { href: "/product/migrations", title: "Migration Safety", description: "The first complete wedge." },
          { href: "/docs", title: "Docs", description: "How a twin run works, end to end." },
        ]}
      />
    </PageShell>
  );
}
