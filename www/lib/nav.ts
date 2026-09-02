export type NavItem = { title: string; href: string; description: string };
export type NavSection = { title: string; items: NavItem[] };
export type FeaturedCard = {
  title: string;
  description: string;
  href: string;
  cta?: string;
  visual?: string;
};
export type HeaderMenu = {
  text: string;
  href?: string;
  sections?: NavSection[];
  featured?: FeaturedCard[];
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
            title: "Load",
            href: "/product/load",
            description: "Traffic shaped like production's access log",
          },
          {
            title: "Migration Safety",
            href: "/product/migrations",
            description: "Locks, rewrites, and query plans",
          },
        ],
      },
    ],
    featured: [
      {
        title: "Migration rehearsal",
        description: "Exclusive locks, table rewrites, and query plans on production's own shape.",
        href: "/product/migrations",
        visual: "fleet",
      },
      {
        title: "Isolated Twin",
        description: "A temporary copy of the application stack, destroyed with the pull request.",
        href: "/product/twins",
        visual: "twin",
      },
    ],
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
    ],
    featured: [
      {
        title: "Give us one nervous deploy",
        description: "A real upcoming migration, not a generic demo.",
        href: "/signup",
        visual: "twin",
      },
      {
        title: "Migration rehearsal",
        description: "Exclusive locks, table rewrites, and query plans on production's own shape.",
        href: "/product/migrations",
        visual: "fleet",
      },
    ],
  },
  { text: "Docs", href: "/docs" },
  { text: "Writing", href: "/blog" },
  { text: "Pricing", href: "/pricing" },
];

export const GITHUB_URL = "https://github.com/antifailure/antifailure";

/**
 * The legal row along the bottom of the footer.
 *
 * Separate from FOOTER_MENUS because these are not a category of the product,
 * and because a security review looks for them here before it looks anywhere
 * else. A document that exists and is not linked is a document nobody finds.
 */
export const LEGAL_LINKS = [
  { text: "Privacy", href: "/privacy" },
  { text: "Terms", href: "/terms" },
  { text: "DPA", href: "/dpa" },
  { text: "Subprocessors", href: "/subprocessors" },
  { text: "Retention", href: "/data-retention" },
  { text: "Service levels", href: "/sla" },
];

export const FOOTER_MENUS = [
  {
    heading: "Product",
    items: [
      { text: "Overview", href: "/product" },
      { text: "Isolated Twin", href: "/product/twins" },
      { text: "Safe State", href: "/product/safe-state" },
      { text: "Load", href: "/product/load" },
      { text: "Pricing", href: "/pricing" },
    ],
  },
  {
    heading: "Features",
    items: [
      { text: "Side-Effect Firewall", href: "/product/firewall" },
      { text: "Migration Safety", href: "/product/migrations" },
      { text: "Insights", href: "/docs/concepts/insights" },
      { text: "Agents", href: "/docs/concepts/agents" },
      { text: "Egress", href: "/docs/concepts/egress" },
      { text: "Masking", href: "/docs/concepts/masking" },
    ],
  },
  {
    heading: "Company",
    items: [
      { text: "Solutions", href: "/solutions" },
      { text: "B2B SaaS", href: "/solutions/saas" },
      { text: "Fintech", href: "/solutions/fintech" },
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
      { text: "Writing", href: "/blog" },
      { text: "Changelog", href: "/changelog" },
      { text: "Manifest", href: "/docs/reference/manifest" },
      { text: "Error reference", href: "/docs/reference/errors" },
      { text: "Enterprise", href: "/docs/enterprise/licensing" },
    ],
  },
  {
    heading: "Connect",
    items: [
      { text: "GitHub", href: GITHUB_URL },
      { text: "Log in", href: "/signin" },
      { text: "Join the waitlist", href: "/signup" },
    ],
  },
];
