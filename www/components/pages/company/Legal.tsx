import {
  Callout,
  FeatureGrid,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  Prose,
  RelatedGrid,
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
        path="/privacy"
        eyebrow="Privacy Notice"
        title="Production data stays in the customer boundary."
        lead="The hosted control plane holds organizations, policy, aggregated reports, and billing. Raw snapshots, secrets, and captured request bodies stay in your cloud by default."
      />
      <PageSection>
        <PageHeading
          kicker="Trust boundary"
          title="<strong>Two planes.</strong> Evidence can leave. Records of production should not."
        />
        <div className="mt-14 grid grid-cols-2 gap-5 max-md:grid-cols-1">
          <div className="rounded-[12px] bg-white p-8 ring-1 ring-black/10 max-md:p-6">
            <div className="mb-3 size-2 rounded-full bg-black" />
            <h3 className="text-[22px] tracking-tighter text-black">Control plane</h3>
            <p className="mt-3 text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
              Organization metadata, account emails, GitHub identifiers, policy configuration,
              aggregated reports, historical comparisons, and billing entitlements.
            </p>
          </div>
          <div className="rounded-[12px] bg-[#E4F1EB] p-8 ring-1 ring-black/10 max-md:p-6">
            <div className="mb-3 size-2 rounded-full bg-[#33bf00]" />
            <h3 className="text-[22px] tracking-tighter text-black">Your boundary</h3>
            <p className="mt-3 text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
              Raw snapshots, secrets, captured request bodies until redacted, raw logs and traces,
              sanitization, provisioning, egress enforcement, and cleanup.
            </p>
          </div>
        </div>
      </PageSection>
      <PageSection tone="white">
        <PageHeading title="<strong>Sanitization happens where the data already lives.</strong>" />
        <FeatureGrid
          items={[
            { title: "Customer-hosted data plane", body: "Masking, subsetting, and credential deletion execute inside your cloud." },
            { title: "Outbound-only agent", body: "Communication leaves the customer agent where possible, with short-lived credentials." },
            { title: "No snapshots in the host", body: "The hosted control plane is not a backup target for production-derived state." },
          ]}
        />
      </PageSection>
      <PageSection>
        <div className="max-w-[720px]">
          <Callout label="Product intent, not a filed policy" tone="warn">
            This notice describes architecture from the August 2026 brief. It is not a substitute for
            a counsel-reviewed privacy policy once the hosted control plane is generally available.
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
          { href: "/security", title: "Security", description: "Trust boundary and fail closed." },
          { href: "/terms", title: "Terms of Use", description: "The promise is evidence, not zero-failure." },
          { href: "/open-source", title: "Open source", description: "Inspect the data-plane components." },
        ]}
      />
    </PageShell>
  );
}

export function TermsPage() {
  return (
    <PageShell>
      <PageHero
        path="/terms"
        eyebrow="Terms of Use"
        title="A proving ground, not a guarantee."
        lead="The product reports whether a deployment is safe to ship under the conditions it could observe and reproduce. It does not mathematically guarantee that a deployment cannot fail."
      />
      <PageSection>
        <PageHeading
          kicker="Scope"
          title="<strong>Evidence under stated fidelity.</strong> You remain responsible for the permissions you grant."
        />
        <ul className="mt-14 grid grid-cols-3 gap-5 max-lg:grid-cols-1">
          <li className="rounded-[12px] bg-white p-7 ring-1 ring-black/10">
            <div className="mb-3 size-2 rounded-full bg-black" />
            <h3 className="text-[18px] tracking-extra-tight text-black">The promise</h3>
            <p className="mt-3 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
              Reproduce the highest-risk production conditions we can observe, measure how the
              proposed system behaves, and expose dangerous differences with concrete evidence.
            </p>
          </li>
          <li className="rounded-[12px] bg-white p-7 ring-1 ring-black/10">
            <div className="mb-3 size-2 rounded-full bg-black" />
            <h3 className="text-[18px] tracking-extra-tight text-black">Your cloud</h3>
            <p className="mt-3 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
              You remain responsible for the cloud permissions you grant the agent, the policies
              you approve, and the production systems those permissions can reach.
            </p>
          </li>
          <li className="rounded-[12px] bg-white p-7 ring-1 ring-black/10">
            <div className="mb-3 size-2 rounded-full bg-black" />
            <h3 className="text-[18px] tracking-extra-tight text-black">Accounts</h3>
            <p className="mt-3 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
              Sign-in is for the waitlist. There is no public production control plane yet, and
              these terms are not a paid-service agreement.
            </p>
          </li>
        </ul>
      </PageSection>
      <PageSection tone="sage">
        <PageHeading title="<strong>What these terms do not say.</strong>" />
        <ul className="mt-12 grid grid-cols-2 gap-4 max-md:grid-cols-1">
          {NOT_CLAIMED.map((claim) => (
            <li key={claim} className="rounded-[12px] bg-white/80 px-6 py-5 ring-1 ring-black/10">
              <div className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-red-700">
                Not claimed
              </div>
              <p className="mt-2 text-[17px] leading-snug tracking-extra-tight text-black/40 line-through">
                {claim}
              </p>
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
          { href: "/security", title: "Security", description: "Fail closed and the data boundary." },
          { href: "/company", title: "About", description: "What the company will not claim." },
        ]}
      />
    </PageShell>
  );
}
