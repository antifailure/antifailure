import {
  Callout,
  FeatureGrid,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
} from "@/components/pages/kit";

const TRIAD = [
  {
    role: "Developer",
    title: "One click from the pull request.",
    body: "Turns the change into a private, production-shaped environment with its own safe database and integrations. Run it, inspect the evidence, destroy everything automatically.",
  },
  {
    role: "Platform",
    title: "Policy instead of a shared stage.",
    body: "Replace fragile shared staging with ephemeral deployment validation inside your cloud. Isolation, cost ceilings, and cleanup are enforced — not ticketed.",
  },
  {
    role: "Executive",
    title: "Smaller blast radius on risky releases.",
    body: "Reduce the probability and blast radius of high-risk rollouts by validating them under production-shaped conditions before they reach customers. Evidence, not a guarantee.",
  },
];

const NOT_CLAIMED = [
  "Zero rollback guarantee",
  "No deployment can ever fail",
  "Thousands of AI agents behave exactly like humans",
  "One click perfectly clones every cloud",
  "Open source bypasses compliance",
];

export function CompanyPage() {
  return (
    <PageShell>
      <PageHero
        path="/company"
        eyebrow="Company"
        title="Answer one question better than any individual tool."
        lead="Is this deployment safe to ship under the conditions that actually matter? Exploratory users, sanitization, E2E execution, and preview deploy are components. None of them alone defines the company."
      />
      <PageSection>
        <PageHeading
          kicker="Messaging"
          title="<strong>Know what happens before you deploy.</strong> Three audiences. The same proving ground."
        />
        <ul className="mt-16 grid grid-cols-3 gap-5 max-lg:grid-cols-1">
          {TRIAD.map((item, i) => (
            <li
              key={item.role}
              className="flex min-h-[280px] flex-col rounded-[12px] bg-white p-8 ring-1 ring-black/10 max-md:min-h-0 max-md:p-6"
            >
              <div className="flex items-center justify-between">
                <span className="font-mono text-[12px] tracking-extra-tight text-[#33bf00]">
                  {String(i + 1).padStart(2, "0")}
                </span>
                <span className="font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-black/50">
                  {item.role}
                </span>
              </div>
              <h3 className="mt-10 text-[28px] leading-dense tracking-tighter text-black max-xl:text-[24px]">
                {item.title}
              </h3>
              <p className="mt-4 text-[16px] leading-7 tracking-extra-tight text-gray-new-40">{item.body}</p>
            </li>
          ))}
        </ul>
      </PageSection>
      <PageSection tone="sage">
        <PageHeading title="<strong>What we will not claim.</strong> Measurable language, or we do not say it." />
        <ul className="mt-12 grid grid-cols-2 gap-4 max-md:grid-cols-1">
          {NOT_CLAIMED.map((claim) => (
            <li
              key={claim}
              className="rounded-[12px] bg-white/80 px-6 py-5 ring-1 ring-black/10"
            >
              <div className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-red-700">
                Not claimed
              </div>
              <p className="mt-2 text-[18px] leading-snug tracking-extra-tight text-black/35 line-through">
                {claim}
              </p>
            </li>
          ))}
        </ul>
        <p className="mt-10 max-w-[560px] text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
          The promise is a disposable production twin and an evidence-backed pass, warning, or block.
          Not zero-failure. Not a perfect clone of every cloud.
        </p>
      </PageSection>
      <PageSection>
        <PageHeading title="<strong>Pre-production deployment safety.</strong> Postgres migrations first. Open-core." />
        <FeatureGrid
          items={[
            { title: "Category", body: "Pre-production deployment safety. Not AI QA, staging, synthetic-user, or load testing." },
            { title: "Wedge", body: "Automated safety validation for risky Postgres-backed web deployments, especially schema migrations." },
            { title: "Architecture", body: "Open-core. Customer-hosted data plane. Fail closed. Cleanup is a safety property." },
          ]}
        />
        <div className="mt-14 max-w-[720px]">
          <Callout label="Recommended next action">
            Not building the universal platform. Securing one real risky migration and building the
            smallest complete wind tunnel that can make a correct, useful decision about it.
          </Callout>
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product", title: "Product", description: "The modules that make a decision." },
          { href: "/design-partners", title: "Design partners", description: "The recommended next action." },
          { href: "/security", title: "Security", description: "Trust boundary and fail closed." },
        ]}
      />
    </PageShell>
  );
}
