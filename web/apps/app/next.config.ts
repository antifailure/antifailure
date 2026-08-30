import type { NextConfig } from "next";
import path from "node:path";

// Where the API is, from inside this process. In an environment brought up by
// `af up` that is another container on the same network; in development it is
// localhost.
const api = (process.env.AF_API_URL ?? "http://127.0.0.1:8080").replace(/\/+$/, "");

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // A self-contained server directory, so the image carries the framework's
  // own runtime and the handful of modules the pages import rather than the
  // whole workspace. It is also what makes the image buildable inside a sealed
  // environment: nothing is resolved at start-up that was not resolved here.
  output: "standalone",
  // The whole application is per-request: every page reads a session cookie and
  // then reads one tenant's rows. There is nothing here that could be cached
  // at build time, and a page that was would show one organization's
  // environments to another.
  // The workspace root, not this directory. npm hoists dependencies to
  // web/node_modules, so tracing from here would trace a tree with nothing in
  // it and produce a standalone server that cannot start. Two directories up
  // is `web/`, which is where the lockfile and the hoisted modules are.
  outputFileTracingRoot: path.resolve(__dirname, "..", ".."),
  turbopack: { root: path.resolve(__dirname, "..", "..") },

  // The API is proxied under this origin rather than called across one, and
  // that is not a convenience. The session is an HttpOnly cookie the API sets;
  // a cookie set by api.example.test is not sent to app.example.test, so a
  // browser talking to the API directly would sign in and then arrive back
  // holding nothing. Proxying makes the cookie first-party, which also means
  // SameSite=Lax does what it is there for.
  async rewrites() {
    return [
      { source: "/auth/:path*", destination: `${api}/auth/:path*` },
      { source: "/trpc/:path*", destination: `${api}/trpc/:path*` },
    ];
  },

  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "x-content-type-options", value: "nosniff" },
          { key: "referrer-policy", value: "strict-origin-when-cross-origin" },
          // This application renders one organization's data and embeds
          // nothing. Being framed is how a signed-in session gets clicked
          // through by somebody else's page.
          { key: "x-frame-options", value: "DENY" },
          // The content security policy is not here. It carries a per-request
          // nonce, so it is set in middleware.ts, where there is a request to
          // generate one for. A static one in this file cost an afternoon: it
          // blocks the inline scripts the App Router streams a page's content
          // in, which does not degrade the page, it blanks it.

        ],
      },
    ];
  },
};

export default nextConfig;
