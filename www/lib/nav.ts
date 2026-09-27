export type NavItem = { id: string; title: string; href: string; description: string };
export type NavSection = { id: string; title: string; items: NavItem[] };
export type FeaturedCard = {
  id: string;
  title: string;
  description: string;
  href: string;
  cta?: string;
  visual?: string;
};
export type HeaderMenu = {
  id: string;
  text: string;
  href?: string;
  sections?: NavSection[];
  featured?: FeaturedCard[];
};

export const HEADER_MENUS: HeaderMenu[] = [
  {
    id: "product",
    text: "Product",
    href: "/product",
  },
  {
    id: "solutions",
    text: "Solutions",
    sections: [
      {
        id: "teams",
        title: "Teams",
        items: [
          {
            id: "saas",
            title: "B2B SaaS",
            href: "/solutions/saas",
            description: "Subscriptions, tenants, schema changes",
          },
          {
            id: "fintech",
            title: "Fintech",
            href: "/solutions/fintech",
            description: "Payments and ledger checks",
          },
          {
            id: "marketplaces",
            title: "Marketplaces",
            href: "/solutions/marketplaces",
            description: "Queues, workers, dual-writes",
          },
          {
            id: "devtools",
            title: "Developer tools",
            href: "/solutions/devtools",
            description: "Schema changes on large tables",
          },
        ],
      },
    ],
    featured: [
      {
        id: "demo",
        title: "Bring your next change",
        description: "See Antifailure on a change relevant to your team.",
        href: "/request-demo",
        visual: "twin",
      },
    ],
  },
  { id: "docs", text: "Docs", href: "/docs" },
  { id: "writing", text: "Writing", href: "/blog" },
  { id: "pricing", text: "Pricing", href: "/pricing" },
];

export const GITHUB_URL = "https://github.com/antifailure/antifailure";

/**
 * The status page. Served by GitHub Pages from the status-data branch, and
 * deliberately not from antifailure.dev: the whole point of the page is to be
 * readable while this site's own host is the thing that is down, so the link
 * goes straight to the address that does not share a region with it. The
 * github.io address rather than a subdomain for the same reason, since the
 * zone for antifailure.dev is Azure DNS. antifailure.dev/status is a 301 to
 * this, for anybody who types it.
 */
export const STATUS_URL = "https://antifailure.github.io/antifailure/";

/**
 * The legal row along the bottom of the footer.
 *
 * Separate from FOOTER_MENUS because these are not a category of the product,
 * and because a security review looks for them here before it looks anywhere
 * else. A document that exists and is not linked is a document nobody finds.
 */
export const LEGAL_LINKS = [
  { id: "privacy", text: "Privacy", href: "/privacy" },
  { id: "terms", text: "Terms", href: "/terms" },
];

export const FOOTER_MENUS = [
  {
    id: "product",
    heading: "Product",
    items: [
      { id: "overview", text: "Overview", href: "/product" },
      { id: "quickstart", text: "Quickstart", href: "/docs/getting-started/quickstart" },
      { id: "pricing", text: "Pricing", href: "/pricing" },
    ],
  },
  {
    id: "features",
    heading: "Features",
    items: [
      { id: "load", text: "Load testing", href: "/docs/concepts/load" },
      { id: "migration", text: "SQL workloads", href: "/docs/concepts/sql-workloads" },
      { id: "insights", text: "Insights", href: "/docs/concepts/insights" },
      { id: "agents", text: "Agents", href: "/docs/concepts/agents" },
      { id: "egress", text: "Egress", href: "/docs/concepts/egress" },
      { id: "masking", text: "Masking", href: "/docs/concepts/masking" },
    ],
  },
  {
    id: "company",
    heading: "Company",
    items: [
      { id: "solutions", text: "Solutions", href: "/solutions" },
      { id: "saas", text: "B2B SaaS", href: "/solutions/saas" },
      { id: "fintech", text: "Fintech", href: "/solutions/fintech" },
      { id: "marketplaces", text: "Marketplaces", href: "/solutions/marketplaces" },
      { id: "devtools", text: "Developer tools", href: "/solutions/devtools" },
      { id: "about", text: "About", href: "/about" },
      { id: "careers", text: "Careers", href: "/careers" },
      { id: "contact", text: "Contact", href: "/contact" },
      { id: "demo", text: "Request a demo", href: "/request-demo" },
    ],
  },
  {
    id: "resources",
    heading: "Resources",
    items: [
      { id: "docs", text: "Documentation", href: "/docs" },
      { id: "quickstart", text: "Quickstart", href: "/docs/getting-started/quickstart" },
      { id: "writing", text: "Writing", href: "/blog" },
      { id: "changelog", text: "Changelog", href: "/changelog" },
      { id: "manifest", text: "Manifest", href: "/docs/reference/manifest" },
      { id: "errors", text: "Error reference", href: "/docs/reference/errors" },
      { id: "enterprise", text: "Enterprise", href: "/docs/enterprise/licensing" },
    ],
  },
  {
    id: "connect",
    heading: "Connect",
    items: [
      { id: "github", text: "GitHub", href: GITHUB_URL },
      { id: "status", text: "Status", href: STATUS_URL },
      { id: "signin", text: "Sign in", href: "/signin" },
      { id: "demo", text: "Request a demo", href: "/request-demo" },
    ],
  },
];
