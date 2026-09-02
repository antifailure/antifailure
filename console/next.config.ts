import type { NextConfig } from "next";
import path from "node:path";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  outputFileTracingRoot: path.resolve(__dirname),
  turbopack: { root: path.resolve(__dirname) },

  // A static export, served by the control plane's own Hono process from
  // /app/console-out. That is not a deployment convenience, it is the security
  // model: the session is a SameSite=Lax cookie on the control plane's origin,
  // so a console served from anywhere else would need SameSite=None and
  // credentialed CORS, which widens the cross-site surface of every endpoint
  // on the API to move a dashboard to a second hostname.
  //
  // The consequence to design around: there is no server here, so no dynamic
  // route segments. A detail view is a query string on a static page
  // (/runs?run=...), never /runs/[runId], because the latter cannot be
  // exported without knowing every id at build time.
  output: "export",
  trailingSlash: false,

  images: { unoptimized: true },
};

export default nextConfig;
