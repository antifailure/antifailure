import {
  Faq,
  type FaqItem,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
  Steps,
} from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
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
  verdict?: "PASS" | "FAIL" | "UNVERIFIED";
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
    body: "Agents driving the workflows you declared, and traffic shaped like production's log.",
  },
  {
    title: "Judgment",
    body: "Workflow verdicts, invariants asked of the data, and latency against production's p95.",
  },
  {
    title: "Evidence",
    body: "A pass or fail on the pull request, with the rows, the trace and the video behind it.",
    verdict: "FAIL",
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
  { miss: "One shared schema", have: "A branch per pull request" },
  { miss: "Live Stripe and email", have: "Fail-closed containment" },
  { miss: "A preview URL", have: "Pass or fail, with evidence" },
];

const VERDICTS: { tone: "PASS" | "FAIL" | "UNVERIFIED"; title: string; body: string }[] = [
  { tone: "PASS", title: "Ship", body: "Every workflow reached the outcome it declared, and every invariant held." },
  { tone: "FAIL", title: "Do not merge", body: "A workflow failed or an invariant broke. The only verdict that exits non-zero." },
  {
    tone: "UNVERIFIED",
    title: "We could not tell",
    body: "Flaky, blocked or unverified. Something is wrong with the run, and it does not count against you.",
  },
];

/**
 * What af insights reports about a migration.
 *
 * The four rows here used to be a lock, a checkout p99, a timeout rate and a
 * rollback verdict. Only the lock was something this engine can measure:
 * nothing runs traffic against a migration while it applies, and there is no
 * old-binary coexistence check anywhere in the product.
 */
const MIGRATION_FINDINGS: { k: string; v: string }[] = [
  { k: "strongest lock", v: "ACCESS EXCLUSIVE 27.4s on subscriptions" },
  { k: "blocked waiters", v: "84 statements queued behind it" },
  { k: "table rewrite", v: "yes, reported by Postgres" },
  { k: "plan change", v: "Index Scan to Seq Scan on events" },
];

/**
 * Every answer here is a claim this repository already makes somewhere else:
 * the trust boundary from the privacy notice, the five egress modes and the
 * five verdicts from the README, the provider list from "Where it runs", and
 * the licence split from the licence section. Nothing is invented for the
 * page, because an answer engine quoting a page is quoting it as fact.
 */
const PRODUCT_FAQ: FaqItem[] = [
  {
    question: "Does production data leave my infrastructure?",
    answer:
      "No. The hosted control plane holds organizations, policy, aggregated reports, and billing. Raw snapshots, secrets, and captured request bodies stay in your cloud by default.",
  },
  {
    question: "How do I know the masking actually worked?",
    answer:
      "A scanner reads back every column of every table looking for anything that still parses as an email, a card number, a phone number, or a key, then signs an attestation. An unverified golden cannot be branched, and that is enforced in code rather than in a checklist.",
  },
  {
    question: "What stops a test run from emailing real customers or charging a real card?",
    answer:
      "Every environment gets a sidecar that owns its network namespace, and nothing leaves except through it. Each host gets a mode: BLOCK, ALLOW, SANDBOX with test credentials and a tripwire if a live key appears, CAPTURE into a searchable inbox, or MOCK from a stateful offline pack. An unlisted host fails closed.",
  },
  {
    question: "Can a run complete with no network access at all?",
    answer:
      "Yes, for the covered surface. The Stripe pack is complete enough to run checkout, subscribe, renew, and cancel with signed webhooks and no network.",
  },
  {
    question: "What happens when a check fails because the tooling broke, not my code?",
    answer:
      "It is classified as such. A run returns pass, fail, flaky, blocked, or unverified, and a failure caused by the runner is never counted against your application.",
  },
  {
    question: "Which databases and platforms does it support?",
    answer:
      "Postgres, sourced from Docker, Neon, Supabase, or DBLab thin clones in front of any Postgres including RDS, Cloud SQL, and Azure Database. It runs locally on Docker, in GitHub Actions, or on your own Kubernetes.",
  },
  {
    question: "Is it open source?",
    answer:
      "The repository is MIT licensed except for the ee/ directory, which is under the Antifailure Enterprise License. That directory is never compiled into the community binary, images, or Helm chart.",
  },
  {
    question: "Is it production ready?",
    answer:
      "Not yet. It is pre-1.0, and docs/plan/STATUS.md gives the honest answer per component rather than one claim for the whole system, marking each one proven, written, or planned.",
  },
];

