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
    "workloads.view", "workloads.edit", "workloads.run",
    "analytics.read",
  ],
  admin: [
    "environments.view", "environments.create", "environments.teardown",
    "masking.edit", "masking.approve", "network.edit", "network.approve",
    "agents.run", "load.run", "members.manage",
    "audit.read", "audit.export", "runtimes.manage", "tokens.manage",
    "organization.settings", "sessions.manage", "data.export", "account.close",
    "workloads.view", "workloads.edit", "workloads.run",
    "analytics.read",
  ],
  member: [
    "environments.view", "environments.create", "environments.teardown",
    "masking.edit", "network.edit", "agents.run", "load.run",
    "audit.read", "account.close",
    "workloads.view", "workloads.edit", "workloads.run",
  ],
  viewer: ["environments.view", "audit.read", "account.close", "workloads.view"],
};

export function may(role: string | null | undefined, permission: string): boolean {
  return (ROLE_PERMISSIONS[role ?? ""] ?? []).includes(permission);
}

/**
 * What a screen should render when a permission decides the whole screen.
 *
 * WHY THIS IS NOT `may()` WITH AN `if`. `may()` answers one question, "does
 * this role hold this permission", and a role that has not arrived yet is not
 * a role that holds nothing: it is not known. Three pages asked `may()` before
 * the session resolved and rendered a REFUSAL, so an owner loading /plan was
 * told "your role cannot see this" about a permission owners are the only
 * holders of. The screenshot of that is what this function exists to prevent.
 *
 * A refusal is also the worst possible thing to render while loading, because
 * it is the one message a person acts on rather than waits through. Somebody
 * reads it and goes looking for who can change their role.
 *
 * The four answers are deliberately four. "unavailable" is separate from
 * "refused" because a session that failed to load says nothing about the role,
 * and a page that shows a permission error when the control plane is
 * unreachable sends the reader after the wrong problem.
 */
export type PermissionVerdict = "loading" | "unavailable" | "allowed" | "refused";

export function permissionVerdict(
  status: "loading" | "ready" | "error",
  role: string | null | undefined,
  permission: string,
): PermissionVerdict {
  if (status === "loading") return "loading";
  if (status === "error") return "unavailable";
  // Ready, and still no role: signed out, or signed in with no organization.
  // Neither is a refusal about this permission, and both are states the shell
  // already renders around this page.
  if (!role) return "unavailable";
  return may(role, permission) ? "allowed" : "refused";
}
