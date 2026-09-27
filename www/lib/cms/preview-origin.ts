/** Query parameters identify the editor, never grant access to its API.
 * Only an exact trusted origin and an unpredictable session are accepted. */
export function parsePreviewConnection(search: string, apiOrigin: string, allowLocal = false): { origin: string; session: string } | null {
  const params = new URLSearchParams(search);
  const origin = params.get("parentOrigin");
  const session = params.get("session");
  if (!origin || !session || !/^[a-f0-9]{8}-[a-f0-9]{4}-[1-8][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/i.test(session)) return null;
  try {
    const url = new URL(origin);
    if (url.origin !== origin) return null;
    const allowed = origin === new URL(apiOrigin).origin || origin === "https://app.antifailure.dev";
    const local = allowLocal && ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname) && url.protocol === "http:";
    return allowed || local ? { origin, session } : null;
  } catch { return null; }
}
