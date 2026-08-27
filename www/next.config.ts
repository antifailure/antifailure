import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // The site is served as static files by Azure Static Web Apps, alongside the
  // documentation build at /docs and the installer at /install.sh. Nothing on
  // the marketing site needs a server: the one dynamic thing it does, the
  // waitlist, is a managed function under /api.
  output: "export",
  trailingSlash: false,
  images: {
    // Static export cannot run the optimiser at request time. The source
    // images are large, so they are pre-resized at build time instead; see
    // scripts/optimise-images.mjs.
    unoptimized: true,
  },
};

export default nextConfig;
