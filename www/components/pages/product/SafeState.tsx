import { Illustrative } from "@/components/layout/Illustrative";
import { Callout, FeatureGrid, PageHeading, PageHero, PageSection, PageShell, RelatedGrid, Split } from "@/components/pages/kit";
import { PSS01, PSS02, PSS03, PSS04 } from "@/components/pages/figures/product-safe-state";

export function SafeStatePage() {
  return (
    <PageShell>
      <PageHero
        path="/product/safe-state"
        eyebrow="Safe State Engine"
        title="Realistic test data. Private customer details."
        lead="Restore and mask Postgres data in your own infrastructure. Keep the relationships and edge cases your tests need while replacing personal details and credentials."
        framed={false}
        visual={<PSS01 />}
      />
      <PageSection>
        <PageHeading title="<strong>Keep the data that reveals the bug.</strong>" />
        <FeatureGrid
          items={[
            { title: "Snapshot restore", body: "Logical restore for portability, or provider-native copy-on-write branches when supported." },
            { title: "Referential subsets", body: "Keep joins valid. Long-tail and malformed historical state stay in the subset." },
            { title: "Deterministic masking", body: "Format-preserving replacement inside the customer boundary." },
            { title: "Remove production credentials", body: "A session token is deleted. A key, a secret and a password become a keyed hash of the same length that unlocks nothing." },
            { title: "Free-text PII", body: "Scan for emails, cards, phones, and keys that schema rules miss." },
            { title: "Evidence report", body: "The tables, columns and rows sampled, every detector finding, and a signed sanitization attestation. A rule naming a column the schema no longer has is refused by name." },
          ]}
        />
      </PageSection>
      <PageSection tone="ruled">
        <Split visual={<PSS02 />}>
          <PageHeading title="<strong>Smaller datasets with the relationships intact.</strong>" />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Mask the full database or choose a subset starting from a seed table. Subsetting follows relationships and preserves rare records that can expose migration failures.
          </p>
          <Illustrative className="mt-6">
            Example subset. Removing a parent also removes its dependent rows.
          </Illustrative>
          <div className="mt-8">
            <Callout label="Unverified goldens" tone="block">
              An unverified golden cannot be branched. Sanitization evidence is required before a snapshot
              becomes a reusable golden.
            </Callout>
          </div>
        </Split>
      </PageSection>
      <PageSection>
        <Split visual={<PSS03 />}>
          <PageHeading title="<strong>A complete preparation flow for Postgres.</strong>" />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Restore a snapshot, choose the rows to keep, and apply masking rules. Antifailure then samples the result for sensitive data and records the checks in a signed attestation.
          </p>
        </Split>
      </PageSection>
      <PageSection tone="panel">
        <Split visual={<PSS04 />}>
          <PageHeading
            kicker="Customer-hosted masking"
            title="<strong>Mask your data where it already lives.</strong>"
          />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Deterministic masking runs inside the customer-hosted data plane. Raw snapshots, secrets, and
            captured request bodies do not enter the hosted control plane.
          </p>
          <div className="mt-8">
            <Callout label="Customer boundary">
              The hosted control plane receives the attestation, including hashes, coverage, and verification results.
            </Callout>
          </div>
        </Split>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/firewall", title: "Side-Effect Firewall", description: "Control external calls during a test run." },
          { href: "/product/twins", title: "Isolated Twin", description: "Where the sanitized state is restored." },
          { href: "/product/migrations", title: "Migration Safety", description: "Measure locks, rewrites, and query plan changes." },
        ]}
      />
    </PageShell>
  );
}
