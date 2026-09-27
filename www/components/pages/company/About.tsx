import Link from "next/link";
import { Button } from "@/components/layout/Button";
import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  Prose,
  RelatedGrid,
  Steps,
} from "@/components/pages/kit";
import {
  DOCS_URL,
  REPO_URL,
  SITE_DESCRIPTION_LONG,
} from "@/lib/site";

const RUN_STEPS = [
  {
    title: "Build a disposable twin",
    body: "The change gets a temporary copy of the application stack instead of sharing a long-lived staging environment.",
  },
  {
    title: "Prepare verified state",
    body: "Postgres data is masked inside the customer boundary, scanned, and attested before an environment can use it.",
  },
  {
    title: "Contain and exercise",
    body: "Egress policy controls external effects while deterministic workflows and exploratory agents use the running application.",
  },
  {
    title: "Report and remove",
    body: "The run returns evidence and a verdict, then cleanup removes the resources it created and records the outcome.",
  },
];

export function AboutPage() {
  return (
    <PageShell>
      <PageHero
        path="/about"
        eyebrow="About"
        title="Confidence before the change reaches production."
        lead="Antifailure gives developers a place to test the change they are about to ship, with realistic data, running services, and evidence they can inspect."
        actions={
          <>
            <Button href={DOCS_URL} theme="filled">
              Read the documentation
            </Button>
            <Button href={REPO_URL} theme="outlined">
              View the source
            </Button>
          </>
        }
      />

      <PageSection>
        <PageHeading
          kicker="The project"
          title="<strong>Make production conditions part of the review.</strong>"
        />
        <div className="mt-14 grid grid-cols-[minmax(0,720px)_minmax(260px,420px)] gap-x-20 gap-y-10 max-lg:grid-cols-1">
          <Prose>
            <p>{SITE_DESCRIPTION_LONG}</p>
            <p className="mt-6">
              A pull request can pass its tests and still behave differently on a
              large database or across several running services. Antifailure brings
              those conditions into the review, while there is still time to change the code.
            </p>
            <p className="mt-6">
              The implementation, plans, and known limits are public in the{" "}
              <a href={REPO_URL}>source repository</a>. Installation and operating details live in
              the <a href="/docs">documentation</a>.
            </p>
          </Prose>
          <div className="self-start">
            <Callout label="Current status" tone="warn">
              Check feature availability and the verification status of each
              runtime and provider.
              {/* min-h-11 because this is a standalone control rather than a
                  link inside a sentence, and its twin on /contact is already
                  44px. Measured at 320, 390 and 1440 against the built export:
                  it was the one control on either page below the target size,
                  and the same pattern on the sibling page was not. */}
              <span className="mt-3 block">
                <a
                  href={`${REPO_URL}/blob/main/docs/plan/STATUS.md`}
                  className="inline-flex min-h-11 items-center text-black underline decoration-black/20 underline-offset-4"
                >
                  Read the status ledger
                </a>
              </span>
            </Callout>
          </div>
        </div>
      </PageSection>

      <PageSection tone="panel">
        <PageHeading
          kicker="How it works"
          title="<strong>Build. Test. Review. Clean up.</strong>"
        />
        <Steps items={RUN_STEPS} />
      </PageSection>

      <PageSection tone="plain">
        <div className="grid grid-cols-2 gap-x-20 gap-y-12 max-lg:grid-cols-1">
          <div>
            <PageHeading title="<strong>Inspectable where trust matters.</strong>" />
            <Prose className="mt-8">
              <p>
                The repository is MIT licensed except for the separately licensed <code>ee/</code>{" "}
                directory. The community build excludes that directory rather than hiding it behind
                a runtime switch. The customer-side engine, masking, containment, and cleanup paths
                remain inspectable where they handle sensitive state.
              </p>
              <p className="mt-5">
                See the <a href={`${REPO_URL}/blob/main/ee/LICENSE.md`}>open-core license boundary</a> and the{" "}
                <Link prefetch={false} href="/product/architecture">control-plane architecture</Link>.
              </p>
            </Prose>
          </div>
          <div>
            <PageHeading title="<strong>Understand what each result covers.</strong>" />
            <Prose className="mt-8">
              <p>
                Review each result alongside the checks that ran and the environment they used. The report identifies missing coverage so your team can decide what to test next.
              </p>
              <p className="mt-5">
                Read the <Link prefetch={false} href="/terms">terms and stated limits</Link> or the{" "}
                <a href="/docs/security/releases">release-security model</a>.
              </p>
            </Prose>
          </div>
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product", title: "Product", description: "The parts of a run and the evidence they produce." },
          { href: "/privacy", title: "Privacy", description: "Data boundaries, the processors this site uses, and your controls." },
          { href: "/contact", title: "Contact", description: "Documented routes for private and public questions." },
        ]}
      />
    </PageShell>
  );
}
