/**
 * Every page this site publishes, declared once.
 *
 * The sitemap, llms.txt, llms-full.txt, the breadcrumb trail, the footer's long
 * tail and the crawl-depth test all read this array. Before it existed the
 * sitemap did not exist either, and each page carried its own title and
 * description with no way to tell whether a route had been left out of
 * anything. Adding a route here is what makes it discoverable; a route that is
 * reachable in the app but missing from this list fails `npm run check:seo`.
 *
 * `title` is the full <title>. It does not get the site name appended, because
 * several of these already end in "— Antifailure" and a template would double
 * it. `summary` is a machine-facing one-liner: it is what an assistant reads in
 * llms.txt, so it says what the page answers rather than selling it.
 */

export type RouteSection = "root" | "product" | "solutions" | "company" | "legal" | "utility";

export type Route = {
  /** Site-relative path, no trailing slash. "/" for the home page. */
  path: string;
  title: string;
  description: string;
  /** One line for llms.txt. What a reader gets from this page. */
  summary: string;
  section: RouteSection;
  /** Left out of the sitemap and marked noindex when false. */
  indexable: boolean;
  /** Sitemap priority. Relative weight only; engines treat it as a hint. */
  priority: number;
  /** Parent path, for the breadcrumb trail. Undefined at the root. */
  parent?: string;
};

