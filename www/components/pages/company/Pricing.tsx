import { Button } from "@/components/layout/Button";
import { Chevron } from "@/components/icons";
import { cn } from "@/lib/cn";
import {
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
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
      "No hosted compute. Your cloud, your credentials",
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
      "Pull-request checks and aggregated reports across repositories",
    ],
  },
  {
    name: "Growth + Enterprise",
    badge: "Illustrative",
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
      "Support and service-level commitments, sold when references exist",
    ],
  },
];

const VALUE_METRICS: [string, string][] = [
  ["Applications protected", "The systems the twin actually covers, not a count of personalities."],
  ["Deployment runs", "Safety validations attached to a pull request or release."],
  ["Environment execution", "Minutes the twin is provisioned, exercised, and destroyed."],
  ["Data volume", "Sanitized, referential state restored inside your boundary."],
  ["Peak workload", "Traffic shape and concurrency, not model fan-out."],
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
        lead="Community is the local engine and it works today. Team is a platform fee plus run usage. Growth and Enterprise add volume, policy, and governance. These bands are illustrative, not a quote."
        actions={
          <>
            <Button href="/signup">Join the waitlist</Button>
            <Button href="/docs" theme="outlined">
              Read the docs
            </Button>
          </>
        }
      />
      <PageSection className="pt-0">
        <p className="mb-14 max-w-[720px] border-l border-black/15 pl-6 text-[16px] leading-7 tracking-extra-tight text-gray-new-40 max-md:mb-10 max-md:pl-4">
          The hosted control plane is in development. Today the engine runs in your own continuous
          integration, on your own compute, and every button on this page leads to a waitlist. Team
          and Enterprise are open for design partners.
        </p>
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
          title="<strong>We meter what the twin actually does.</strong> Not how many agents you named."
        />
        <div className="mt-14 max-w-[960px] pl-24 max-xl:pl-16 max-md:pl-0">
          {VALUE_METRICS.map(([title, body]) => (
            <details
              key={title}
              className="group border-b border-black/10 first:border-t"
            >
              <summary className="flex cursor-pointer list-none items-center justify-between gap-6 py-5 text-[18px] tracking-extra-tight text-black [&::-webkit-details-marker]:hidden">
                {title}
                <Chevron className="h-2.5 w-2.5 shrink-0 text-black/40 transition-transform duration-200 group-open:rotate-180" />
              </summary>
              <p className="pb-5 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                {body}
              </p>
            </details>
          ))}
        </div>
      </PageSection>
    </PageShell>
  );
}
