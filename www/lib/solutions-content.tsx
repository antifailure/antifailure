import type { MarketingContent } from "@/lib/marketing-content";
import { PageCallout } from "@/components/layout/MarketingPage";

const RELATED = [
  { href: "/product/migrations", title: "Migration Safety", description: "The failure mode these teams feel first." },
  { href: "/signup", title: "Sign up", description: "Join the waitlist." },
  { href: "/product", title: "Product", description: "How a twin run actually decides." },
];

export const SOLUTION_OVERVIEW: MarketingContent = {
  eyebrow: "Solutions",
  title: "The same question, for the teams who feel it first.",
  lead: "Fast-growing SaaS and internet companies that ship daily and cannot afford a migration that locks production. 20 to 300 engineers, Postgres-backed, a real CI/CD process, and a platform engineer who owns deployment reliability.",
  description: "Pre-production deployment safety for SaaS, fintech, commerce, marketplaces, and developer tools.",
  features: [
    { title: "B2B SaaS", body: "Daily deploys, expanding schemas, and staging that drifted years ago." },
    { title: "Fintech", body: "Billing, ledgers, and side effects that must never hit live processors." },
    { title: "E-commerce", body: "Checkout, inventory, and promotions under production-shaped load." },
    { title: "Marketplaces", body: "Queues, workers, dual-writes, and matching logic staging never reproduces." },
    { title: "Developer tools", body: "Schema changes on large tables, pools, and query-plan regressions." },
    { title: "Platform engineering", body: "Policy-controlled ephemeral validation instead of a shared staging ticket." },
  ],
  related: RELATED,
  body: (
    <>
      <h2>Initial ideal customer</h2>
      <ul>
        <li>20 to 300 engineers</li>
        <li>A Postgres-backed application</li>
        <li>Daily or weekly production deployments</li>
        <li>A cloud-native or containerized stack</li>
        <li>A real CI/CD process</li>
        <li>A history of migration anxiety, staging drift, or release incidents</li>
        <li>Enough production data and traffic that toy fixtures are misleading</li>
        <li>A platform or infrastructure engineer who owns deployment reliability</li>
        <li>A willingness to install a customer-hosted agent</li>
      </ul>
      <h2>Buyer and users</h2>
      <p>
        Economic buyer: VP Engineering, Head of Infrastructure, Head of Platform, or CTO at a smaller
        company. Technical champion: staff platform engineer, database reliability engineer, senior
        backend engineer, or DevOps lead. Daily users: backend, full-stack, platform engineers, and
        release owners. Secondary: security, compliance, QA, product engineering, and incident response.
      </p>
      <h2>Trigger events</h2>
      <ul>
        <li>A migration recently caused downtime</li>
        <li>A large table must be altered</li>
        <li>The team is changing an event schema</li>
        <li>A billing or checkout flow is being rewritten</li>
        <li>A monolith is being split into services</li>
        <li>A database or cloud migration is planned</li>
        <li>A release works in staging but repeatedly fails under production load</li>
        <li>Engineering leadership wants deployment evidence for a regulated workflow</li>
      </ul>
      <h2>Why not start with heavily regulated enterprises</h2>
      <p>
        Regulated enterprises have intense need, but initially require long procurement, private
        networking, customer-managed encryption, strict residency, evidence retention, external audits,
        incident procedures, and contractual support. The product should be architected for enterprise
        from the beginning, but sold first to technically sophisticated mid-market teams.
      </p>
    </>
  ),
};

