// Who is asking, and what to do when the answer is nobody.
//
// Three outcomes and they are deliberately three rather than two:
//
//   signed in, in an organization   render the page
//   not signed in                   send them to sign in, remembering where
//   signed in, in no organization   say so, because every page would be empty
//
// The third is the one that gets missed. Somebody in several organizations, or
// in none, lands with a session that has no tenant, and every query returns
// nothing. Rendering an empty environment matrix for that person is a page
// that looks broken and gives them nothing to act on.

import { redirect } from "next/navigation";
import { ApiError, NotSignedIn, session, type Session } from "./api";

export interface Actor {
  label: string;
  orgId: string;
  /** What to call the organization on screen. Falls back to the identifier,
   *  which is ugly and is still better than saying nothing about which tenant
   *  the page is showing. */
  orgSlug: string;
  role: Session["role"];
}

/** Resolves the caller, or leaves for the sign-in page. */
export async function requireActor(returnTo: string): Promise<Actor | "no-organization"> {
  let current: Session;
  try {
    current = await session();
  } catch (err) {
    if (err instanceof NotSignedIn) return toSignIn(returnTo);
    // The API being unreachable is not the same as being signed out, and
    // sending somebody to sign in during a restart teaches them that the
    // sign-in page is where you go when the product is broken.
    throw err instanceof ApiError ? err : new ApiError("UNREACHABLE", String(err));
  }

  if (!current.signedIn) return toSignIn(returnTo);
  if (!current.orgId) return "no-organization";
  return {
    label: current.label ?? "you",
    orgId: current.orgId,
    orgSlug: current.orgSlug || current.orgId,
    role: current.role ?? null,
  };
}

function toSignIn(returnTo: string): never {
  // The path is carried so that a link out of a pull request comment lands
  // where it was pointed rather than at the front page. Only a path: the API
  // refuses anything else, and this passes on what it was given rather than
  // building a URL out of it.
  const safe = returnTo.startsWith("/") && !returnTo.startsWith("//") ? returnTo : "/";
  redirect(`/login?next=${encodeURIComponent(safe)}`);
}

