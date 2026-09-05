export interface McpConsent {
  clientName: string;
  scopes: string[];
  organization: string;
  organizationName?: string;
  redirectUri: string;
  expiresInDays: number;
}

export const mcpScopeLabels: Record<string, string> = {
  "mcp:read": "Read projects, environments, runs and reported network activity.",
  "mcp:write": "Start environments and test workflows, and request environment cleanup.",
};

export function consentParameters(query: string): URLSearchParams {
  const params = new URLSearchParams(query);
  if ([...params.keys()].some((key) => params.getAll(key).length !== 1)) {
    throw new Error("The connection request repeats a parameter. Return to your MCP client and start a new connection.");
  }
  return params;
}

export function pendingConsent(value: unknown, organization: string): McpConsent {
  const p = value as Partial<McpConsent> | null;
  if (!p || typeof p.clientName !== "string" || !p.clientName.trim() ||
    p.organization !== organization || typeof p.redirectUri !== "string" ||
    !Array.isArray(p.scopes) || p.scopes.length === 0 ||
    !p.scopes.every((scope) => typeof scope === "string" && Object.hasOwn(mcpScopeLabels, scope)) ||
    new Set(p.scopes).size !== p.scopes.length || p.expiresInDays !== 90) {
    throw new Error("The connection request is incomplete or does not belong to your organization. Nothing has been approved.");
  }
  let destination: URL;
  try { destination = new URL(p.redirectUri); } catch {
    throw new Error("The connection request has an invalid return address. Nothing has been approved.");
  }
  if (!["https:", "http:"].includes(destination.protocol) || destination.username || destination.password || destination.hash) {
    throw new Error("The connection request has an unsupported return address. Nothing has been approved.");
  }
  return {
    clientName: p.clientName,
    scopes: p.scopes,
    organization: p.organization,
    organizationName: typeof p.organizationName === "string" ? p.organizationName : undefined,
    redirectUri: p.redirectUri,
    expiresInDays: p.expiresInDays,
  };
}

/** The server registers the destination. Its approval may add only code and state. */
export function approvalDestination(value: unknown, registeredUri: string, state: string | null): string {
  const result = value as { redirect?: unknown } | null;
  if (typeof result?.redirect !== "string") {
    throw new Error("The approval response was incomplete. Return to your client before trying again.");
  }
  let destination: URL;
  try { destination = new URL(result.redirect); } catch {
    throw new Error("The approval response has an invalid address. Return to your client before trying again.");
  }
  const registered = new URL(registeredUri);
  if (destination.origin !== registered.origin || destination.pathname !== registered.pathname ||
    destination.username || destination.password || destination.hash ||
    ![...registered.searchParams.keys()].every((key) => JSON.stringify(destination.searchParams.getAll(key)) === JSON.stringify(registered.searchParams.getAll(key))) ||
    ![...destination.searchParams.keys()].every((key) => registered.searchParams.has(key) || key === "code" || key === "state") ||
    destination.searchParams.getAll("code").length !== 1 || !destination.searchParams.get("code") ||
    destination.searchParams.getAll("state").length !== (state === null ? 0 : 1) || destination.searchParams.get("state") !== state) {
    throw new Error("The approval response returned a different address or request. Return to your client before trying again.");
  }
  return destination.toString();
}
