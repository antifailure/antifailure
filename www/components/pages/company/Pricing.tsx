import { Button } from "@/components/layout/Button";
import { cn } from "@/lib/cn";
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
    cta: { href: "/docs", label: "Inspect the surface", theme: "outlined" },
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
    cta: { href: "/signup", label: "Start a design partnership", theme: "green" },
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

const VALUE_METRICS: [string, string][] = [
  ["Applications protected", "The systems the twin actually covers — not a personality count."],
  ["Deployment runs", "Safety validations attached to a pull request or release."],
  ["Environment execution", "Minutes the twin is provisioned, exercised, and destroyed."],
  ["Data volume", "Sanitized, referential state restored inside your boundary."],
  ["Peak workload", "Deterministic concurrency and traffic shape, not LLM fan-out."],
  ["Governance and support", "Policy scope, evidence retention, residency, and response level."],
];

function PlanCard({ plan }: { plan: Plan }) {
  return (
    <article
      className={cn(
        "flex h-full flex-col border-t border-black/15 pt-6",
        plan.featured && "border-black/40",
      )}
    >
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="text-[18px] tracking-extra-tight text-black">{plan.name}</h3>
        {plan.badge ? (
          <span className="text-[12px] tracking-extra-tight text-gray-new-40">{plan.badge}</span>
        ) : null}
      </div>
      <div className="mt-5 font-title text-[36px] leading-none tracking-tighter text-black max-xl:text-[32px]">
        {plan.price}
      </div>
      <div className="mt-2 text-[14px] tracking-extra-tight text-gray-new-40">{plan.period}</div>
      {plan.secondary ? (
        <div className="mt-5">
          <div className="text-[12px] tracking-extra-tight text-gray-new-40">{plan.secondary.label}</div>
          <div className="mt-1 text-[20px] tracking-tighter text-black">{plan.secondary.value}</div>
          <div className="mt-1 text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
            {plan.secondary.hint}
          </div>
        </div>
      ) : null}
      <p className="mt-5 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">{plan.tagline}</p>
      <div className="mt-6">
        <Button href={plan.cta.href} theme={plan.cta.theme ?? "filled"} className="max-md:w-full">
          {plan.cta.label}
        </Button>
      </div>
      <ul className="mt-8 flex flex-1 flex-col border-t border-black/10 pt-6">
        {plan.includes.map((item) => (
          <li
            key={item}
            className="border-b border-black/8 py-2.5 text-[14px] leading-6 tracking-extra-tight text-black/80 last:border-0"
          >
            {item}
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
            <Button href="/docs" theme="outlined">
              Read the docs
            </Button>
          </>
        }
      />
      <PageSection className="pt-0">
        <ul className="grid grid-cols-3 items-stretch gap-x-12 max-xl:grid-cols-1 max-xl:gap-y-12">
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
        <div className="mt-14">
          <SpecTable rows={VALUE_METRICS} />
        </div>
      </PageSection>
      <PageSection tone="sage">
        <PageHeading title="<strong>Illustrative, not a quote.</strong> Customer-cloud execution exists to control margin and data exposure." />
        <div className="mt-10 max-w-[720px]">
          <Callout label="Hosted compute is not free">
            Unlimited free hosted compute is not viable. Free usage has strict credits, or it
            requires your own cloud and model credentials. Community stays useful because it runs
            on infrastructure you already pay for.
          </Callout>
        </div>
        <Prose className="mt-8">
          <p>
            Early Team and Growth figures are planning ranges from the August 2026 brief. Enterprise
            depends on scale and governance scope. Nothing here is an offer, invoice, or committed
            rate.
          </p>
        </Prose>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/docs", title: "Docs", description: "How a twin run works." },
          { href: "/product", title: "Product", description: "The modules that make a decision." },
          { href: "/signup", title: "Sign up", description: "Join the waitlist." },
        ]}
      />
    </PageShell>
  );
}
