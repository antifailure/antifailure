import { Button } from "@/components/layout/Button";
import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Steps,
} from "@/components/pages/kit";

const SUCCESS = [
  {
    title: "Connect in one session",
    body: "Three partners can connect a repository and runner in a single working session.",
  },
  {
    title: "A real upcoming deploy",
    body: "The run targets an actual migration the team is nervous about — not a sandbox app.",
  },
  {
    title: "A finding staging missed",
    body: "At least one defect their normal process did not surface, with a cause attached.",
  },
  {
    title: "Deterministic replay",
    body: "The finding can be reproduced without another exploratory loop.",
  },
  {
    title: "Containment holds",
    body: "No production data and no real-world action escaped the twin. Cleanup is attested.",
  },
  {
    title: "Repeat with less help",
    body: "A later pull request runs with less founder assistance, then a willingness to pay.",
  },
];

export function DesignPartnersPage() {
  return (
    <PageShell>
      <PageHero
        path="/design-partners"
        eyebrow="Design partners"
        title="One nervous deployment. Not a generic demo."
        lead="The recommended next action is not building the universal platform. It is securing one real risky migration and making a correct, useful decision about it."
        actions={
          <>
            <Button href="/signup">Apply</Button>
            <Button href="/solutions/migrations" theme="outlined">
              Schema migrations
            </Button>
          </>
        }
      />
      <PageSection tone="sage">
        <div className="max-w-[920px]">
          <div className="font-mono text-xs font-medium uppercase tracking-snug text-black/70">The offer</div>
          <h2 className="mt-6 text-[44px] leading-dense tracking-tighter text-gray-new-40 max-xl:text-[36px] max-lg:text-[28px] max-md:text-[24px] [&>strong]:font-normal [&>strong]:text-black">
            <strong>Give us one deployment your team is nervous about.</strong> We will create an
            isolated production-shaped test, run the change, and show you what your existing staging
            process misses.
          </h2>
          <p className="mt-8 max-w-[640px] text-[18px] leading-7 tracking-extra-tight text-gray-new-40">
            The pilot centers on an actual upcoming schema change or risky release. We do not start
            from a toy fixture, a synthetic demo tenant, or a promise of perfect cloud clones.
          </p>
          <div className="mt-10">
            <Button href="/signup">Apply as a design partner</Button>
          </div>
        </div>
      </PageSection>
      <PageSection>
        <PageHeading
          kicker="Success criteria"
          title="<strong>Success is a finding staging missed</strong> — reproduced, contained, and destroyed."
        />
        <ol className="mt-14 grid grid-cols-2 gap-x-16 gap-y-12 max-md:grid-cols-1 max-md:gap-y-8">
          {SUCCESS.map((item, i) => (
            <li key={item.title} className="min-w-0 border-t border-black/10 pt-6">
              <div className="font-mono text-[12px] tracking-extra-tight text-[#33bf00]">
                {String(i + 1).padStart(2, "0")}
              </div>
              <h3 className="mt-3 text-[22px] leading-snug tracking-tighter text-black">{item.title}</h3>
              <p className="mt-2 max-w-[420px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                {item.body}
              </p>
            </li>
          ))}
        </ol>
      </PageSection>
      <PageSection tone="white">
        <PageHeading title="<strong>Four steps.</strong> Pick the deploy, run the twin, read the report, repeat." />
        <div className="mt-12">
          <Steps
            items={[
              { title: "Pick the deploy", body: "A real upcoming migration, not a sandbox app." },
              { title: "Run the twin", body: "Isolated, sanitized, fail-closed." },
              { title: "Read the report", body: "Pass, warning, or block with a cause." },
              { title: "Repeat", body: "A later PR with less founder assistance." },
            ]}
          />
        </div>
        <div className="mt-12 max-w-[720px]">
          <Callout label="Willingness to pay">
            The pilot is successful when partners would pay to keep running twins on later pull
            requests — not when they enjoyed a demo. Pricing after the pilot is operational value,
            not AI personalities.
          </Callout>
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/solutions/migrations", title: "Schema migrations", description: "The intended first pilot." },
          { href: "/pricing", title: "Pricing", description: "What paid use looks like after the pilot." },
          { href: "/signup", title: "Sign up", description: "Join the waitlist." },
        ]}
      />
    </PageShell>
  );
}
