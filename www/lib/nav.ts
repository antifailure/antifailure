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
      href: "/signup",
      cta: "Design-partner offer",
    },
  },
  { text: "Docs", href: "/docs" },
  { text: "Pricing", href: "/pricing" },
];

export const GITHUB_URL = "https://github.com/antifailure/antifailure";

export const FOOTER_MENUS = [
  {
    heading: "Product",
    items: [
      { text: "Overview", href: "/product" },
      { text: "Isolated Twin", href: "/product/twins" },
      { text: "Safe State", href: "/product/safe-state" },
      { text: "Workload Studio", href: "/product/workload" },
      { text: "Pricing", href: "/pricing" },
      { text: "Architecture", href: "/product/architecture" },
    ],
  },
  {
    heading: "Features",
    items: [
      { text: "Side-Effect Firewall", href: "/product/firewall" },
      { text: "Migration Safety", href: "/product/migrations" },
      { text: "Safety Report", href: "/product/report" },
      { text: "Change Intelligence", href: "/product/change-intelligence" },
      { text: "Differential Oracle", href: "/product/oracle" },
      { text: "Fidelity Graph", href: "/product/fidelity" },
      { text: "Exploratory users", href: "/product/exploratory-users" },
      { text: "Insights", href: "/docs/concepts/insights" },
    ],
  },
  {
    heading: "Company",
    items: [
      { text: "Solutions", href: "/solutions" },
      { text: "B2B SaaS", href: "/solutions/saas" },
      { text: "Fintech", href: "/solutions/fintech" },
      { text: "E-commerce", href: "/solutions/ecommerce" },
      { text: "Marketplaces", href: "/solutions/marketplaces" },
      { text: "Developer tools", href: "/solutions/devtools" },
      { text: "Sign up", href: "/signup" },
    ],
  },
  {
    heading: "Resources",
    items: [
      { text: "Documentation", href: "/docs" },
      { text: "Quickstart", href: "/docs/getting-started/quickstart" },
      { text: "Manifest", href: "/docs/reference/manifest" },
      { text: "Error reference", href: "/docs/reference/errors" },
      { text: "Enterprise", href: "/docs/enterprise/licensing" },
      { text: "Platform engineering", href: "/solutions/platform" },
    ],
  },
  {
    heading: "Connect",
    items: [
      { text: "GitHub", href: GITHUB_URL },
      { text: "Discord", href: "/signup" },
      { text: "Log in", href: "/signin" },
      { text: "Sign up", href: "/signup" },
    ],
  },
];
