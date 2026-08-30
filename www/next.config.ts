import type { NextConfig } from "next";
import path from "node:path";

const isDev = process.env.NODE_ENV !== "production";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  outputFileTracingRoot: path.resolve(__dirname),
  turbopack: {
    root: path.resolve(__dirname),
  },

  // Local only. Production does not use this: deploy.yml builds the Starlight
  // site separately and assemble.sh places it at /docs. `next dev` has no such
  // step, so without a proxy the Docs link 404s. Omitted from the export build
  // because `output: "export"` refuses rewrites.
  ...(isDev
    ? {
        async rewrites() {
          return [
            { source: "/docs", destination: "http://127.0.0.1:4321/docs" },
            { source: "/docs/:path*", destination: "http://127.0.0.1:4321/docs/:path*" },
          ];
        },
      }
    : {}),

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
};

export default nextConfig;
