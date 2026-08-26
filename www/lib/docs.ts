export const DOC_SLUGS = [
  "quickstart",
  "pull-requests",
  "concepts",
  "migration-safety",
  "firewall",
  "workload",
  "report",
  "architecture",
  "open-source",
] as const;

export type DocSlug = (typeof DOC_SLUGS)[number];

export type DocNavItem = { href: string; title: string };

export const DOC_NAV: { title: string; items: DocNavItem[] }[] = [
  {
    title: "Get started",
    items: [
      { href: "/docs", title: "Introduction" },
      { href: "/docs/quickstart", title: "Quickstart" },
      { href: "/docs/pull-requests", title: "Pull requests" },
    ],
  },
  {
    title: "Concepts",
    items: [
      { href: "/docs/concepts", title: "How it works" },
      { href: "/docs/migration-safety", title: "Migration safety" },
      { href: "/docs/firewall", title: "Side-effect firewall" },
      { href: "/docs/workload", title: "Workload Studio" },
      { href: "/docs/report", title: "Safety report" },
    ],
  },
  {
    title: "Reference",
    items: [
      { href: "/docs/architecture", title: "Architecture" },
      { href: "/docs/open-source", title: "Open source" },
    ],
  },
];

export const DOC_META: Record<string, { title: string; description: string }> = {
  "": {
    title: "Introduction",
    description: "A disposable production twin that proves whether a deployment is safe before it ships.",
  },
  quickstart: {
    title: "Quickstart",
    description: "Connect a repository and create an isolated twin. The init command is not a published package yet.",
  },
  "pull-requests": {
    title: "Pull requests",
    description: "How Antifailure analyzes a risky change, runs a twin, and attaches pass, warning, or block.",
  },
  concepts: {
    title: "How it works",
    description: "Twin, safe state, containment, behavior, comparison, judgment, evidence, and cleanup.",
  },
  "migration-safety": {
    title: "Migration safety",
    description: "Locks, query plans, rollback feasibility, and the 27.4s subscriptions demo.",
  },
  firewall: {
    title: "Side-effect firewall",
    description: "Fail-closed egress, clone-local DNS, and simulators instead of real-world side effects.",
  },
  workload: {
    title: "Workload Studio",
    description: "Observed patterns, deterministic scenarios, and Crowdi exploratory users.",
  },
  report: {
    title: "Safety report",
    description: "Pass, warning, or block with fidelity, evidence, and cleanup proof.",
  },
  architecture: {
    title: "Architecture",
    description: "Customer-hosted data plane, environment lifecycle, isolation, and Postgres strategy.",
  },
  "open-source": {
    title: "Open source",
    description: "The planned open-source surface inside the customer trust boundary.",
  },
};

export function isDocSlug(value: string): value is DocSlug {
  return (DOC_SLUGS as readonly string[]).includes(value);
}
