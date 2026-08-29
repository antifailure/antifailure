// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import tailwindcss from "@tailwindcss/vite";

// Every error the engine prints ends with a link to this site. errors.go builds
// it as "https://antifailure.dev/docs/" + the catalog's docs field, and
// tools/errcheck refuses to build if any of those fields has no page under
// src/content/docs. So the routing here is not a preference: `base` has to be
// /docs and a page's URL has to be its path under src/content/docs, exactly.
// Change either and every error message in the product starts lying.
export default defineConfig({
  vite: { plugins: [tailwindcss()] },
  site: "https://antifailure.dev",
  base: "/docs",
  trailingSlash: "ignore",
  integrations: [
    starlight({
      title: "Antifailure",
      description:
        "A disposable copy of your production stack for every pull request: masked Postgres branches, contained third-party APIs, agents that use the app like people, and load shaped like your real traffic.",
      favicon: "/favicon.svg",
      customCss: ["./src/styles/antifailure.css"],
      components: {
        Header: "./src/components/Header.astro",
        Footer: "./src/components/Footer.astro",
        Pagination: "./src/components/Pagination.astro",
        Search: "./src/components/Search.astro",
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/antifailure/antifailure",
        },
      ],
      lastUpdated: true,
      pagination: true,
      head: [
        {
          tag: "meta",
          attrs: { property: "og:image", content: "https://antifailure.dev/og.png" },
        },
        {
          tag: "meta",
          attrs: { name: "twitter:card", content: "summary_large_image" },
        },
      ],
      // Groups are ordered the way somebody actually arrives: install it, learn
      // the words, follow a guide for your stack, then look things up. Inside a
      // group the order comes from each page's own `sidebar.order` frontmatter,
      // which is already declared on all 35 pages.
      sidebar: [
        { label: "Getting started", items: [{ autogenerate: { directory: "getting-started" } }] },
        { label: "Concepts", items: [{ autogenerate: { directory: "concepts" } }] },
        { label: "Guides", items: [{ autogenerate: { directory: "guides" } }] },
        { label: "Providers", items: [{ autogenerate: { directory: "providers" } }] },
        { label: "Reference", items: [{ autogenerate: { directory: "reference" } }] },
        { label: "Security", items: [{ autogenerate: { directory: "security" } }] },
        { label: "Self-hosting", items: [{ autogenerate: { directory: "self-hosting" } }] },
        { label: "Enterprise", items: [{ autogenerate: { directory: "enterprise" } }] },
        { label: "Contributing", items: [{ autogenerate: { directory: "contributing" } }] },
      ],
    }),
  ],
});
