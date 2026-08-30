import type { NextConfig } from "next";
import path from "node:path";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  outputFileTracingRoot: path.resolve(__dirname),
  turbopack: {
    root: path.resolve(__dirname),
  },

  // The site is served as static files by Azure Static Web Apps, alongside the
  // documentation build at /docs and the installer at /install.sh. Nothing on
  // the marketing site needs a server: the one dynamic thing it does, the
  // waitlist, is a managed function under /api.
  //
  // This is load bearing rather than a preference. deploy.yml publishes
  // `www/out`, and without `output: "export"` there is no `out` to publish, so
  // dropping this line does not fail the build that produces it. It fails the
  // deploy, later, somewhere else.
  output: "export",
  trailingSlash: false,
  images: {
    // A static export has no server to run the optimiser on, and five
    // components use next/image.
    unoptimized: true,
  },

  // No `redirects()` here on purpose. next.config redirects are evaluated by a
  // server this build does not have, so `output: "export"` refuses them. The
  // one redirect the site needs, /product/crowdi to /product/exploratory-users,
  // is app/product/crowdi/page.tsx: a real page, with the canonical link and
  // noindex, that the CDN can hand out like any other.

  // Host level rules that a static export cannot express live next to the
  // build output in public/staticwebapp.config.json: the 301 for the legacy
  // slug, immutable cache headers on hashed assets, the text/markdown content
  // type for the per page .md twins, and a real 404 status.
};

export default nextConfig;
