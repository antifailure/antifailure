/**
 * Which controls are worth rendering, and nothing else.
 *
 * The server decides access. Every route re-reads the role from the database
 * on every request and refuses what the role does not hold, so this table is
 * not a security boundary and removing it would change what a person can do by
 * exactly nothing.
 *
 * What it is for: a button that always answers "your role cannot do this" is
 * worse than no button, and five ad hoc `new Set(["owner", "admin"])` scattered
 * through five pages is how the console ends up disagreeing with itself about
 * who may approve an egress rule.
 *
 * It is a copy of ROLE_PERMISSIONS in web/apps/api/src/permissions.ts. The two
 * cannot share a module: the console is a separate build with its own
 * lockfile, and a workspace for one table is a workspace to maintain forever.
 * If the server's table changes, this changes with it, and the worst that a
 * stale copy does is show a control that is then refused.
 */
export const ROLE_PERMISSIONS: Record<string, readonly string[]> = {
  owner: [
    "environments.view", "environments.create", "environments.teardown",
    "masking.edit", "masking.approve", "network.edit", "network.approve",
    "agents.run", "load.run", "members.manage", "billing.manage",
    "audit.read", "audit.export", "runtimes.manage", "tokens.manage",
    "organization.settings", "organization.delete", "sessions.manage",
    "data.export", "account.close",
    "analytics.read",
  ],
  admin: [
    "environments.view", "environments.create", "environments.teardown",
    "masking.edit", "masking.approve", "network.edit", "network.approve",
    "agents.run", "load.run", "members.manage",
    "audit.read", "audit.export", "runtimes.manage", "tokens.manage",
    "organization.settings", "sessions.manage", "data.export", "account.close",
    "analytics.read",
  ],
  member: [
    "environments.view", "environments.create", "environments.teardown",
    "masking.edit", "network.edit", "agents.run", "load.run",
    "audit.read", "account.close",
  ],
  viewer: ["environments.view", "audit.read", "account.close"],
};

export function may(role: string | null | undefined, permission: string): boolean {
  return (ROLE_PERMISSIONS[role ?? ""] ?? []).includes(permission);
}