export const ROUTES: readonly Route[] = [
  {
    path: "/",
    title: "Antifailure — Know what happens before you deploy",
    description:
      "A disposable copy of your production stack for every pull request: masked Postgres, contained third-party APIs, and agents that use your app like people.",
    summary:
      "What Antifailure is, the four things a run produces, and the commands that produce them.",
    section: "root",
    indexable: true,
    priority: 1.0,
  },

  // Product
  {
    path: "/product",
    title: "Product — Antifailure",
    description:
      "The twin, the state inside it, the containment around it, and the judgment it returns on a pull request.",
    summary: "The four parts of a run: twin, state, containment, judgment. Start here.",
    section: "product",
    indexable: true,
    priority: 0.9,
    parent: "/",
  },
  {
    path: "/product/twins",
    title: "Isolated Twin — Antifailure",
    description:
      "A temporary copy of the application stack for every risky change, created per pull request and destroyed with it.",
    summary: "How a per-pull-request copy of the stack is created, isolated and torn down.",
    section: "product",
    indexable: true,
    priority: 0.8,
    parent: "/product",
  },
  {
    path: "/product/safe-state",
    title: "Safe State — Antifailure",
    description:
      "Sanitized, referentially consistent, production-shaped Postgres. Masking is compiled to SQL, verified by a scanner, and attested before it can be branched.",
    summary:
      "How production data is masked deterministically, verified column by column, and attested before use.",
    section: "product",
    indexable: true,
    priority: 0.8,
    parent: "/product",
  },
  {
    path: "/product/firewall",
    title: "Side-Effect Firewall — Antifailure",
    description:
      "Fail-closed egress with a per-host decision: block, allow, sandbox, capture, or answer from an offline mock.",
    summary:
      "The five per-host egress modes, and what happens to a request that matches none of them.",
    section: "product",
    indexable: true,
    priority: 0.8,
    parent: "/product",
  },
  {
    path: "/product/workload",
    title: "Workload Studio — Antifailure",
    description:
      "Observed traffic patterns, deterministic scenarios, and exploratory agents, run against the twin at production shape.",
    summary: "How load is shaped from real traffic rather than invented, and how it is replayed.",
    section: "product",
    indexable: true,
    priority: 0.8,
    parent: "/product",
  },
  {
    path: "/product/exploratory-users",
    title: "Exploratory users — Antifailure",
    description:
      "Agents that drive a real browser through the accessibility tree, log in the way a person does, and return a verdict with evidence.",
    summary:
      "How agent-driven exploration works, how it signs in, and the five verdicts it can return.",
    section: "product",
    indexable: true,
    priority: 0.8,
    parent: "/product",
  },
  {
    path: "/product/migrations",
    title: "Migration Safety — Antifailure",
    description:
      "Pending migrations rehearsed on a fresh branch: per-statement timing, the strongest lock held per table, plan regressions, and whether rollback is still safe.",
    summary:
      "What migration rehearsal measures: locks, per-statement timing, plan diffs, rollback feasibility.",
    section: "product",
    indexable: true,
    priority: 0.9,
    parent: "/product",
  },
  {
    path: "/product/report",
    title: "Safety Report — Antifailure",
    description:
      "Pass, warning, or block on the pull request, with the video, trace and reproduction steps behind the decision.",
    summary: "What lands on the pull request, and what evidence backs each verdict.",
    section: "product",
    indexable: true,
    priority: 0.8,
    parent: "/product",
  },
  {
    path: "/product/change-intelligence",
    title: "Change Intelligence — Antifailure",
    description:
      "What to validate for this pull request, and at what fidelity, decided from the change rather than from a fixed suite.",
    summary: "How the system decides which checks a given diff actually warrants.",
    section: "product",
    indexable: true,
    priority: 0.7,
    parent: "/product",
  },
  {
    path: "/product/oracle",
    title: "Differential Oracle — Antifailure",
    description:
      "Baseline against candidate on equivalent state and behavior, so a difference is attributable to the change and not to the environment.",
    summary: "How baseline and candidate are compared, and what counts as a real difference.",
    section: "product",
    indexable: true,
    priority: 0.7,
    parent: "/product",
  },
  {
    path: "/product/fidelity",
    title: "Fidelity Graph — Antifailure",
    description:
      "An explicit model of what the twin reproduced and what it did not, so a passing run states its own limits.",
    summary: "How the twin records what it did and did not reproduce, and why that is published.",
    section: "product",
    indexable: true,
    priority: 0.7,
    parent: "/product",
  },
  {
    path: "/product/architecture",
    title: "Architecture — Antifailure",
    description:
      "The trust boundary between control plane and customer data plane, the environment lifecycle, and the Postgres branching strategy.",
    summary:
      "Control plane versus data plane, where production data is allowed to exist, and the branch lifecycle.",
    section: "product",
    indexable: true,
    priority: 0.8,
    parent: "/product",
  },

  // Solutions
  {
    path: "/solutions",
    title: "Solutions — Antifailure",
    description:
      "Pre-production deployment safety for SaaS, fintech, commerce, marketplaces, and developer tools.",
    summary: "Index of the vertical and role pages.",
    section: "solutions",
    indexable: true,
    priority: 0.8,
    parent: "/",
  },
  {
    path: "/solutions/saas",
    title: "B2B SaaS — Antifailure",
    description:
      "Daily deploys against tenant-shaped data, and the migration anxiety that comes with a shared schema.",
    summary: "What a twin looks like for multi-tenant B2B SaaS.",
    section: "solutions",
    indexable: true,
    priority: 0.7,
    parent: "/solutions",
  },
  {
    path: "/solutions/fintech",
    title: "Fintech — Antifailure",
    description: "Billing and ledger-safe production twins, with no live money movement.",
    summary: "How ledger and billing flows are exercised without touching a real payment network.",
    section: "solutions",
    indexable: true,
    priority: 0.7,
    parent: "/solutions",
  },
  {
    path: "/solutions/ecommerce",
    title: "E-commerce — Antifailure",
    description: "Checkout exercised end to end under production-shaped load, with signed webhooks and no network.",
    summary: "Running a complete checkout, subscribe, renew and cancel offline.",
    section: "solutions",
    indexable: true,
    priority: 0.7,
    parent: "/solutions",
  },
  {
    path: "/solutions/marketplaces",
    title: "Marketplaces — Antifailure",
    description: "Queues, workers and dual-writes, exercised where the ordering actually varies.",
    summary: "Testing the orderings a marketplace hits: queues, workers, dual-writes.",
    section: "solutions",
    indexable: true,
    priority: 0.7,
    parent: "/solutions",
  },
  {
    path: "/solutions/devtools",
    title: "Developer tools — Antifailure",
    description: "Schema changes on large tables, rehearsed at production row counts rather than fixture counts.",
    summary: "Why a migration that is instant on a fixture is not instant on a real table.",
    section: "solutions",
    indexable: true,
    priority: 0.7,
    parent: "/solutions",
  },
  {
    path: "/solutions/platform",
    title: "Platform engineering — Antifailure",
    description: "Ephemeral twins per pull request instead of one shared staging everybody queues for.",
    summary: "Replacing shared staging with a per-branch environment.",
    section: "solutions",
    indexable: true,
    priority: 0.7,
    parent: "/solutions",
  },
  {
    path: "/solutions/migrations",
    title: "Schema migrations — Antifailure",
    description: "The failure mode staging never catches: a lock held long enough to matter at production scale.",
    summary: "Why staging misses migration failures, and what rehearsal catches instead.",
    section: "solutions",
    indexable: true,
    priority: 0.8,
    parent: "/solutions",
  },
  {
    path: "/solutions/release-gates",
    title: "Release gates — Antifailure",
    description: "Evidence-backed pass, warning, or block, so a gate can be argued with rather than overridden.",
    summary: "Using the safety report as a merge gate.",
    section: "solutions",
    indexable: true,
    priority: 0.7,
    parent: "/solutions",
  },
  {
    path: "/solutions/workflow",
    title: "Workflow products — Antifailure",
    description: "Workers, schedules, and the long-tail state that only appears after something has been running a while.",
    summary: "Exercising scheduled and long-running state.",
    section: "solutions",
    indexable: true,
    priority: 0.7,
    parent: "/solutions",
  },

  // Company
  {
    path: "/pricing",
    title: "Pricing — Antifailure",
    description: "Community, team, and enterprise pricing for pre-production deployment safety.",
    summary: "What each tier includes and what it costs.",
    section: "company",
    indexable: true,
    priority: 0.9,
    parent: "/",
  },
  {
    path: "/company",
    title: "About — Antifailure",
    description: "Antifailure is an open-core pre-production deployment safety platform. This is why it exists.",
    summary: "Who builds Antifailure and the problem it was built for. The entity home for the project.",
    section: "company",
    indexable: true,
    priority: 0.7,
    parent: "/",
  },
  {
    path: "/security",
    title: "Security — Antifailure",
    description: "Fail closed. Production data stays inside the customer boundary, and an unverified golden cannot be branched.",
    summary: "The trust model, the containment guarantees, and how to report a vulnerability.",
    section: "company",
    indexable: true,
    priority: 0.8,
    parent: "/",
  },
  {
    path: "/open-source",
    title: "Open source — Antifailure",
    description: "Customer agent, adapters, sanitization, egress, and cleanup: the open-source surface and what stays commercial.",
    summary: "Which components are open source, under which licence, and where the line sits.",
    section: "company",
    indexable: true,
    priority: 0.7,
    parent: "/",
  },
  {
    path: "/design-partners",
    title: "Design partners — Antifailure",
    description: "One real risky migration, a complete wind tunnel, a useful decision. The design-partner offer.",
    summary: "What the design-partner engagement involves and who it suits.",
    section: "company",
    indexable: true,
    priority: 0.7,
    parent: "/",
  },

  // Legal
  {
    path: "/privacy",
    title: "Privacy Notice — Antifailure",
    description: "What is collected, what is never taken, and why production data stays in the customer boundary.",
    summary: "The privacy notice.",
    section: "legal",
    indexable: true,
    priority: 0.3,
    parent: "/",
  },
  {
    path: "/terms",
    title: "Terms of Use — Antifailure",
    description: "A proving ground, not a guarantee. The promise is evidence, not zero failure.",
    summary: "The terms of use.",
    section: "legal",
    indexable: true,
    priority: 0.3,
    parent: "/",
  },

  // Utility. Real pages, deliberately not in the index: they are a waitlist
  // form with nothing to rank for, and indexing them spends crawl budget that
  // belongs to the product pages.
  {
    path: "/signin",
    title: "Join the waitlist — Antifailure",
    description: "There is no hosted control plane yet. Leave an address and we will tell you when there is.",
    summary: "Waitlist form.",
    section: "utility",
    indexable: false,
    priority: 0.1,
    parent: "/",
  },
  {
    path: "/signup",
    title: "Join the waitlist — Antifailure",
    description: "There is no hosted control plane yet. Leave an address and we will tell you when there is.",
    summary: "Waitlist form.",
    section: "utility",
    indexable: false,
    priority: 0.1,
    parent: "/",
  },
];

/** Routes that belong in the sitemap and may be indexed. */
export const INDEXABLE_ROUTES = ROUTES.filter((r) => r.indexable);

const BY_PATH = new Map(ROUTES.map((r) => [r.path, r]));

export function getRoute(path: string): Route | undefined {
  return BY_PATH.get(path);
}

/**
 * The breadcrumb trail for a path, root first, including the page itself.
 * Returns a single entry for the home page, which renders no visible trail.
 */
export function breadcrumbTrail(path: string): Route[] {
  const trail: Route[] = [];
  let current = BY_PATH.get(path);
  const seen = new Set<string>();
  while (current && !seen.has(current.path)) {
    seen.add(current.path);
    trail.unshift(current);
    current = current.parent ? BY_PATH.get(current.parent) : undefined;
  }
  return trail;
}

/** Routes in a section, for the related-pages module and llms.txt grouping. */
export function routesInSection(section: RouteSection): Route[] {
  return INDEXABLE_ROUTES.filter((r) => r.section === section);
}
