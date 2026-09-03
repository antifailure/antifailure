// The platform permission catalog.
//
// WHY THIS IS A SECOND FILE AND NOT AN ADDITION TO permissions.ts. That file is
// the TENANT catalog, and its test enforces two rules that together make it a
// trap for anything platform level:
//
//   every permission in the catalog guards at least one route, and
//   every permission that guards a route is granted to some tenant role.
//
// Both are good rules for a tenant catalog. Together they mean that adding
// `admin.users.read` there would force granting it to owner, admin, member or
// viewer, which is exactly backwards: a customer's own organization owner would
// hold a permission that reads every other customer's data. So platform
// permissions live here, with their own roles and their own procedure builder,
// and the two catalogs never mix.
//
// The same deny-by-default reasoning applies as in the tenant catalog, and the
// second rule there is worth repeating because it is the one that actually
// fails in practice: the way access control breaks is not a wrong grant, it is
// a new endpoint nobody remembered to guard. adminProcedure takes the
// permission as an argument for exactly that reason, so a route cannot exist
// without one.

export const ADMIN_PERMISSIONS = [
  // The portal itself. Holding this is what makes /admin load at all, and
  // every role has it, because a role that can sign in and then sees nothing
  // is indistinguishable from a broken deployment.
  'admin.portal.access',

  // The operator directory: who can reach this portal, and with what role.
  'admin.operators.read',
  'admin.operators.write',

  // The platform's own audit chain.
  'admin.audit.read',
  'admin.audit.export',

  // Tenants and the people in them. Read is broad on purpose: an operator
  // answering a support question needs to see the account, and splitting that
  // finer produces roles nobody can hold usefully.
  'admin.tenants.read',
  'admin.tenants.suspend',
  'admin.tenants.plan',

  'admin.users.read',
  'admin.users.write',
  'admin.sessions.read',
  'admin.sessions.revoke',

  // What an operator wrote down about a customer, and stepping into their
  // account.
  //
  // Read is separate from write on notes because a note is the vendor's own
  // words about a paying customer: everybody answering a question needs to see
  // what the last person found, and far fewer people need to add to the file.
  //
  // Impersonation is split the same way and the split is not symmetric with the
  // others. Reading is oversight, so it is held widely: an operator should be
  // able to see that somebody was inside a customer's account whether or not
  // they could go in themselves. Starting one is the single most powerful thing
  // in this portal and is held by three roles.
  'admin.support.read',
  'admin.support.write',
  'admin.impersonation.read',
  'admin.impersonation.start',

  // Money, and the three things that decide what a customer gets.
  //
  // Read is separate from write on all three, and on billing that split is the
  // one that matters: an on-call engineer answering "why was this customer
  // charged twice" needs the invoices and the charges, and does not need the
  // refund button. Merging them would make everybody who can answer a question
  // able to move money, which is how a support rota becomes a financial risk.
  'admin.billing.read',
  'admin.billing.write',
  'admin.entitlements.read',
  'admin.entitlements.write',
  'admin.flags.read',
  'admin.flags.write',

  // Infrastructure. One read covers system health, the fleet of twins, the
  // teardown ledger and the egress firewall, because they are one question
  // asked from four angles and an operator who can see one and not the next
  // cannot answer it.
  'admin.infra.read',
  // Asking for twins to be torn down. Separate from read because it writes,
  // and separate from the emergency switches because it is a routine action
  // during an incident rather than one that stops the installation.
  'admin.infra.teardown',

  // The emergency switches. Read is separate from engage on purpose: every
  // operator should be able to SEE that the installation is paused, and almost
  // none of them should be able to pause it.
  'admin.emergency.read',
  'admin.emergency.engage',

  // The developer platform: what customers connect to this installation, and
  // the credentials that can act as one of them.
  //
  // Read on repositories is one permission rather than two because a pull
  // request cannot be read usefully without the repository it is on, and a
  // split there produces a role that can see a number and not what it is a
  // number of.
  'admin.repos.read',

  // Credentials. Read and revoke are separate, and the split is the same one
  // billing makes: everybody who answers "which key is this" should be able to
  // answer it, and revoking a customer's credential stops their pipeline
  // within seconds, which is an incident decision rather than a support one.
  'admin.keys.read',
  'admin.keys.revoke',

  // What arrives here from GitHub and from Stripe, and whether it was handled.
  'admin.webhooks.read',

  // The MCP page, which reads no customer data at all. It is guarded because
  // every route in this tree is guarded, not because what it returns is
  // sensitive: it describes the engine's own tool surface and states that this
  // control plane holds no MCP record.
  'admin.mcp.read',
] as const

