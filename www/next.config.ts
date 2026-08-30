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
  // step, so without a proxy the Docs link 404s.
  //
  // `output: "export"` refuses rewrites, including in `next dev`, so the
  // static export is production-only. The deploy still publishes `www/out`.
  ...(isDev
    ? {
        async rewrites() {
          return [
            { source: "/docs", destination: "http://127.0.0.1:4321/docs" },
            { source: "/docs/:path*", destination: "http://127.0.0.1:4321/docs/:path*" },
          ];
        },
      }
    : { output: "export" }),

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
