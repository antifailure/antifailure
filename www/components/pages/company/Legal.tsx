import { Button } from "@/components/layout/Button";
import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  Prose,
  RelatedGrid,
  SpecTable,
} from "@/components/pages/kit";

const NOT_CLAIMED = [
  "Zero rollback. No deployment can ever fail.",
  "Perfect clones of every cloud topology.",
  "Open source as a substitute for compliance.",
  "A generally available production control plane.",
];

export function PrivacyPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Privacy Notice"
        title="Production data stays in the customer boundary."
        lead="The hosted control plane holds organizations, policy, aggregated reports, and billing. Raw snapshots, secrets, and captured request bodies stay in your cloud by default."
        actions={
          <>
            <Button href="/terms" theme="outlined">
              Terms of Use
            </Button>
            <Button href="/docs" theme="outlined">
              Read the docs
            </Button>
          </>
        }
      />
      <PageSection>
        <PageHeading
          kicker="Trust boundary"
          title="<strong>Two planes.</strong> Evidence can leave. Records of production should not."
        />
        <div className="mt-14">
          <SpecTable
            rows={[
              [
                "Control plane",
                "Organization metadata, account emails, GitHub identifiers, policy configuration, aggregated reports, historical comparisons, and billing entitlements.",
              ],
              [
                "Your boundary",
                "Raw snapshots, secrets, captured request bodies until redacted, raw logs and traces, sanitization, provisioning, egress enforcement, and cleanup.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <PageHeading title="<strong>Sanitization happens where the data already lives.</strong>" />
        <div className="mt-14">
          <SpecTable
            rows={[
              [
                "Customer-hosted data plane",
                "Masking, subsetting, and credential deletion execute inside your cloud.",
              ],
              [
                "Outbound-only agent",
                "Communication leaves the customer agent where possible, with short-lived credentials.",
              ],
              [
                "No snapshots in the host",
                "The hosted control plane is not a backup target for production-derived state.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection>
        <div className="max-w-[720px]">
          <Callout label="Product intent, not a filed policy" tone="warn">
            This notice describes architecture from the August 2026 brief. It is not a substitute
            for a counsel-reviewed privacy policy once the hosted control plane is generally
            available.
          </Callout>
        </div>
        <Prose className="mt-10">
          <p>
            Sign-in today is for the waitlist. There is no public production control plane yet. If
            that changes, this page will be replaced with a dated policy that names a legal entity,
            subprocessors, and retention periods — not a restatement of intent.
          </p>
        </Prose>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/terms", title: "Terms of Use", description: "The promise is evidence, not zero-failure." },
          { href: "/docs", title: "Docs", description: "How a twin run works." },
          { href: "/pricing", title: "Pricing", description: "Community, team, and enterprise." },
        ]}
      />
    </PageShell>
  );
}

export function TermsPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Terms of Use"
        title="A proving ground, not a guarantee."
        lead="The product reports whether a deployment is safe to ship under the conditions it could observe and reproduce. It does not mathematically guarantee that a deployment cannot fail."
        actions={
          <>
            <Button href="/privacy" theme="outlined">
              Privacy Notice
            </Button>
            <Button href="/docs" theme="outlined">
              Read the docs
            </Button>
          </>
        }
      />
      <PageSection>
        <PageHeading
          kicker="Scope"
          title="<strong>Evidence under stated fidelity.</strong> You remain responsible for the permissions you grant."
        />
        <div className="mt-14">
          <SpecTable
            rows={[
              [
                "The promise",
                "Reproduce the highest-risk production conditions we can observe, measure how the proposed system behaves, and expose dangerous differences with concrete evidence.",
              ],
              [
                "Your cloud",
                "You remain responsible for the cloud permissions you grant the agent, the policies you approve, and the production systems those permissions can reach.",
              ],
              [
                "Accounts",
                "Sign-in is for the waitlist. There is no public production control plane yet, and these terms are not a paid-service agreement.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="sage">
        <PageHeading title="<strong>What these terms do not say.</strong>" />
        <ul className="mt-12 flex flex-col border-t border-black/10">
          {NOT_CLAIMED.map((claim) => (
            <li
              key={claim}
              className="border-b border-black/10 py-4 text-[17px] leading-snug tracking-extra-tight text-black"
            >
              {claim}
            </li>
          ))}
        </ul>
      </PageSection>
      <PageSection>
        <div className="max-w-[720px]">
          <Callout label="Not a counsel-reviewed agreement" tone="warn">
            This page states product limits from the August 2026 brief so waitlist visitors are not
            sold a zero-failure guarantee. It is not a substitute for terms of service, a DPA, or
            an order form.
          </Callout>
        </div>
        <Prose className="mt-10">
          <p>
            When a hosted control plane is generally available, these pages will be replaced with
            dated legal documents that name a contracting entity, governing law, and acceptable
            use. Until then, treat every safety report as evidence under the fidelity the run
            disclosed — pass, warning, or block — not as insurance.
          </p>
        </Prose>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/privacy", title: "Privacy Notice", description: "What we collect and never take." },
          { href: "/docs", title: "Docs", description: "How a twin run works." },
          { href: "/pricing", title: "Pricing", description: "Community, team, and enterprise." },
        ]}
      />
    </PageShell>
  );
}
