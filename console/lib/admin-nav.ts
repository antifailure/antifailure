/**
 * The operator portal's information architecture, declared once.
 *
 * ONE FILE, AND WHY THAT MATTERS MORE THAN IT LOOKS. Six groups, twenty two
 * sections and an overview above them are built by six people at once. The way
 * that goes wrong is not a merge conflict, which git tells you about: it is two
 * rails that disagree, an entry whose label reads one way in the sidebar and
 * another on the page it opens, and a permission the navigation checks that the
 * server never heard of. So the label, the route, the icon, the permission and
 * the one sentence describing the section are declared together, here, and
 * every screen in the portal reads them from this list rather than repeating
 * them. There is no second copy to drift.
 *
 * THE LABELS ARE THE SPECIFICATION. They are the product owner's words and
 * their order is the product owner's order. The ampersand is deliberate and it
 * belongs to the rendered label only: routes, file names and prose spell the
 * word out, because a URL with an ampersand in it is a query string waiting to
 * happen.
 *
 * THE ROUTE SHAPE IS /admin/<group>/<section>, and it is what keeps six people
 * out of each other's way. A group owns one directory under console/app/admin
 * and one module under web/apps/api/src/admin, so two sections in different
 * groups cannot land in the same file. See console/app/admin/README.md.
 *
 * WHY THERE IS NO "is this page finished" FLAG HERE. Because the page itself is
 * the answer: a section with nothing behind it yet renders `Planned`, and a
 * section that has been built does not. A flag in this file would be a second
 * claim about the same fact, maintained by hand, and the copy that goes stale
 * is always the one that says a page is ready when it is not.
 *
 * ON THE PERMISSIONS. Every string here is a permission that really exists in
 * web/apps/api/src/admin/permissions.ts, and the navigation hides an entry
 * whose permission the operator does not hold. It is a CONVENIENCE and never
 * the enforcement: the server refuses on its own, and an entry left visible by
 * a mistake here still opens a page that answers "your role cannot see this".
 *
 * A section that is not built yet names the permission of the nearest
 * capability that does exist, because there is no honest way to name a
 * permission that has not been designed. When the section lands, the agent
 * building it changes this one line to the permission its routes actually
 * declare. That is the only edit anybody but the shell makes to this file.
 */

import {
  IconAudit,
  IconBilling,
  IconBranches,
  IconDatabase,
  IconEmail,
  IconExperiments,
  IconGovernance,
  IconIncidents,
  IconInfrastructure,
  IconIntegrations,
  IconKeys,
  IconLogs,
  IconMcp,
  IconOperators,
  IconOverview,
  IconPlan,
  IconRepositories,
  IconRuns,
  IconSecurity,
  IconSettings,
  IconSupport,
  IconTenants,
  IconTwins,
} from "@/components/icons";

/** The one icon shape the rail draws. Every glyph in icons.tsx has it. */
export type NavIcon = (props: { className?: string }) => React.JSX.Element;

export interface AdminNavItem {
  /** As rendered, ampersand and all. Never used to build a route. */
  label: string;
  href: string;
  Icon: NavIcon;
  /** Hides the entry when the operator does not hold it. See the header. */
  permission: string;
  /**
   * What lives here, in one sentence.
   *
   * Read by three screens: the overview's directory, the `Planned` state on a
   * section nobody has built yet, and the rail's title attribute. Written for
   * the operator rather than for us, so it says what the section answers and
   * not which table it reads.
   */
  summary: string;
}

export interface AdminNavGroup {
  label: string;
  /** The directory and the router module this group owns, which is the name
   *  used in console/app/admin/<slug> and web/apps/api/src/admin/<slug>.ts. */
  slug: "customers" | "product" | "platform" | "operations" | "security" | "administration";
  items: AdminNavItem[];
}

/**
 * The portal's front door, above the groups rather than inside one.
 *
 * Separate because it is not a section of anything: it is where an operator
 * lands, and a group heading over a single entry reads as a group somebody
 * forgot to fill in.
 */
export const ADMIN_OVERVIEW: AdminNavItem = {
  label: "Overview",
  href: "/admin",
  Icon: IconOverview,
  permission: "admin.portal.access",
  summary: "Everything this portal can reach, grouped, and filtered to what your role holds.",
};

