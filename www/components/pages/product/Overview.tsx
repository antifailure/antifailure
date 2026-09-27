import { Faq, PageHeading, PageHero, PageSection, PageShell, Split, Steps, type FaqItem } from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
import { POV01, POV02, POV03, POV04 } from "@/components/pages/figures/product-overview";
import { MonoLabel, StatusPill } from "@/components/home/visuals/primitives";

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

const STAGING_ROWS: { miss: string; have: string }[] = [
  { miss: "Fixture volume", have: "Production-shaped subset" },
  { miss: "No long-tail rows", have: "Referential rare records" },
  { miss: "Quiet concurrency", have: "Equivalent workload" },
  { miss: "One shared schema", have: "A branch per pull request" },
  { miss: "Live Stripe and email", have: "Fail-closed containment" },
  { miss: "A preview URL", have: "Pass or fail, with evidence" },
];

const VERDICTS: { tone: "PASS" | "FAIL" | "UNVERIFIED"; title: string; body: string }[] = [
  { tone: "PASS", title: "Ship", body: "The configured checks passed. Review their scope and supporting evidence." },
  { tone: "FAIL", title: "Do not merge", body: "A check found a failure. Review the evidence and reproduction steps." },
  {
    tone: "UNVERIFIED",
    title: "We could not tell",
    body: "The run could not establish an outcome. Review the reason and rerun the affected checks.",
  },
];

/**
 * Every answer here is a claim this repository already makes somewhere else:
 * the trust boundary from the privacy notice, the seven egress modes and the
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
      "After masking, a scanner samples rows across every table and column for emails, card numbers, phone numbers, and keys. A signed attestation records coverage and findings. Only verified snapshots can be branched.",
  },
  {
    question: "What stops a test run from emailing real customers or charging a real card?",
    answer:
      "Outbound traffic goes through a gateway with a policy for each host. Use sandboxes, offline mocks, local emulators, or captured messages for integrations. Unlisted destinations are blocked.",
  },
  {
    question: "Can a run complete with no network access at all?",
    answer:
      "Yes. Supported offline mocks include Stripe checkout, subscriptions, renewals, cancellations, and signed webhooks.",
  },
  {
    question: "What happens when a check fails because the tooling broke, not my code?",
    answer:
      "The result distinguishes application failures from blocked, flaky, and unverified checks, with a reason to help you decide what to rerun.",
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
      "The version 1 contract covers the manifest, CLI, documented JSON fields, provider interfaces, and error codes. The stability guide and component status document describe support and verification for each feature.",
  },
];

export function OverviewPage() {
  return (
    <PageShell>
      <PageHero
        path="/product"
        eyebrow="Product"
        title="See how your change behaves before you deploy."
        lead="Connect your coding agent through MCP. Give it an isolated copy of your stack to rehearse migrations, test user journeys, and return evidence before you merge."
        framed={false}
        visual={<POV01 />}
      />

      <PageSection>
        <PageHeading title="<strong>A full test environment for each pull request.</strong>" />

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

      <PageSection tone="panel">
        <Split visual={<POV02 rows={STAGING_ROWS} />}>
          <PageHeading title="<strong>Test against the conditions your change will meet.</strong>" />
          <p className="mt-8 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Bring services, data, and test traffic together in an environment dedicated to your change.
          </p>
        </Split>
      </PageSection>

      <PageSection>
        <Split visual={<POV03 />}>
          <PageHeading kicker="Scope" title="<strong>Rehearse migrations at realistic database sizes.</strong>" />
          <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Inspect lock durations, table rewrites, query plan changes, and compatibility with the previous release.
          </p>
        </Split>
        <Illustrative label="Example finding">
            Example migration report with sample values.
          </Illustrative>

        <div className="mt-8 grid grid-cols-2 items-start gap-x-16 gap-y-12 max-xl:grid-cols-1">
          <POV04 />

          <div className="flex min-w-0 flex-col justify-center">
            <h3 className="text-[28px] leading-dense tracking-tighter text-gray-new-40 max-lg:text-[22px] [&>strong]:font-normal [&>strong]:text-black">
              <strong>A 27-second lock is a finding.</strong> Not a line in a log nobody reads.
            </h3>
            <p className="mt-5 max-w-[440px] text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
              The rehearsal runs the pending migrations against a branch with production's shape and
              samples what is locked every 250 milliseconds.
            </p>
          </div>
        </div>

        <div className="mt-16">
          <MonoLabel tone="reader">How a run decides</MonoLabel>
          <div className="mt-8">
            <Steps
              items={[
                { title: "Read the repository", body: "Detection writes a manifest, names the file every answer came from, and says what it assumed." },
                { title: "Reproduce safely", body: "Isolated twin, sanitized state, fail-closed egress." },
                { title: "Exercise", body: "Declared workflows, invariants asked of the data, production's route mix." },
                { title: "Decide", body: "Pass or fail on the pull request, then destroy the environment." },
              ]}
            />
          </div>
        </div>
      </PageSection>

      <PageSection tone="ruled">
        <PageHeading title="<strong>Know what passed and what needs attention.</strong>" />
        {/* The two column rules are siblings of the list, not children of it.
            They were spans inside the ul, and a ul may only directly contain
            li, so axe's list rule reported the element and a screen reader
            counted five items where there are three. */}
        <div className="relative mt-16">
        <ul className="grid grid-cols-3 gap-x-16 max-xl:grid-cols-1 max-xl:gap-y-10">
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
        </ul>
          <span className="pointer-events-none absolute inset-y-0 left-[calc(33.333%-32px)] w-px bg-black/12 max-xl:hidden" />
          <span className="pointer-events-none absolute inset-y-0 right-[calc(33.333%-32px)] w-px bg-black/12 max-xl:hidden" />
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
          title="<strong>Common questions.</strong>"
        />
        <Faq path="/product" items={PRODUCT_FAQ} />
      </PageSection>
    </PageShell>
  );
}