export type AdminPermission = (typeof ADMIN_PERMISSIONS)[number]

/**
 * Permissions the other portal lanes own.
 *
 * Declared here rather than in their own files so that the catalog is one list
 * and the matrix test can see all of it, and so that two lanes cannot pick the
 * same string. Each prefix belongs to exactly one agent, and adding a
 * permission under somebody else's prefix is the kind of thing a review misses
 * and this comment does not.
 */
export const RESERVED_PREFIXES: Record<string, string> = {
  'admin.portal': 'the foundation',
  'admin.operators': 'the foundation',
  'admin.audit': 'the foundation',
  'admin.tenants': 'the foundation',
  // The Customers group: Users & Organizations, Support & Impersonation, and
  // Billing & Stripe. These four prefixes read 'ops' and 'money' when the
  // portal was four lanes; the navigation now declares six groups and one
  // module each, and admin/customers.ts owns all of them. Two places named a
  // different owner for the same prefix, which is worse than either answer.
  'admin.users': 'customers',
  'admin.sessions': 'customers',
  'admin.impersonation': 'customers',
  'admin.support': 'customers',
  'admin.billing': 'customers',
  'admin.projects': 'ops',
  'admin.search': 'ops',
  'admin.entitlements': 'money',
  'admin.flags': 'money',
  'admin.infra': 'infra',
  'admin.deploys': 'the developer platform',
  // Moved from infra to the developer platform, which is where the API Keys and
  // Integrations & Webhooks sections live and where these two are now
  // implemented. w-admin-infra implemented admin.infra.* and admin.emergency.*
  // and neither of these, so nothing was taken from a lane that was using it.
  'admin.webhooks': 'the developer platform',
  'admin.keys': 'the developer platform',
  // Repositories and pull requests, and the MCP surface. New prefixes rather
  // than borrowed ones: admin.deploys does not describe a pull request, and a
  // permission filed under a name that does not fit it is one nobody can find.
  'admin.repos': 'the developer platform',
  'admin.mcp': 'the developer platform',
  'admin.logs': 'infra',
  'admin.security': 'infra',
  'admin.emergency': 'infra',
}

export const ADMIN_ROLES = [
  'owner',
  'super_admin',
  'infrastructure',
  'security',
  'billing',
  'support',
  'analytics',
  'read_only',
] as const

export type AdminRole = (typeof ADMIN_ROLES)[number]

/**
 * What each permission means, in the words an auditor reads.
 *
 * The documentation is generated from this, so a permission cannot be added
 * without describing it. Same rule as the tenant catalog, same reason: three
 * lists would disagree and the documentation is the one that would be wrong.
 */