export const SOLUTION_PAGES: Record<string, MarketingContent> = {
  saas: {
    eyebrow: "Solutions · B2B SaaS",
    title: "Daily deploys. Expanding schemas. Staging that drifted years ago.",
    lead: "The first twin should catch the migration that locks subscriptions during peak traffic. Workload Studio replays checkout and seat-change journeys against sanitized tenant data.",
    description: "Pre-production deployment safety for B2B SaaS teams with migration anxiety.",
    features: [
      { title: "Tenant-shaped state", body: "Referential subsets of accounts, seats, and billing without production identities." },
      { title: "Checkout and upgrades", body: "Critical workflows under production-shaped concurrency, not empty fixtures." },
      { title: "Schema coexistence", body: "Old application instances still running while the new column lands." },
    ],
    related: RELATED,
    body: (
      <>
        <h2>Jobs to be done</h2>
        <p>
          Before I merge or deploy a risky change, show me whether it will break under
          production-shaped conditions and explain exactly why. Give every developer an isolated
          environment without a platform-team ticket. Attach evidence to a pull request and enforce
          organizational release policy.
        </p>
        <h2>What the twin exercises</h2>
        <ul>
          <li>Seat changes, plan upgrades, and billing workers</li>
          <li>Long-tail tenant records and malformed historical state</li>
          <li>Background jobs and scheduled tasks</li>
          <li>Third-party integrations contained by the firewall</li>
        </ul>
        <PageCallout label="Primary job">
          Staging differs from production in too many dimensions at once. A change can pass unit,
          integration, end-to-end, and a manual staging check, then still fail in production.
        </PageCallout>
      </>
    ),
  },
  fintech: {
    eyebrow: "Solutions · Fintech",
    title: "Billing, ledgers, and side effects that must never hit live processors.",
    lead: "The firewall simulates Stripe. Safe State masks account identifiers deterministically. The oracle compares ledger writes between baseline and candidate.",
    description: "Ledger-safe production twins for fintech infrastructure and billing systems.",
    features: [
      { title: "Simulated payments", body: "Stripe creation is stored in a clone-local ledger. Nothing charges." },
      { title: "Ledger comparison", body: "The oracle compares writes, events, and third-party effects." },
      { title: "Fail closed", body: "Unknown processors and production API hostnames are blocked and flagged." },
    ],
    related: RELATED,
    body: (
      <>
        <h2>Why fintech feels this first</h2>
        <p>
          Duplicate queue or webhook events, irreversible writes, and a migration that holds
          ACCESS EXCLUSIVE on subscriptions are not academic. They are incidents. The twin exists so
          those failures show up in a report instead of in production.
        </p>
        <h2>Containment</h2>
        <ul>
          <li>Stripe simulated into a clone-local ledger</li>
          <li>Email rendered and captured, never delivered</li>
          <li>Production hostnames blocked</li>
          <li>Unknown TCP denied by default</li>
        </ul>
        <p>
          We do not claim zero-failure or that every regulated workflow is automatically compliant.
          We claim evidence under production-shaped conditions, with production data remaining in
          the customer boundary.
        </p>
      </>
    ),
  },
  ecommerce: {
    eyebrow: "Solutions · E-commerce",
    title: "Checkout under production-shaped load.",
    lead: "A twin with production-shaped carts and long-tail SKUs. Side effects to email and payments stay captured. Migration safety watches exclusive locks on orders.",
    description: "Pre-production deployment safety for checkout, inventory, and promotions.",
    features: [
      { title: "Long-tail catalogs", body: "Toy product fixtures miss the SKU that breaks a constraint." },
      { title: "Checkout p99", body: "Equivalent traffic against baseline and candidate, with lock timing." },
      { title: "Promotions and email", body: "Rendered and captured. Never delivered to customers." },
    ],
    related: RELATED,
    body: (
      <>
        <h2>What fails in production</h2>
        <p>
          A defaulted column on orders, a full table rewrite during a sale, connection-pool
          exhaustion, and query-plan regressions on the hottest checkout paths. Staging with a
          handful of SKUs will not show it.
        </p>
        <h2>Workers and side effects</h2>
        <p>
          Inventory workers, confirmation emails, and payment capture all run inside the twin.
          The firewall keeps them off the real world. The oracle compares journey outcomes:
          confirmation visible, one payment attempt, stock consistent.
        </p>
      </>
    ),
  },
  marketplaces: {
    eyebrow: "Solutions · Marketplaces",
    title: "Queues, workers, dual-writes, and matching logic staging never reproduces.",
    lead: "The twin includes workers and queues. The firewall blocks production webhooks. Exploratory users try multi-tab and retry behavior; the oracle compares event emissions.",
    description: "Deployment safety for marketplace systems with queues, workers, and dual-writes.",
    features: [
      { title: "Queues in the twin", body: "Simulated streams so dual-writes and retries are visible." },
      { title: "Webhook containment", body: "Production webhooks are blocked and ledgered." },
      { title: "Retry personas", body: "Impatient users and API clients with retry behavior become deterministic scenarios." },
    ],
    related: RELATED,
    body: (
      <>
        <h2>Why marketplaces break staging</h2>
        <p>
          Timing among services, queues, and workers; rolling deployment behavior; old and new
          schema coexistence; duplicate events. Those dimensions are exactly what a disposable twin
          is for.
        </p>
        <h2>Supporting jobs</h2>
        <ul>
          <li>Confirm that old and new application versions can coexist during rollout</li>
          <li>Prevent test environments from performing real-world side effects</li>
          <li>Reproduce representative production behavior without delaying production requests</li>
          <li>Convert discovered behavior into deterministic, repeatable tests</li>
        </ul>
      </>
    ),
  },
  devtools: {
    eyebrow: "Solutions · Developer tools",
    title: "Schema changes on large tables.",
    lead: "The flagship wedge. Measure lock duration, blocked statements, and whether old application instances can still read the new schema.",
    description: "Migration safety for developer tools with large Postgres tables.",
    features: [
      { title: "Large tables", body: "Exclusive locks and rewrites that never show up on a laptop database." },
      { title: "Query plans", body: "Plan regressions under production-shaped volume." },
      { title: "Pools", body: "Connection-pool exhaustion during migrate-and-serve." },
    ],
    related: RELATED,
    body: (
      <>
        <h2>The wedge, applied</h2>
        <p>
          Developer-tool companies often have the combination that makes migrations dangerous:
          large tables, frequent schema change, and users who notice p99 immediately. The first
          supported stack should be exceptional. A broad compatibility list with unreliable
          connectors would destroy trust.
        </p>
        <h2>MVP shape</h2>
        <p>
          GitHub-hosted Next.js or generic Docker, Postgres or Supabase, Stripe and email contained,
          Playwright exploration, deterministic scenarios, lock instrumentation, GitHub check,
          automatic TTL teardown. Not every cloud, not MongoDB, not a guarantee of every incident.
        </p>
      </>
    ),
  },
  platform: {
    eyebrow: "Solutions · Platform engineering",
    title: "Replace fragile shared staging with policy-controlled ephemeral validation.",
    lead: "Give every developer an isolated environment without a platform-team ticket. Destroy temporary infrastructure automatically and cap its cost.",
    description: "Ephemeral production twins for platform and infrastructure teams.",
    features: [
      { title: "No ticket", body: "Create Wind Tunnel from the pull request. The adapter is chosen for you." },
      { title: "Policy", body: "Isolation, cost, retention, and release gates are organization-wide." },
      { title: "Cleanup", body: "TTL, cost ceiling, independent reaper, verifiable destruction record." },
    ],
    related: RELATED,
    body: (
      <>
        <h2>Platform-engineering message</h2>
        <p>
          Replace fragile shared staging with policy-controlled, ephemeral deployment validation
          inside your cloud. The customer currently chooses between assembling several tools,
          maintaining an expensive staging environment, testing directly in production, or accepting
          the risk.
        </p>
        <h2>Setup</h2>
        <ol>
          <li>Install the GitHub application and select a repository.</li>
          <li>Install a customer-hosted runner or grant a narrowly scoped cloud role.</li>
          <li>Connect a Postgres source or approved backup.</li>
          <li>Review sensitive fields and discovered outbound services.</li>
          <li>Approve isolation, cost, and retention policies.</li>
          <li>Run a baseline against the current production version.</li>
        </ol>
        <p>
          The platform generates a repository configuration file. The standard path should not
          require hand-authoring YAML.
        </p>
      </>
    ),
  },
  migrations: {
    eyebrow: "Solutions · Schema migrations",
    title: "The failure mode staging never catches.",
    lead: "Long exclusive locks, full table rewrites, pool exhaustion, plan regressions, rare constraint failures, and rollback that is no longer safe.",
    description: "Automated safety validation for risky Postgres schema migrations.",
    features: [
      { title: "Clear failure mode", body: "A lock you can measure. A p99 you can compare. A rollback you can call unsafe." },
      { title: "Technical buyer", body: "Staff platform, DRE, senior backend, or DevOps lead." },
      { title: "Measurable value", body: "Incidents prevented, unsafe migrations detected, cleanup reliability." },
    ],
    related: RELATED,
    body: (
      <>
        <h2>Why this is the start</h2>
        <p>
          Database migrations create failures conventional tests miss. The initial wedge is
          automated safety validation for risky Postgres-backed web deployments, especially schema
          migrations — not universal multicloud cloning.
        </p>
        <h2>Land and expand</h2>
        <ol>
          <li>One application and one critical migration</li>
          <li>All schema migrations for that repository</li>
          <li>Every risky pull request</li>
          <li>Multiple applications and teams</li>
          <li>Organization-wide release policy</li>
          <li>Enterprise governance and evidence retention</li>
        </ol>
      </>
    ),
  },
  "release-gates": {
    eyebrow: "Solutions · Release gates",
    title: "Evidence-backed pass, warning, or block.",
    lead: "Attach a report to the pull request. Enforce organizational release policy. Do not ship on a green preview URL alone.",
    description: "Release policy for pass, warning, or block on every risky change.",
    features: [
      { title: "Pass / warning / block", body: "The only output that matters is the deployment decision." },
      { title: "Policy packs", body: "Locks, error rate, unknown egress, latency, fidelity thresholds." },
      { title: "Audit", body: "Videos, traces, queries, remediations, cleanup proof." },
    ],
    related: RELATED,
    body: (
      <>
        <h2>Example policies</h2>
        <ul>
          <li>Block if candidate error rate exceeds baseline by 1 percentage point</li>
          <li>Block if an exclusive lock exceeds 2 seconds on a critical table</li>
          <li>Block if unknown external egress is attempted</li>
          <li>Warn if p95 latency increases by more than 15%</li>
          <li>Require approval if fidelity is below 80%</li>
        </ul>
        <h2>Executive message</h2>
        <p>
          Reduce the probability and blast radius of high-risk releases by validating them under
          production-shaped conditions before rollout. That is not a zero-rollback guarantee.
        </p>
      </>
    ),
  },
  workflow: {
    eyebrow: "Solutions · Workflow products",
    title: "Workers, schedules, and long-tail state.",
    lead: "Timing among services, queues, and workers is a dimension staging drops. The twin includes background jobs and scheduled tasks.",
    description: "Deployment safety for workflow and collaboration products.",
    features: [
      { title: "Jobs in the twin", body: "Background workers and scheduled tasks run against sanitized state." },
      { title: "Long-tail records", body: "Malformed historical state that fixtures never include." },
      { title: "Multi-tab behavior", body: "Exploratory personas that abandon, resume, and retry." },
    ],
    related: RELATED,
    body: (
      <>
        <h2>Good verticals</h2>
        <p>
          Workflow and collaboration products, and data-heavy internal platforms, sit alongside B2B
          SaaS, fintech, e-commerce, marketplaces, and developer tools as initial ideal customers.
          They share Postgres, frequent deploys, and workers that staging does not run the same way.
        </p>
        <h2>What gets compared</h2>
        <p>
          Event and queue emissions, third-party effects, trace topology, and user-journey outcomes.
          Duplicate events and irreversible writes show up in the oracle, not after the deploy.
        </p>
      </>
    ),
  },
};

export const SOLUTION_SLUGS = Object.keys(SOLUTION_PAGES);
