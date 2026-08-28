import { Button } from "@/components/layout/Button";
import { cn } from "@/lib/cn";
import {
  Callout,
  FeatureGrid,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
} from "@/components/pages/kit";

type PlanCta = {
  href: string;
  label: string;
  theme?: "filled" | "outlined" | "green";
};

type Plan = {
  name: string;
  badge?: string;
  price: string;
  period: string;
  secondary?: { label: string; value: string; hint: string };
  tagline: string;
  featured?: boolean;
  cta: PlanCta;
  includes: string[];
};

const PLANS: Plan[] = [
  {
    name: "Community",
    price: "$0",
    period: "local engine, forever",
    tagline: "Open-source proving ground on your machine and your cloud.",
    cta: { href: "/open-source", label: "Inspect the surface", theme: "outlined" },
    includes: [
      "Docker Compose and basic Postgres support",
      "Bring-your-own infrastructure and model keys",
      "Community simulators and local reports",
      "Inspectable agent, sanitization, egress, and cleanup",
      "No hosted compute — your cloud, your credentials",
    ],
  },
  {
    name: "Team",
    badge: "Illustrative",
    price: "$500–$2,000",
    period: "per month · one application",
    tagline: "Hosted control plane, included run credits, usage beyond the floor.",
    featured: true,
    cta: { href: "/design-partners", label: "Start a design partnership", theme: "green" },
    includes: [
      "Base platform fee per organization",
      "Included run credits for deployment twins",
      "Usage for environment minutes, data volume, and workload execution",
      "Customer-cloud execution for margin and data exposure",
      "Pull-request checks and a private preview URL",
    ],
  },
  {
    name: "Growth + Enterprise",
    price: "$2,000–$8,000",
    period: "per month · Growth band",
    secondary: {
      label: "Enterprise",
      value: "$30,000–$250,000+",
      hint: "annually · scale, governance, residency, fleet",
    },
    tagline: "More repositories, organization policy, and the controls enterprises buy.",
    cta: { href: "/signup", label: "Talk to us", theme: "outlined" },
    includes: [
      "More repositories, volume, and peak workload",
      "Organization-wide release policy",
      "Governance, evidence retention, and residency",
      "Fleet management and premium connectors",
      "Support and service-level commitments — sold when references exist",
    ],
  },
];

const VALUE_METRICS = [
  { title: "Applications protected", body: "The systems the twin actually covers — not a personality count." },
  { title: "Deployment runs", body: "Safety validations attached to a pull request or release." },
  { title: "Environment execution", body: "Minutes the twin is provisioned, exercised, and destroyed." },
  { title: "Data volume", body: "Sanitized, referential state restored inside your boundary." },
  { title: "Peak workload", body: "Deterministic concurrency and traffic shape, not LLM fan-out." },
  { title: "Governance and support", body: "Policy scope, evidence retention, residency, and response level." },
];

function PlanCard({ plan }: { plan: Plan }) {
  return (
    <article
      className={cn(
        "flex h-full flex-col rounded-[12px] p-8 ring-1 max-md:p-6",
        plan.featured
          ? "bg-[#E4F1EB] ring-black/20"
          : "bg-white ring-black/10",
      )}
    >
      <div className="flex flex-1 flex-col">
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-[18px] tracking-extra-tight text-black">{plan.name}</h3>
          {plan.badge ? (
            <span className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-black/50">
              {plan.badge}
            </span>
          ) : null}
        </div>
        <div className="mt-6">
          <div className="font-title text-[44px] leading-none tracking-tighter text-black max-xl:text-[36px]">
            {plan.price}
          </div>
          <div className="mt-2 text-[14px] tracking-extra-tight text-gray-new-40">{plan.period}</div>
        </div>
        {plan.secondary ? (
          <div className="mt-6 border-t border-black/10 pt-5">
            <div className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-black/50">
              {plan.secondary.label}
            </div>
            <div className="mt-2 text-[22px] tracking-tighter text-black">{plan.secondary.value}</div>
            <div className="mt-1 text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
              {plan.secondary.hint}
            </div>
          </div>
        ) : null}
        <p className="mt-6 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">{plan.tagline}</p>
      </div>
      <div className="mt-8">
        <Button href={plan.cta.href} theme={plan.cta.theme ?? "filled"} className="w-full">
          {plan.cta.label}
        </Button>
      </div>
      <ul className="mt-8 flex flex-col gap-3 border-t border-black/10 pt-7">
        {plan.includes.map((item) => (
          <li key={item} className="flex gap-3 text-[14px] leading-6 tracking-extra-tight text-black/80">
            <span className="mt-2 size-1.5 shrink-0 rounded-full bg-[#33bf00]" aria-hidden />
            <span>{item}</span>
          </li>
        ))}
      </ul>
    </article>
  );
}

export function PricingPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Pricing"
        title="Operational value, not AI personalities."
        lead="Community is the local engine. Team is a platform fee plus run usage. Growth and Enterprise add volume, policy, and governance. These bands are illustrative — not a quote."
        actions={
          <>
            <Button href="/signup">Get started</Button>
            <Button href="/design-partners" theme="outlined">
              Design partners
            </Button>
          </>
        }
      />
      <PageSection className="pt-0">
        <ul className="grid grid-cols-3 items-stretch gap-5 max-lg:grid-cols-1">
          {PLANS.map((plan) => (
            <li key={plan.name} className="min-w-0">
              <PlanCard plan={plan} />
            </li>
          ))}
        </ul>
      </PageSection>
      <PageSection tone="white">
        <PageHeading
          kicker="Value metrics"
          title="<strong>We meter what the twin actually does.</strong> Not how many exploratory users you named."
        />
        <FeatureGrid items={VALUE_METRICS} />
      </PageSection>
      <PageSection tone="sage">
        <PageHeading title="<strong>Illustrative, not a quote.</strong> Customer-cloud execution exists to control margin and data exposure." />
        <div className="mt-10 max-w-[720px]">
          <Callout label="Hosted compute is not free">
            Unlimited free hosted compute is not viable. Free usage has strict credits, or it requires
            your own cloud and model credentials. Community stays useful because it runs on infrastructure
            you already pay for.
          </Callout>
        </div>
        <p className="mt-8 max-w-[560px] text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
          Early Team and Growth figures are planning ranges from the August 2026 brief. Enterprise
          depends on scale and governance scope. Nothing here is an offer, invoice, or committed rate.
        </p>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/open-source", title: "Open source", description: "What stays inspectable inside your boundary." },
          { href: "/design-partners", title: "Design partners", description: "Start with one nervous deploy." },
          { href: "/company", title: "About", description: "Why the company exists." },
        ]}
      />
    </PageShell>
  );
}