export const ADMIN_PERMISSION_DESCRIPTIONS: Record<AdminPermission, string> = {
  'admin.portal.access': 'Sign in to the operator portal and see its navigation.',
  'admin.operators.read': 'See who holds an operator account and what role they have.',
  'admin.operators.write': 'Create operators, change their role, and suspend them.',
  'admin.audit.read': 'Read the platform audit chain.',
  'admin.audit.export': 'Export the platform audit chain and verify its hashes.',
  'admin.infra.read':
    'See system health, every environment running on this installation, the teardown ledger, ' +
    'and the egress rules across every organization.',
  'admin.infra.teardown':
    'Ask for environments to be torn down. This records a request; the runtime confirms it.',
  'admin.emergency.read':
    'See whether maintenance mode, new sign-ups, or new runs are paused, and why.',
  'admin.emergency.engage':
    'Pause or resume the whole installation: maintenance mode, new sign-ups, and new runs.',
  'admin.repos.read':
    'See every repository connected to this installation, its pull requests, and the check ' +
    'generations behind them.',
  'admin.keys.read':
    'See every credential that can act as a customer: its name, its prefix, what created it and ' +
    'when it was last used. Never its value, which is stored only as a hash.',
  'admin.keys.revoke':
    'Stop a credential working, and revoke a GitHub OIDC repository binding along with every ' +
    'token it has minted.',
  'admin.webhooks.read':
    'See the GitHub App installations and the deliveries that arrived from GitHub and Stripe, ' +
    'including the ones that were never handled.',
  'admin.mcp.read':
    'See what this control plane records about the MCP server, and the tools the engine serves.',
  'admin.billing.read':
    'See a customer\'s Stripe customer, subscription, invoices, charges, payment methods and ' +
    'credit balance, and the record of every administrative money action taken on the account.',
  'admin.billing.write':
    'Move money: issue a refund, add credit, change or cancel a plan, extend a trial, apply a ' +
    'discount, retry a payment, and resend an invoice.',
  'admin.entitlements.read':
    'See what an organization is entitled to, and which of its limits were granted rather than ' +
    'set by its plan.',
  'admin.entitlements.write':
    'Grant an organization, project or user a limit other than its plan\'s, and revoke one.',
  'admin.flags.read': 'See feature flags, their rollout and who they are targeted at.',
  'admin.flags.write':
    'Turn a feature flag on or off for everybody, target it, roll it out, and kill it during an ' +
    'incident.',
  'admin.tenants.read': 'See every organization, its plan, its usage and its members.',
  'admin.tenants.suspend':
    'Stop an organization creating new work, and let it start again. Running environments are untouched.',
  'admin.tenants.plan': "Change an organization's plan, which is what its quotas are derived from.",
  'admin.users.read': 'See any account on the platform and which organizations it belongs to.',
  'admin.users.write': 'Suspend and restore an account.',
  'admin.sessions.read': 'See who is signed in, on what, and since when.',
  'admin.sessions.revoke': 'Sign any account out of any session.',
  'admin.support.read': "Read the notes operators have written about a customer, including ones that were retracted.",
  'admin.support.write': 'Write a note about a customer, and retract one.',
  'admin.impersonation.read':
    'See which operators are signed in as a customer right now, and every impersonation that ' +
    'has started or ended.',
  'admin.impersonation.start':
    'Sign in as a customer, for a stated reason and for a bounded number of minutes. The ' +
    'customer sees it in their own audit log.',
}

/**
 * The built-in operator roles.
 *
 * Written out per role rather than as inheritance, for the reason the tenant
 * catalog gives: inheritance reads well and hides exactly the thing a reviewer
 * is checking, which is whether a low-privilege role can do something it should
 * not. This table can be read straight down a column.
 *
 * Three decisions worth defending:
 *
 * Only owner and super_admin can write operators. Granting an operator account
 * is granting cross-tenant read of the entire customer base, so it is the one
 * permission that is genuinely about who runs the company rather than who is
 * on call.
 *
 * read_only holds admin.audit.read. Oversight is not a privilege to be
 * rationed, and a role that can see what everyone did without being able to do
 * anything is the role an auditor should be given.
 *
 * owner and security hold admin.audit.export; nobody else does. Reading is
 * oversight and every role has it; exporting produces a file of every operator
 * action that leaves the system, so it is held by the two roles whose job is
 * answering for what happened.
 *
 * security is not an exception grudgingly made, it is the point: a security
 * team that can read an incident's audit trail and cannot produce it for an
 * investigation or for counsel is not much use, and they are the role most
 * likely to need it at the worst moment.
 *
 * An earlier version of this comment said "nobody except owner", which
 * contradicted the table three lines below it AND misdescribed the tenant
 * catalog it claimed to mirror: there, audit.export is held by owner and
 * admin, and what the split actually withholds is a VIEWER exporting. Found by
 * admin-money reading the comment against the grant.
 */
