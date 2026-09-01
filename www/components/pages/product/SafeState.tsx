import { Illustrative } from "@/components/layout/Illustrative";
import { Callout, FeatureGrid, PageHeading, PageHero, PageSection, PageShell, RelatedGrid, Split } from "@/components/pages/kit";
import { PSS01, PSS02, PSS03, PSS04 } from "@/components/pages/figures/product";

export function SafeStatePage() {
  return (
    <PageShell>
      <PageHero
        path="/product/safe-state"
        eyebrow="Safe State Engine"
        title="Production-shaped Postgres without production identities."
        lead="Snapshot restore, referentially consistent subsetting, and deterministic masking inside the customer boundary. A live session is deleted outright, and a key is replaced by a keyed hash that grants nothing. The output is a sanitization evidence report, not a dataset."
        framed={false}
        visual={<PSS01 />}
      />
      <PageSection>
        <PageHeading title="<strong>Realistic enough to fail for the right reasons.</strong> Toy fixtures miss the row that breaks the constraint." />
        <FeatureGrid
          items={[
            { title: "Snapshot restore", body: "Logical restore for portability, or provider-native copy-on-write branches when supported." },
            { title: "Referential subsets", body: "Keep joins valid. Long-tail and malformed historical state stay in the subset." },
            { title: "Deterministic masking", body: "Format-preserving replacement with uniqueness preserved, inside the customer boundary." },
            { title: "Nothing that grants access survives", body: "A session token is deleted. A key, a secret and a password become a keyed hash of the same length that unlocks nothing." },
            { title: "Free-text PII", body: "Scan for emails, cards, phones, and keys that schema rules miss." },
            { title: "Evidence report", body: "Distribution validation, schema-drift handling, and a signed sanitization attestation." },
          ]}
        />
      </PageSection>
      <PageSection tone="ruled">
        <Split visual={<PSS02 />}>
          <PageHeading title="<strong>A 12% subset that still joins.</strong> Dropped parents take their children. Rare rows stay." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Subsetting is available and off by default, so a first run masks the whole database and
            you turn subsetting on with a seed table when you want it. With it on, the twin keeps
            referential integrity, long-tail billing states, and malformed history, the records that
            actually break migrations, while volume stays bounded.
          </p>
          <Illustrative className="mt-6">
            Six rows of a worked example, with the ratio chosen. What a real subset keeps depends on
            the shape of your own data. What is fixed is the rule: a dropped parent takes its
            children, and a rare row is kept on purpose rather than sampled away.
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
          <PageHeading title="<strong>Postgres first.</strong> Deep enterprise data platforms can be an external provider, not a rebuild." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            The built-in engine covers common Postgres cases: restore, subset, mask, delete credentials,
            validate distribution, then destroy. Matching a dedicated test-data platform’s connector depth
            is not the point. What this returns is a decision about a deployment, not a dataset.
          </p>
        </Split>
      </PageSection>
      <PageSection tone="panel">
        <Split visual={<PSS04 />}>
          <PageHeading
            kicker="Customer-hosted masking"
            title="<strong>Masking never leaves your cloud.</strong> The control plane receives evidence, not records."
          />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Deterministic masking runs inside the customer-hosted data plane. Raw snapshots, secrets, and
            captured request bodies do not enter the hosted control plane.
          </p>
          <div className="mt-8">
            <Callout label="Customer boundary">
              Production data stays in the customer boundary. What crosses the trust boundary is a
              sanitization attestation: hashes, coverage, and whether it verified. Not the rows.
            </Callout>
          </div>
        </Split>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/firewall", title: "Side-Effect Firewall", description: "The twin cannot act on the real world." },
          { href: "/product/architecture", title: "Architecture", description: "Control plane and customer data plane." },
          { href: "/product/migrations", title: "Migration Safety", description: "What a branch with production's shape shows." },
        ]}
      />
    </PageShell>
  );
}
