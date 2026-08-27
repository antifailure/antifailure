/**
 * The waitlist client.
 *
 * This used to be `localStorage.setItem(...)` and nothing else, under a screen
 * that said "We'll email you when the control plane can connect a repo". The
 * address never left the visitor's own browser, so that sentence could not
 * have been true for anybody. The address now goes to a server and is stored,
 * and this module will not report success unless the server said so.
 *
 * The local copy is kept, but only as a convenience: it lets a returning
 * visitor see that they already signed up without a round trip. It is never
 * the record. If the browser has forgotten and the server has not, the person
 * simply signs up again and the server treats it as the same row.
 */

const LOCAL_KEY = "af.waitlist.v1";

export type WaitlistSource = "signin" | "signup" | "modal" | "footer";

export type WaitlistResult =
  | { ok: true; email: string; alreadyJoined: boolean }
  | { ok: false; message: string };

/** The address this device last submitted successfully, if any. */
export function rememberedEmail(): string | null {
  try {
    const raw = localStorage.getItem(LOCAL_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { email?: unknown };
    return typeof parsed.email === "string" ? parsed.email : null;
  } catch {
    // Private mode, disabled site data, or a value somebody else wrote.
    // None of those are worth a broken page.
    return null;
  }
}

function remember(email: string) {
  try {
    localStorage.setItem(LOCAL_KEY, JSON.stringify({ email, at: Date.now() }));
  } catch {
    // Storage being unavailable must not turn a successful signup into a
    // failure, because the server already has the address.
  }
}

/**
 * Deliberately permissive: this rejects the shapes that are certainly not
 * addresses and leaves the rest to the server, which is the only thing that
 * can really tell. A regex that tries to be authoritative about email
 * addresses rejects real ones.
 */
export function looksLikeEmail(value: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(value);
}

export async function joinWaitlist(
  rawEmail: string,
  source: WaitlistSource,
): Promise<WaitlistResult> {
  const email = rawEmail.trim().toLowerCase();
  if (!looksLikeEmail(email)) {
    return { ok: false, message: "That does not look like an email address." };
  }

  let response: Response;
  try {
    response = await fetch("/api/waitlist", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ email, source }),
    });
  } catch {
    return {
      ok: false,
      message: "Could not reach the server. Check your connection and try again.",
    };
  }

  if (response.status === 429) {
    return { ok: false, message: "Too many attempts. Try again in a minute." };
  }

  if (!response.ok) {
    // Surface the server's own words when it gave any, because a generic
    // failure message on a form is how people conclude a product is broken.
    let message = "Something went wrong on our side. Try again shortly.";
    try {
      const body = (await response.json()) as { message?: unknown };
      if (typeof body.message === "string" && body.message) message = body.message;
    } catch {
      // Keep the default.
    }
    return { ok: false, message };
  }

  let alreadyJoined = false;
  try {
    const body = (await response.json()) as { alreadyJoined?: unknown };
    alreadyJoined = body.alreadyJoined === true;
  } catch {
    // A 2xx with a body we cannot read is still a success. The row is written.
  }

  remember(email);
  return { ok: true, email, alreadyJoined };
}
