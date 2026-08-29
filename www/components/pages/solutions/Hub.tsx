import { PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { AFTER_HEADING, DirectoryList, Metrics, SectionHeading } from "./visuals";

const ICP = [
  { value: "20–300", label: "engineers. Fast-growing SaaS and internet companies, not a first sale into regulated enterprise." },
  { value: "Postgres", label: "backed applications with enough production data that toy fixtures are misleading." },
  { value: "Daily / weekly", label: "production deploys, a real CI/CD process, and a platform engineer who owns reliability." },
] as const;

const TEAMS = [
  {
    href: "/solutions/saas",
    title: "B2B SaaS",
    body: "Daily deploys, expanding schemas, and staging that drifted years ago. Tenant-shaped state without production identities.",
    metric: "Seats · billing · coexistence",
  },
  {
    href: "/solutions/fintech",
    title: "Fintech",
    body: "Billing, ledgers, and side effects that must never hit live processors. Simulators, not production APIs.",
    metric: "Stripe simulated · fail closed",
  },
  {
    href: "/solutions/marketplaces",
    title: "Marketplaces",
    body: "Queues, workers, dual-writes, and matching logic staging never reproduces. Timing is the bug.",
    metric: "Workers · webhooks blocked",
  },
  {
    href: "/solutions/devtools",
    title: "Developer tools",
    body: "Schema changes on large tables. Users notice p99 immediately. Locks, plans, and pool pressure show up here first.",
    metric: "Locks · plans · pools",
  },
];

export function SolutionsHubPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions"
        title="The same question, for the teams who feel it first."
        lead="Before a risky change meets production, prove it on a disposable twin. Built for teams who already feel migration anxiety, staging drift, and release incidents."
      />

      <PageSection>
        <SectionLabel>Ideal customer</SectionLabel>
        <div className={AFTER_HEADING}>
          <Metrics items={[...ICP]} />
        </div>
        <p className={`${AFTER_HEADING} text-[14px] tracking-extra-tight text-black/45`}>
          Cloud-native or containerized · customer-hosted agent · history of migration anxiety · production-shaped traffic
        </p>
      </PageSection>

      <PageSection tone="white">
        <SectionHeading title="<strong>Teams.</strong> Postgres, frequent deploys, and a platform engineer who owns reliability." />
        <div className={AFTER_HEADING}>
          <DirectoryList items={TEAMS} />
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/signup", title: "Sign up", description: "Join the waitlist." },
          { href: "/product/migrations", title: "Migration Safety", description: "The failure mode these teams feel first." },
          { href: "/product", title: "Product", description: "How a twin run actually decides." },
        ]}
      />
    </PageShell>
  );
}