export function OverviewPage() {
  return (
    <PageShell inset>
      <PageHero
        path="/product"
        eyebrow="Product"
        title="A disposable production twin that proves whether a deployment is safe."
        lead="Connect a repository and a cloud environment. For every risky change, Antifailure builds an isolated production twin, fills it with safe production-shaped state, exercises it, and says whether the deployment is safe to ship."
        visual={<TwinLifecycleScene />}
      />

      <PageSection>
        <PageHeading title="<strong>Seven pieces, one decision.</strong> None of these is the product on its own." />
        <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Twin, state, containment, behavior, judgment, evidence, and cleanup. The output is a pass
          or a fail on the pull request, and then the environment is destroyed.
        </p>

        <div className="relative mt-14 max-md:mt-10">
          <ul className="grid grid-cols-4 gap-x-16 gap-y-12 max-xl:grid-cols-2 max-xl:gap-x-10 max-md:grid-cols-1 max-md:gap-y-8">
            {MODULES.map((m) => (
              <li key={m.title} className="min-w-0">
                <svg viewBox="0 0 16 16" className="mb-4 size-4 text-black" fill="none" aria-hidden>
                  <rect x="1.5" y="1.5" width="13" height="13" stroke="currentColor" strokeWidth="1.2" />
                </svg>
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <h3 className="text-[18px] leading-snug tracking-extra-tight text-black">{m.title}</h3>
                  {m.verdict ? <StatusPill tone={m.verdict}>{m.verdict}</StatusPill> : null}
                </div>
                <p className="mt-2 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                  {m.body}
                </p>
              </li>
            ))}
          </ul>
          <span className="pointer-events-none absolute inset-y-0 left-[calc(25%-16px)] w-px bg-black/12 max-xl:hidden" />
          <span className="pointer-events-none absolute inset-y-0 left-1/2 w-px bg-black/12 max-md:hidden" />
          <span className="pointer-events-none absolute inset-y-0 right-[calc(25%-16px)] w-px bg-black/12 max-xl:hidden" />
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
              <div className="grid grid-cols-2 max-xl:grid-cols-1">
                <div className="border-r border-black/8 px-5 py-5 max-xl:border-r-0 max-xl:border-b max-xl:border-black/8">
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
                    <StatusPill tone="FAIL">FAIL</StatusPill>
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
            each cover one fragment. A disposable twin unifies the minimum set required to validate a
            real deployment.
          </p>
        </Split>
      </PageSection>

      <PageSection>
        <PageHeading kicker="Scope" title="<strong>Postgres migrations first.</strong> Not universal multicloud cloning." />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Exclusive locks, table rewrites, pool exhaustion, query-plan regressions, and old binaries
          that cannot read candidate writes. Conventional tests miss all of them. So the first thing
          built completely is the check for a risky Postgres-backed deployment, rather than a shallow
          version of everything.
        </p>

        <div className="mt-14">
          <MigrationScene tab={0} playId={0} />
        </div>
        <Illustrative label="Example finding">
          One migration rehearsed, with the numbers chosen. The measurements are the ones{" "}
          <code className="font-mono text-[15px] text-black">af insights</code> takes: the strongest
          lock mode and its hold time, what queued behind it, rewrites, and plans before and after.
        </Illustrative>

        <div className="mt-8 grid grid-cols-2 gap-5 max-xl:grid-cols-1">
          <Panel className="rounded-[12px] bg-white">
            <div className="flex items-center justify-between gap-3 px-5 py-3">
              <MonoLabel className="text-black/55">af insights · migration rehearsal</MonoLabel>
              <StatusPill tone="FAIL">FAIL</StatusPill>
            </div>
            <Hairline />
            <div className="px-5 py-5">
              <h3 className="text-[18px] tracking-extra-tight text-black">Unsafe schema migration</h3>
              <p className="mt-2 font-mono text-[12px] tracking-extra-tight text-black/45">
                20260824_add_billing_status
              </p>
              <dl className="mt-5 space-y-2.5">
                {MIGRATION_FINDINGS.map((row) => (
                  <div key={row.k} className="flex items-baseline justify-between gap-4">
                    <dt className="font-mono text-[11px] tracking-extra-tight text-black/40">{row.k}</dt>
                    <dd
                      className={cn(
                        "font-mono text-[12px] tracking-extra-tight",
                        row.k === "strongest lock" ? "text-red-700" : "text-black/70",
                      )}
                    >
                      {row.v}
                    </dd>
                  </div>
                ))}
              </dl>
            </div>
            <div className="border-t border-black/[0.08] px-5 py-3 font-mono text-[11px] leading-5 tracking-extra-tight text-[#285D49]">
              lint · add the column without a default, then backfill in batches
            </div>
          </Panel>

          <div className="flex min-w-0 flex-col justify-center rounded-[12px] border border-black/[0.08] bg-white px-8 py-8 max-md:px-5 max-md:py-6">
            <h3 className="text-[28px] leading-dense tracking-tighter text-gray-new-40 max-lg:text-[22px] [&>strong]:font-normal [&>strong]:text-black">
              <strong>A 27-second lock is a finding.</strong> Not a line in a log nobody reads.
            </h3>
            <p className="mt-5 max-w-[440px] text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
              The rehearsal runs the pending migrations against a branch with production's shape and
              samples what is locked every 250 milliseconds. It reports the strongest mode held per
              table, how long it was held, and which statements queued behind it.
            </p>
          </div>
        </div>

        <div className="mt-16">
          <MonoLabel className="text-black/55">How a run decides</MonoLabel>
          <div className="mt-8">
            <Steps
              items={[
                { title: "Read the repository", body: "Twelve analyzers write a manifest and say what they assumed." },
                { title: "Reproduce safely", body: "Isolated twin, sanitized state, fail-closed egress." },
                { title: "Exercise", body: "Declared workflows, invariants asked of the data, production's route mix." },
                { title: "Decide", body: "Pass or fail on the pull request, then destroy the environment." },
              ]}
            />
          </div>
        </div>
      </PageSection>

      <PageSection tone="white">
        <PageHeading title="<strong>The output is a decision.</strong> Not a dataset. Not a preview URL alone." />
        <ul className="relative mt-16 grid grid-cols-3 gap-x-16 max-xl:grid-cols-1 max-xl:gap-y-10">
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
          <span className="pointer-events-none absolute inset-y-0 left-[calc(33.333%-32px)] w-px bg-black/12 max-xl:hidden" />
          <span className="pointer-events-none absolute inset-y-0 right-[calc(33.333%-32px)] w-px bg-black/12 max-xl:hidden" />
        </ul>
        <div className="mt-16 max-w-[640px] border-t border-black/10 pt-8">
          <MonoLabel>What we will not claim</MonoLabel>
          <p className="mt-3 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
            Zero rollback. No deployment can ever fail. Thousands of AI agents behave exactly like
            humans. One click perfectly clones every cloud. Where a run could not measure something,
            it says so rather than scoring it.
          </p>
        </div>
      </PageSection>

      {/* Phrased the way the questions are actually asked, not the way a
          feature list would put them. These are the eight things people want
          settled before they will read the documentation, and each answer is
          self-contained so it survives being lifted out of the page on its
          own. Faq carries the matching FAQPage markup from the same array. */}
      <PageSection>
        <PageHeading
          kicker="Questions"
          title="<strong>What people ask first.</strong> Answered here rather than in a sales call."
        />
        <Faq path="/product" items={PRODUCT_FAQ} />
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/twins", title: "Isolated Twin", description: "How the orchestrator provisions and tears down." },
          { href: "/product/migrations", title: "Migration Safety", description: "Locks, rewrites and plans, before it ships." },
          { href: "/docs", title: "Docs", description: "How a twin run works, end to end." },
        ]}
      />
    </PageShell>
  );
}
