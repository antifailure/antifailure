// The content security policy, with a nonce, per request.
//
// This exists because the obvious version does not work and fails in the worst
// possible way. A static `script-src 'self'` header looks correct, passes a
// header scanner, and produces a completely blank page: the App Router streams
// the rest of a page as inline scripts that swap the placeholder for the real
// content, so blocking inline script does not degrade the page, it deletes it.
// The browser says only "Connection closed", which points at the network and
// not at the header that caused it.
//
// The two ways out are 'unsafe-inline', which is the policy with the hole in
// it, and a nonce, which is this. A fresh random value per response goes into
// the header and into every script tag the framework emits, so the scripts we
// sent run and an injected one does not. 'strict-dynamic' is what lets those
// scripts load the chunks they need without every chunk having to be listed.
//
// A nonce forces every response to be dynamic, which costs nothing here: every
// page in this application reads a session cookie and one tenant's rows, so
// none of them was ever cacheable.

import { NextResponse, type NextRequest } from "next/server";

export function middleware(request: NextRequest): NextResponse {
  // 128 bits from the platform's own generator. Reused across two responses it
  // would be worth nothing, so it is generated here rather than at module
  // scope, which would compute one per process.
  const nonce = Buffer.from(crypto.randomUUID()).toString("base64");

  const policy = [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'`,
    // Inline style stays. The framework emits critical CSS inline, and a style
    // attribute cannot execute: the reason to forbid inline script does not
    // apply to it.
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data:",
    "font-src 'self' data:",
    // Same origin only. The API is reached through this origin's rewrites, so
    // there is no second host for a page to talk to and no reason to allow one.
    "connect-src 'self'",
    "form-action 'self'",
    "frame-ancestors 'none'",
    "base-uri 'none'",
    "object-src 'none'",
  ].join("; ");

  // Passed on to the render, which is how the framework learns the value to
  // stamp on the tags it emits. Without this the header names a nonce that
  // nothing carries, which is the blank page again.
  const headers = new Headers(request.headers);
  headers.set("x-nonce", nonce);

  const response = NextResponse.next({ request: { headers } });
  response.headers.set("content-security-policy", policy);
  return response;
}

export const config = {
  matcher: [
    // Everything except the framework's own static output and the files a
    // browser asks for on its own. Those are static, carry no script, and
    // running middleware for each one is latency on every asset of every page.
    {
      source: "/((?!_next/static|_next/image|favicon.ico).*)",
      missing: [
        // A prefetch of an RSC payload is answered from the same render as the
        // navigation it precedes; setting a second nonce on it would mean the
        // document and its payload disagree about which scripts may run.
        { type: "header", key: "next-router-prefetch" },
        { type: "header", key: "purpose", value: "prefetch" },
      ],
    },
  ],
};
