// Signing out.
//
// A POST rather than a GET, because a link that ends a session is a link that
// a prefetcher, a link scanner in somebody's mail client, or an image tag on
// another site can follow on your behalf. The header renders it as a one
// button form for that reason.
//
// The cookie is cleared here as well as at the API. The API is what makes the
// token stop working, and that is the part that matters; clearing it here is
// what stops the browser from sending a dead cookie on every subsequent
// request and getting the sign-in page each time as if something were wrong.

import { NextResponse } from "next/server";
import { cookies } from "next/headers";

const API = (process.env.AF_API_URL ?? "http://127.0.0.1:8080").replace(/\/+$/, "");
const SESSION_COOKIE = "af_session";

export async function POST(request: Request): Promise<Response> {
  const jar = await cookies();
  const token = jar.get(SESSION_COOKIE)?.value;

  if (token) {
    try {
      await fetch(`${API}/auth/signout`, {
        method: "POST",
        headers: { cookie: `${SESSION_COOKIE}=${token}` },
        cache: "no-store",
        signal: AbortSignal.timeout(5_000),
      });
    } catch {
      // The API being unreachable must not leave somebody stuck signed in on a
      // shared machine. The cookie goes either way, and the session expires on
      // its own; this is the one place where the weaker outcome is the right
      // one, because the alternative is a sign-out button that does nothing.
    }
  }

  const response = NextResponse.redirect(new URL("/login", request.url), 303);
  response.cookies.set({
    name: SESSION_COOKIE,
    value: "",
    path: "/",
    httpOnly: true,
    sameSite: "lax",
    maxAge: 0,
  });
  return response;
}