export const ADMIN_ROLE_PERMISSIONS: Record<AdminRole, readonly AdminPermission[]> = {
  owner: [...ADMIN_PERMISSIONS],
  super_admin: [
    'admin.portal.access',
    'admin.operators.read', 'admin.operators.write',
    'admin.audit.read',
    'admin.tenants.read', 'admin.tenants.suspend', 'admin.tenants.plan',
    'admin.users.read', 'admin.users.write',
    'admin.sessions.read', 'admin.sessions.revoke',
    'admin.infra.read', 'admin.infra.teardown',
    // The only role besides owner that may stop the installation. Gated on the
    // permission rather than on rank: ordering roles and comparing ranks is how
    // a permission model stops being a table and starts being an assumption.
    'admin.emergency.read', 'admin.emergency.engage',
    'admin.repos.read', 'admin.webhooks.read', 'admin.mcp.read',
    'admin.keys.read', 'admin.keys.revoke',
    'admin.support.read', 'admin.support.write',
    'admin.impersonation.read', 'admin.impersonation.start',
  ],
  infrastructure: [
    'admin.portal.access', 'admin.audit.read',
    'admin.tenants.read', 'admin.tenants.suspend',
    'admin.users.read', 'admin.sessions.read',
    // Flags, both halves. A kill switch is an incident tool before it is a
    // product one, and the people holding the pager have to be able to reach
    // it without finding somebody from billing at three in the morning.
    'admin.flags.read', 'admin.flags.write',
    'admin.infra.read', 'admin.infra.teardown',
    // Sees the switches, cannot throw them. An infrastructure operator
    // debugging "their runs will not start" must be able to discover that runs
    // are frozen; pausing the installation is a different decision.
    'admin.emergency.read',
    // The repositories, the deliveries and the credentials, because "their
    // checks are not running" is an infrastructure question and the answer is
    // usually a delivery that never arrived or a credential that stopped
    // working. Revoke as well as read: a leaked engine token is an incident and
    // the pager holder is who reaches it first.
    'admin.repos.read', 'admin.webhooks.read', 'admin.mcp.read',
    'admin.keys.read', 'admin.keys.revoke',
    // What support already found, and who is inside an account right now.
    // During an incident both are context rather than power, and neither is a
    // write. Starting an impersonation is deliberately not here: an engineer
    // reproducing a fault has the logs, the twin and the run, and none of
    // those require becoming the customer.
    'admin.support.read', 'admin.impersonation.read',
  ],
  security: [
    'admin.portal.access', 'admin.audit.read', 'admin.audit.export',
    'admin.operators.read',
    'admin.tenants.read', 'admin.tenants.suspend',
    'admin.users.read', 'admin.users.write',
    'admin.sessions.read', 'admin.sessions.revoke',
    // Read on all three, and write on flags. Security investigates money and
    // entitlements rather than changing them, and containing an incident by
    // killing a feature is the one write it needs at speed.
    'admin.billing.read', 'admin.entitlements.read',
    'admin.flags.read', 'admin.flags.write',
    'admin.infra.read', 'admin.emergency.read',
    // Credentials are security's surface more than anybody's: a key that
    // appeared in a public repository is the report they receive, and revoking
    // it is the first thing they do about it.
    'admin.repos.read', 'admin.webhooks.read', 'admin.mcp.read',
    'admin.keys.read', 'admin.keys.revoke',
    // Notes both ways, because writing down what an investigation found is
    // most of what a security review produces. Impersonation read and not
    // start, for the same reason this role holds sessions.revoke and not the
    // means to create a session: the job is establishing what happened.
    'admin.support.read', 'admin.support.write',
    'admin.impersonation.read',
  ],
  billing: [
    'admin.portal.access', 'admin.audit.read',
    'admin.tenants.read', 'admin.tenants.plan',
    'admin.users.read',
    // The role the money permissions exist for, and the only one below owner
    // that holds the write half of all three.
    'admin.billing.read', 'admin.billing.write',
    'admin.entitlements.read', 'admin.entitlements.write',
    'admin.flags.read', 'admin.flags.write',
    // Read, because the last person to touch this account left a note, and a
    // refund argument that ignores it is an argument being had twice.
    'admin.support.read',
  ],
  support: [
    'admin.portal.access', 'admin.audit.read',
    'admin.tenants.read',
    'admin.users.read', 'admin.sessions.read',
    // Support answers "why was I charged this" every day and must never be the
    // rota that can refund. Read without write is the whole point of the split.
    'admin.billing.read', 'admin.entitlements.read', 'admin.flags.read',
    // Read across the whole developer platform and revoke on none of it. This
    // is the rota that answers "why did our check not run", which needs the
    // repository, the delivery and whether the key is still live, and it is
    // emphatically not the rota that should be able to stop a customer's
    // pipeline while answering.
    'admin.repos.read', 'admin.keys.read', 'admin.webhooks.read', 'admin.mcp.read',
    // The role these four permissions exist for. Support is the rota that
    // answers "it does not work for me", and it cannot answer that from the
    // outside. This is the one role below owner and super_admin that can step
    // into an account, and it is bounded rather than trusted: minutes, a
    // stated reason, an entry in the customer's own audit log, and the
    // operator portal closed for as long as it lasts.
    'admin.support.read', 'admin.support.write',
    'admin.impersonation.read', 'admin.impersonation.start',
  ],
  analytics: ['admin.portal.access', 'admin.audit.read', 'admin.tenants.read', 'admin.users.read'],
  read_only: [
    'admin.portal.access', 'admin.audit.read', 'admin.tenants.read', 'admin.users.read',
    // The auditor's read of the developer platform. Every one of these is a
    // read, and the credential list holds no secret: only the prefix, which is
    // what the customer's own console shows them. A role that can see which
    // credentials exist and cannot touch one is exactly the role an auditor
    // should be given.
    'admin.repos.read', 'admin.keys.read', 'admin.webhooks.read', 'admin.mcp.read',
  ],
}

export function adminRoleHas(role: AdminRole, permission: AdminPermission): boolean {
  return ADMIN_ROLE_PERMISSIONS[role].includes(permission)
}

/** Every role that holds a permission, for the documentation table. */
export function adminRolesWith(permission: AdminPermission): AdminRole[] {
  return ADMIN_ROLES.filter((r) => adminRoleHas(r, permission))
}

/**
 * Ordered most to least privileged, for DISPLAY only.
 *
 * Never used to decide access, for the reason the tenant catalog states:
 * ordering roles and comparing ranks is how a permission model stops being a
 * table and starts being an assumption, and the assumption breaks the first
 * time a role does not fit the line. `security` outranks `infrastructure` on
 * sessions and not on anything else, which is exactly the case a rank cannot
 * express.
 */
export const ADMIN_ROLE_ORDER: readonly AdminRole[] = [...ADMIN_ROLES]

/**
 * The root operator's role, which the database also enforces.
 *
 * A trigger in 0030 refuses to let the root row be demoted from owner, so this
 * constant and that trigger have to agree. They are checked against each other
 * by a test rather than by anybody remembering.
 */
export const ROOT_ADMIN_ROLE: AdminRole = 'owner'
