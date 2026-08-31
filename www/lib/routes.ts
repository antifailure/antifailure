import { POSTS_BY_DATE } from "./blog";

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

export type RouteSection = "root" | "product" | "solutions" | "company" | "writing" | "legal" | "utility";

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
      "Sanitized, referentially consistent, production-shaped Postgres. Masking is compiled to SQL, read back by a scanner, and signed before it can be branched.",
    summary:
      "How production data is masked deterministically, read back column by column, and signed before use.",
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
    path: "/product/load",
    title: "Load — Antifailure",
    description:
      "Traffic shaped like production's own access log, sent at the twin, and compared against the p95 production serves each route in.",
    summary:
      "Where the traffic shape comes from, which routes are sent, and what a regression is measured against.",
    section: "product",
    indexable: true,
    priority: 0.8,
    parent: "/product",
  },
  {
    path: "/product/migrations",
    title: "Migration Safety — Antifailure",
    description:
      "Pending migrations rehearsed on a branch with production's shape: per-statement timing, the strongest lock held per table, table rewrites, and query plans before and after.",
    summary:
      "What migration rehearsal measures: locks, per-statement timing, table rewrites, plan diffs.",
    section: "product",
    indexable: true,
    priority: 0.9,
    parent: "/product",
  },
  {
    path: "/product/report",
    title: "Safety Report — Antifailure",
    description:
      "Pass or fail on the pull request, with the video, trace and reproduction steps behind the decision.",
    summary: "What lands on the pull request, the five verdicts a workflow can return, and the evidence behind each.",
    section: "product",
    indexable: true,
    priority: 0.8,
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

  // Writing
  {
    path: "/blog",
    title: "Writing — Antifailure",
    description:
      "Notes on shipping schema changes without taking production down: what staging cannot measure, and what a test environment should do with an outbound call.",
    summary: "Index of the writing.",
    section: "writing",
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
  {
    path: "/dpa",
    title: "Data Processing Agreement — Antifailure",
    description:
      "A draft DPA written from the code: the roles, the security measures that exist, and the ones that do not yet.",
    summary: "The data processing agreement, and which of its measures are real today.",
    section: "legal",
    indexable: true,
    priority: 0.3,
    parent: "/privacy",
  },
  {
    path: "/subprocessors",
    title: "Subprocessors — Antifailure",
    description:
      "Everyone who receives data, everyone who deliberately does not, and how the list changes.",
    summary: "The subprocessor list and the notice period for changing it.",
    section: "legal",
    indexable: true,
    priority: 0.3,
    parent: "/privacy",
  },
  {
    path: "/data-retention",
    title: "Retention and deletion — Antifailure",
    description:
      "How long each thing is kept, how it goes away, and where the period is not exact.",
    summary: "Retention periods per data class, and where the boundary is approximate.",
    section: "legal",
    indexable: true,
    priority: 0.3,
    parent: "/privacy",
  },
  {
    path: "/sla",
    title: "Service levels — Antifailure",
    description:
      "There is no service level agreement. What is not committed, what holds anyway, and what would have to change.",
    summary: "What is and is not promised about availability, stated as a limit.",
    section: "legal",
    indexable: true,
    priority: 0.3,
    parent: "/terms",
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

/**
 * Blog posts, derived from the post registry rather than typed out again.
 *
 * Appending them here is what makes the sitemap, llms.txt, llms-full.txt, the
 * breadcrumb trail and the SEO check all cover the blog without any of them
 * being taught what a blog is. A post added to lib/blog.ts appears in every
 * one of those on the next build.
 */
const POST_ROUTES: Route[] = POSTS_BY_DATE.map((post) => ({
  path: `/blog/${post.slug}`,
  title: `${post.title} — Antifailure`,
  description: post.dek,
  summary: post.summary,
  section: "writing" as const,
  indexable: true,
  priority: 0.6,
  parent: "/blog",
}));

const ALL_ROUTES: readonly Route[] = [...ROUTES, ...POST_ROUTES];

/** Routes that belong in the sitemap and may be indexed. */
export const INDEXABLE_ROUTES = ALL_ROUTES.filter((r) => r.indexable);

const BY_PATH = new Map(ALL_ROUTES.map((r) => [r.path, r]));

export function getRoute(path: string): Route | undefined {
  return BY_PATH.get(path);
}

/**
 * The breadcrumb trail for a path, root first, including the page itself.
 * Returns a single entry for the home page, which renders no visible trail.
 */
export function breadcrumbTrail(path: string): Route[] {
  const trail: Route[] = [
];
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
