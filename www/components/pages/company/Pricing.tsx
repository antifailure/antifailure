import { Button } from "@/components/layout/Button";
import { Chevron } from "@/components/icons";
import { cn } from "@/lib/cn";
import {
  Faq,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  Prose,
  type FaqItem,
} from "@/components/pages/kit";
import { FREE_PLAN } from "@/lib/plan-facts";

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
    tagline: "The whole engine, on your machine and your cloud, with no account.",
    // The filled action on this page, because it is the only plan on it a
    // visitor can start without somebody else's permission. It used to be an
    // outlined "Inspect the surface" while the invitation wall carried the
    // filled button in the hero above.
    cta: { href: "/docs/getting-started/quickstart", label: "Start the quickstart", theme: "filled" },
    includes: [
      "MIT licensed, installed with one command",
      "Docker Compose and basic Postgres support",
      "The MCP server, af mcp, for a coding agent",
      "Bring-your-own infrastructure and model keys",
      "Inspectable agent, sanitization, egress, and cleanup",
      "No account and no key. Your cloud, your credentials",
    ],
  },
  {
    name: "Team",
    badge: "Illustrative",
    price: "$500 to $2,000",
    period: "per month · one application",
    tagline: "Hosted control plane, included run credits, usage beyond the floor.",
    featured: true,
    cta: { href: "/contact#book", label: "Start a design partnership", theme: "green" },
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
    price: "$2,000 to $8,000",
    period: "per month · Growth band",
    secondary: {
      label: "Enterprise",
      value: "$30,000 to $250,000+",
      hint: "annually · scale, governance, residency, fleet",
    },
    tagline: "More repositories, organization policy, and the controls enterprises buy.",
    cta: { href: "/contact#book", label: "Talk to us", theme: "outlined" },
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

/**
 * What free actually gives you, from the code that enforces it.
 *
 * These three numbers decide whether an organization's next environment is
 * created and they were published nowhere a customer could read. The only way
 * to learn the shape of the free plan was to reach a limit and read the
 * refusal, which is a support ticket that a sentence prevents.
 *
 * The values come from lib/plan-facts.ts rather than from this file, and
 * web/apps/api/test/plan-facts.test.ts fails the build if that file and the
 * enforcement stop agreeing.
 */
function FreePlanFacts() {
  return (
    <ul className="mt-14 grid grid-cols-3 gap-x-12 max-xl:grid-cols-1 max-xl:gap-y-10 max-md:mt-10">
      {Object.entries(FREE_PLAN).map(([key, fact]) => (
        <li key={key} className="min-w-0 border-t border-black/15 pt-6">
          <div className="text-[14px] tracking-extra-tight text-gray-new-40">{fact.label}</div>
          <div className="mt-4 font-title text-[36px] leading-none tracking-tighter text-black max-xl:text-[32px]">
            {fact.value}
          </div>
          <div className="mt-2 text-[14px] tracking-extra-tight text-gray-new-40">{fact.unit}</div>
          <p className="mt-5 text-[15px] leading-6 tracking-extra-tight text-black/80">
            {fact.body}
          </p>
        </li>
      ))}
    </ul>
  );
}

/**
 * The questions a visitor actually arrives with, and the MCP one nobody had
 * answered anywhere a customer looks.
 *
 * Answered here rather than in a paragraph because Faq carries the FAQPage
 * markup from the same array, so each pair is individually quotable by an
 * answer engine and the markup cannot disagree with the rendering.
 */
const PRICING_FAQ: FaqItem[] = [
  {
    question: "Is the MCP server free?",
    answer:
      "Yes, and it needs no account. The MCP server is part of the engine, which is MIT licensed, and your client starts it on your own machine with one command. It serves the rehearsal tools to a coding agent over standard input and output, so there is no hosted component behind it, no key, and no plan attached to it. The enterprise directory carries no MCP code at all.",
  },
  {
    question: "Do I need an account to use Antifailure?",
    answer:
      "No. The quickstart goes from an empty machine to a running environment with no account and no sign-up. An account exists only for the hosted control plane, which coordinates environments across a team and is invitation only while it is in development.",
  },
  {
    question: "Is the engine really open source?",
    answer:
      "Yes, under the MIT license. Everything outside the ee directory is MIT, which is the engine, the command line interface, the masking, the egress gateway, the reports and the MCP server. The ee directory is public source under a separate enterprise license and running it needs a license key.",
  },
  {
    question: "What happens when a free limit is reached?",
    answer:
      "The next environment is refused, with a message naming the limit, what the organization is currently holding, and who can change it. Nothing that already exists is torn down. Taking somebody's running environment away because a number moved is not a behaviour this product has.",
  },
  {
    question: "Can I run the control plane myself?",
    answer:
      "Yes. The control plane is in the same repository and the self-hosting documentation covers it. The three numbers above are the defaults it ships with, so an installation you run yourself enforces the same free plan unless its operator moves an organization to another plan or edits the numbers.",
  },
  {
    question: "What is an environment-hour?",
    answer:
      "One environment, held for one hour. It is the unit the control plane can actually measure: every environment holds a database branch, a network and a container per service for as long as it exists, so the cost is close to linear in it. A cap in dollars would need a price list per runtime, per region and per service size, none of which a control plane has.",
  },
];

export function PricingPage() {
  return (
    <PageShell>
      <PageHero
        path="/pricing"
        eyebrow="Pricing"
        title="Operational value, not AI personalities."
        lead="Community is the local engine. It is free, it is MIT licensed, and it works today with no account. Team is a platform fee plus run usage. Growth and Enterprise add volume, policy, and governance. Those bands are illustrative, not a quote."
        actions={
          <>
            <Button href="/docs/getting-started/quickstart">Start the quickstart</Button>
            <Button href="/signup" theme="outlined">
              Request hosted access
            </Button>
          </>
        }
      />
      <PageSection className="pt-0">
        <p className="mb-14 max-w-[720px] border-l border-black/15 pl-6 text-[16px] leading-7 tracking-extra-tight text-gray-new-40 max-md:mb-10 max-md:pl-4">
          Community needs nothing from us. The engine is MIT licensed, it installs with one
          command, and the quickstart runs on your own compute without an account. The hosted
          control plane is deployed and invitation only while it is in development, so the access
          button leads to a waitlist unless you have been invited. Team and Enterprise are open
          for design partners, and those two buttons book a call rather than take an address.
        </p>
        <ul className="grid grid-cols-3 items-stretch gap-x-12 max-xl:grid-cols-1 max-xl:gap-y-12">
          {PLANS.map((plan) => (
            <li key={plan.name} className="min-w-0">
              <PlanCard plan={plan} />
            </li>
          ))}
        </ul>
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading
          kicker="Free plan"
          title="<strong>Three numbers decide what free gives you.</strong> These are the ones a control plane enforces, not a summary of them."
        />
        <Prose className="mt-8">
          <p>
            The engine itself has no quota. It is MIT licensed, it runs on your machine and in
            your own continuous integration, and nothing in it counts environments or hours. The
            three below are what a control plane enforces for an organization with no live
            subscription, and they apply the same way whether that control plane is the hosted
            one or one you run yourself.
          </p>
          <p className="mt-6">
            Reaching any of them refuses the next environment and says so, naming the number and
            what you are holding. Nothing that is already running is ever torn down.
          </p>
        </Prose>
        <FreePlanFacts />
      </PageSection>
      <PageSection tone="ruled">
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
                <Chevron className="h-2.5 w-2.5 shrink-0 text-black/55 transition-transform duration-200 group-open:rotate-180" />
              </summary>
              <p className="pb-5 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                {body}
              </p>
            </details>
          ))}
        </div>
      </PageSection>
      <PageSection tone="plain">
        <PageHeading
          kicker="Questions"
          title="<strong>What free covers, in the words people ask it in.</strong> Including the one about MCP."
        />
        <Faq path="/pricing" items={PRICING_FAQ} />
      </PageSection>
    </PageShell>
  );
}