export const ADMIN_NAV: AdminNavGroup[] = [
  {
    label: "Customers",
    slug: "customers",
    items: [
      {
        label: "Users & Organizations",
        href: "/admin/customers/users",
        Icon: IconTenants,
        permission: "admin.tenants.read",
        summary:
          "Every organization on the installation, its plan, its people, and whether it is suspended.",
      },
      {
        label: "Support & Impersonation",
        href: "/admin/customers/support",
        Icon: IconSupport,
        permission: "admin.users.read",
        summary:
          "Answer a customer's question from their side of the product, and every impersonation on the record.",
      },
      {
        label: "Billing & Stripe",
        href: "/admin/customers/billing",
        Icon: IconBilling,
        permission: "admin.billing.read",
        summary:
          "Subscriptions, invoices, charges and credit, read from Stripe rather than from a copy of it.",
      },
    ],
  },
  {
    label: "Product",
    slug: "product",
    items: [
      {
        label: "Production Twins",
        href: "/admin/product/twins",
        Icon: IconTwins,
        permission: "admin.infra.read",
        summary: "Every twin running on this installation, who owns it, and what it costs to keep.",
      },
      {
        label: "Runs & Jobs",
        href: "/admin/product/runs",
        Icon: IconRuns,
        permission: "admin.infra.read",
        summary: "Work in flight and work that failed, across every organization at once.",
      },
      {
        label: "Safe State & Databases",
        href: "/admin/product/safe-state",
        Icon: IconDatabase,
        permission: "admin.infra.read",
        summary:
          "The database snapshots a twin can be returned to, and whether restoring one has been proven.",
      },
      {
        label: "Branches & Environments",
        href: "/admin/product/branches",
        Icon: IconBranches,
        permission: "admin.infra.read",
        summary: "Environments per branch, how long they have stood, and what is holding them open.",
      },
      {
        label: "Experiments & Feature Flags",
        href: "/admin/product/experiments",
        Icon: IconExperiments,
        permission: "admin.flags.read",
        summary: "What each flag is turned on for, who it is targeted at, and how to kill it fast.",
      },
    ],
  },
  {
    label: "Developer Platform",
    slug: "platform",
    items: [
      {
        label: "Repositories & Pull Requests",
        href: "/admin/platform/repositories",
        Icon: IconRepositories,
        permission: "admin.tenants.read",
        summary:
          "The repositories this installation is connected to and the pull requests it is answering on.",
      },
      {
        label: "MCP Management",
        href: "/admin/platform/mcp",
        Icon: IconMcp,
        permission: "admin.tenants.read",
        summary: "The MCP servers customers have connected, and what each one is allowed to reach.",
      },
      {
        label: "API Keys",
        href: "/admin/platform/api-keys",
        Icon: IconKeys,
        permission: "admin.tenants.read",
        summary: "Keys issued across the platform, when each was last used, and how to revoke one.",
      },
      {
        label: "Integrations & Webhooks",
        href: "/admin/platform/integrations",
        Icon: IconIntegrations,
        permission: "admin.tenants.read",
        summary: "Outbound deliveries, what failed, and the endpoints that keep refusing them.",
      },
    ],
  },
  {
    label: "Operations",
    slug: "operations",
    items: [
      {
        label: "Infrastructure & Compute",
        href: "/admin/operations/infrastructure",
        Icon: IconInfrastructure,
        permission: "admin.infra.read",
        summary: "What this installation is running on, and how much of it is left.",
      },
      {
        label: "Logs & Error Explorer",
        href: "/admin/operations/logs",
        Icon: IconLogs,
        permission: "admin.infra.read",
        summary: "Search the platform's own logs, and the errors it has grouped for you.",
      },
      {
        label: "Email & Notifications",
        href: "/admin/operations/email",
        Icon: IconEmail,
        permission: "admin.infra.read",
        summary: "What the product sent, what bounced, and what is still waiting to go out.",
      },
      {
        label: "Incidents & Kill Switches",
        href: "/admin/operations/incidents",
        Icon: IconIncidents,
        permission: "admin.emergency.read",
        summary:
          "Whether maintenance mode, new sign-ups and new runs are paused, and the switch for each.",
      },
    ],
  },
  {
    label: "Security & Governance",
    slug: "security",
    items: [
      {
        label: "Security Center",
        href: "/admin/security/center",
        Icon: IconSecurity,
        permission: "admin.audit.read",
        summary: "Refused requests, suspicious sign-ins, and what the platform is currently blocking.",
      },
      {
        label: "Audit Logs",
        href: "/admin/security/audit",
        Icon: IconAudit,
        permission: "admin.audit.read",
        summary: "Every operator action on this installation, in a chain that can be verified.",
      },
      {
        label: "Data Governance",
        href: "/admin/security/data-governance",
        Icon: IconGovernance,
        permission: "admin.tenants.read",
        summary: "What customer data is kept, for how long, and the deletions that were carried out.",
      },
    ],
  },
  {
    label: "Administration",
    slug: "administration",
    items: [
      {
        label: "Analytics & Usage",
        href: "/admin/administration/analytics",
        Icon: IconPlan,
        permission: "admin.tenants.read",
        summary: "What the platform is being used for, measured rather than estimated.",
      },
      {
        label: "Admins & Permissions",
        href: "/admin/administration/admins",
        Icon: IconOperators,
        permission: "admin.operators.read",
        summary: "Who can reach this portal, what their role grants, and the catalog behind it.",
      },
      {
        label: "System Configuration",
        href: "/admin/administration/configuration",
        Icon: IconSettings,
        permission: "admin.infra.read",
        summary: "How this installation is configured, and which settings were set rather than defaulted.",
      },
    ],
  },
];

/** Every entry, overview first, for the code that does not care about groups. */
export const ADMIN_NAV_ITEMS: AdminNavItem[] = [
  ADMIN_OVERVIEW,
  ...ADMIN_NAV.flatMap((g) => g.items),
];

/**
 * The entry a route belongs to, for the page that has to name itself.
 *
 * Exact match rather than a prefix. A section with a detail route beneath it,
 * such as one organization under Users & Organizations, is a different page
 * with a different title, and a prefix match here would hand it the list's.
 */
export function navItemFor(href: string): AdminNavItem | null {
  return ADMIN_NAV_ITEMS.find((item) => item.href === href) ?? null;
}
