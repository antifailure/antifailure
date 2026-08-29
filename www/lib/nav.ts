export type NavItem = { title: string; href: string; description: string };
export type NavSection = { title: string; items: NavItem[] };
export type FeaturedCard = {
  title: string;
  description: string;
  href: string;
  cta: string;
};
export type HeaderMenu = {
  text: string;
  href?: string;
  sections?: NavSection[];
  featured?: FeaturedCard;
};

export const HEADER_MENUS: HeaderMenu[] = [
  {
    text: "Product",
    sections: [
      {
        title: "Core",
        items: [
          {
            title: "Overview",
            href: "/product",
            description: "Twin, state, containment, judgment",
          },
          {
            title: "Isolated Twin",
            href: "/product/twins",
            description: "Temporary copy of the application stack",
          },
          {
            title: "Safe State",
            href: "/product/safe-state",
            description: "Sanitized production-shaped Postgres",
          },
          {
            title: "Side-Effect Firewall",
            href: "/product/firewall",
            description: "Simulators instead of real-world side effects",
          },
          {
            title: "Workload Studio",
            href: "/product/workload",
            description: "Observed, deterministic, and exploratory traffic",
          },
          {
            title: "Migration Safety",
            href: "/product/migrations",
            description: "Locks, plans, rollback feasibility",
          },
        ],
      },
      {
        title: "Judgment",
        items: [
          {
            title: "Safety Report",
            href: "/product/report",
            description: "Pass, warning, or block on the PR",
          },
          {
            title: "Change Intelligence",
            href: "/product/change-intelligence",
            description: "What to validate for this change",
          },
          {
            title: "Differential Oracle",
            href: "/product/oracle",
            description: "Baseline vs candidate comparison",
          },
          {
            title: "Fidelity Graph",
            href: "/product/fidelity",
            description: "What the twin actually reproduced",
          },
          {
            title: "Architecture",
            href: "/product/architecture",
            description: "Control plane and customer data plane",
          },
        ],
      },
    ],
    featured: {
      title: "The migration wedge",
      description:
        "The first complete stack is Postgres schema safety — exclusive locks, plan regressions, and rollback that is no longer safe.",
      href: "/product/migrations",
      cta: "See migration safety",
    },
  },
  {
    text: "Solutions",
    sections: [
      {
        title: "Teams",
        items: [
          {
            title: "B2B SaaS",
            href: "/solutions/saas",
            description: "Daily deploys, migration anxiety",
          },
          {
            title: "Fintech",
            href: "/solutions/fintech",
            description: "Billing and ledger-safe twins",
          },
          {
            title: "E-commerce",
            href: "/solutions/ecommerce",
            description: "Checkout under production-shaped load",
          },
          {
            title: "Marketplaces",
            href: "/solutions/marketplaces",
            description: "Queues, workers, dual-writes",
          },
          {
            title: "Developer tools",
            href: "/solutions/devtools",
            description: "Schema changes on large tables",
          },
        ],
      },
      {
        title: "Jobs",
        items: [
          {
            title: "Platform engineering",
            href: "/solutions/platform",
            description: "Ephemeral twins instead of shared staging",
          },
          {
            title: "Schema migrations",
            href: "/solutions/migrations",
            description: "The failure mode staging never catches",
          },
          {
            title: "Release gates",
            href: "/solutions/release-gates",
            description: "Evidence-backed pass, warning, or block",
          },
          {
            title: "Workflow products",
            href: "/solutions/workflow",
            description: "Workers, schedules, and long-tail state",
          },
        ],
      },
    ],
    featured: {
      title: "Give us one nervous deploy",
      description:
        "The design-partner offer is a real upcoming migration, not a generic demo. We show what staging missed.",
      href: "/design-partners",
      cta: "Design-partner offer",
    },
  },
  {
    text: "Resources",
    sections: [
      {
        title: "Learn",
        items: [
          { title: "Docs", href: "/docs", description: "How a twin run works" },
          {
            title: "Security",
            href: "/security",
            description: "Fail closed. Data stays in your boundary",
          },
          {
            title: "Open source",
            href: "/open-source",
            description: "Customer agent, adapters, cleanup",
          },
          {
            title: "Exploratory users",
            href: "/product/exploratory-users",
            description: "Exploratory users inside Workload Studio",
          },
        ],
      },
      {
        title: "Company",
        items: [
          { title: "About", href: "/company", description: "Why this company exists" },
          { title: "Pricing", href: "/pricing", description: "Community, team, and enterprise" },
          {
            title: "Design partners",
            href: "/design-partners",
            description: "One real risky migration",
          },
          { title: "Privacy", href: "/privacy", description: "What we collect and what we never take" },
        ],
      },
    ],
    featured: {
      title: "Fail closed by default",
      description:
        "Unknown egress, unresolved secrets, or incomplete cleanup blocks the run. Convenience does not override containment.",
      href: "/security",
      cta: "Read the trust model",
    },
  },
  { text: "Docs", href: "/docs" },
  { text: "Pricing", href: "/pricing" },
];

export const FOOTER_MENUS = [
  {
    heading: "Company",
    items: [
      { text: "About", href: "/company" },
      { text: "Pricing", href: "/pricing" },
      { text: "Security", href: "/security" },
      { text: "Open source", href: "/open-source" },
      { text: "Design partners", href: "/design-partners" },
      { text: "Sign up", href: "/signup" },
    ],
  },
  {
    heading: "Product",
    items: [
      { text: "Product overview", href: "/product" },
      { text: "Isolated Twin", href: "/product/twins" },
      { text: "Safe State", href: "/product/safe-state" },
      { text: "Firewall", href: "/product/firewall" },
      { text: "Workload Studio", href: "/product/workload" },
      { text: "Migrations", href: "/product/migrations" },
      { text: "Safety report", href: "/product/report" },
    ],
  },
  {
    heading: "Resources",
    items: [
      { text: "Docs", href: "/docs" },
      { text: "Quickstart", href: "/docs/getting-started/quickstart" },
      { text: "Architecture", href: "/product/architecture" },
      { text: "Solutions", href: "/solutions" },
      { text: "Privacy Notice", href: "/privacy" },
      { text: "Terms of Use", href: "/terms" },
    ],
  },
];

export const GITHUB_URL = "https://github.com/antifailure/antifailure";
