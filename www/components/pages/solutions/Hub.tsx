import { PageHero, PageSection, PageShell } from "@/components/pages/kit";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { AFTER_HEADING, DirectoryList, Metrics, SectionHeading } from "./visuals";

const ICP = [
  { value: "Your stack", label: "Services, workers, and integrations tested together." },
  { value: "Postgres", label: "Realistic row counts, relationships, and masked customer data." },
  { value: "Your workflow", label: "Results in your pull request, before the deployment." },
] as const;

const TEAMS = [
  {
    href: "/solutions/saas",
    title: "B2B SaaS",
    body: "Test migrations, subscriptions, and account changes with masked tenant data.",
    metric: "Seats · billing · rare rows",
  },
  {
    href: "/solutions/fintech",
    title: "Fintech",
    body: "Rehearse payment flows and inspect ledger results with a local Stripe simulator.",
    metric: "Stripe offline · fail closed",
  },
  {
    href: "/solutions/marketplaces",
    title: "Marketplaces",
    body: "Test matching, notifications, and settlement with services and workers running together.",
    metric: "Workers · webhooks captured",
  },
  {
    href: "/solutions/devtools",
    title: "Developer tools",
    body: "Inspect locks, rewrites, and query plans before a schema change reaches users.",
    metric: "Locks · rewrites · plans",
  },
];

export function SolutionsHubPage() {
  return (
    <PageShell>
      <PageHero
        path="/solutions"
        eyebrow="Solutions"
        title="Test the changes your business depends on."
        lead="From subscription upgrades to background workers, Antifailure gives each change an isolated environment and a result your team can inspect."
      />

      <PageSection>
        <SectionLabel>Who this fits</SectionLabel>
        <div className={AFTER_HEADING}>
          <Metrics items={[...ICP]} />
        </div>
        <p className={`${AFTER_HEADING} text-[14px] tracking-extra-tight text-black/60`}>
          Run locally or in CI, with data and test environments in your infrastructure.
        </p>
      </PageSection>

      <PageSection tone="ruled">
        <SectionHeading title="<strong>Start with the workflows that matter to your team.</strong>" />
        <div className={AFTER_HEADING}>
          <DirectoryList items={TEAMS} />
        </div>
      </PageSection>

    </PageShell>
  );
}
