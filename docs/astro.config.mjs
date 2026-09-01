// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import tailwindcss from "@tailwindcss/vite";
import { codeTheme } from "./src/code-theme.mjs";

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
  // Static Web Apps canonicalizes documentation routes without a trailing
  // slash. Emitting the same form here keeps the sitemap, canonical, og:url,
  // and final response URL identical instead of making every indexed URL take
  // a redirect before it can be read.
  trailingSlash: "never",
  integrations: [
    starlight({
      title: "Antifailure",
      description:
        "A disposable copy of your production stack for every pull request: masked Postgres branches, contained third-party APIs, agents that use the app like people, and load shaped like your real traffic.",
      favicon: "/favicon.svg",
      customCss: ["./src/styles/antifailure.css"],
      // One theme, not a light/dark pair. The site commits to a single light
      // appearance and the theme toggle is off, so a second code theme would
      // only ever be dead configuration.
      //
      // BEFORE YOU DEBUG A CODE BLOCK THAT RENDERS COMPLETELY UNSTYLED: it is
      // almost certainly Astro's cache, not this config and not the CSS. Astro
      // keeps a stale reference to the Expressive Code stylesheet across a
      // change to this block, so the HTML links a hashed file the build no
      // longer emits. The page 404s its own code CSS silently, the frame loses
      // the overflow-x it gets from that stylesheet, and one wide line then
      // pushes the whole page sideways on a phone. It looks exactly like a
      // theme that was never applied, which is an hour spent re-theming code
      // blocks that were already themed. Any change in here needs:
      //
      //     rm -rf dist .astro node_modules/.astro
      //
      // Confirm it by comparing the ec.<hash>.css the HTML asks for against
      // what is really in dist/_astro.
      expressiveCode: {
        themes: [codeTheme],
        // One theme, so there is no media query to pick between two. Do not
        // add `themeCssSelector: () => null` here: with a single theme it stops
        // Expressive Code emitting its stylesheet at all, which silently drops
        // the frame's own `overflow-x` and lets a wide line push the whole page
        // sideways on a phone.
        useDarkModeMediaQuery: false,
        styleOverrides: {
          // 7px, not 8: Expressive Code adds the border width to this, so 8
          // here renders a 9px outer corner and puts a third value in the scale.
          borderRadius: "7px",
          borderColor: "#2b2d31",
          borderWidth: "1px",
          codeFontFamily: "var(--sl-font-mono)",
          codeFontSize: "0.875rem",
          codeLineHeight: "1.65",
          codePaddingBlock: "0.9rem",
          codePaddingInline: "1rem",
          frames: {
            shadowColor: "transparent",
            frameBoxShadowCssValue: "none",
            editorTabBarBackground: "#111214",
            editorTabBarBorderBottomColor: "#2b2d31",
            editorActiveTabBackground: "#18191b",
            editorActiveTabForeground: "#e4e5e7",
            editorActiveTabBorderColor: "#2b2d31",
            editorActiveTabIndicatorTopColor: "#33bf00",
            terminalBackground: "#18191b",
            terminalTitlebarBackground: "#111214",
            terminalTitlebarForeground: "#c9cbcf",
            terminalTitlebarBorderBottomColor: "#2b2d31",
            inlineButtonBackground: "#ffffff",
            inlineButtonForeground: "#c9cbcf",
            inlineButtonBorder: "#4a4d53",
            tooltipSuccessBackground: "#1a7f00",
            tooltipSuccessForeground: "#ffffff",
          },
        },
      },
      components: {
        Head: "./src/components/Head.astro",
        Header: "./src/components/Header.astro",
        Footer: "./src/components/Footer.astro",
        Pagination: "./src/components/Pagination.astro",
        Search: "./src/components/Search.astro",
        MobileMenuFooter: "./src/components/MobileMenuFooter.astro",
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
      // The social card, and the documentation's place in the site's entity
      // graph.
      //
      // PR #47 ported the marketing half of this work and left the docs half
      // behind, so all 76 pages here shipped with two of these eight tags.
      // Nothing noticed, because www/scripts/check-seo.mjs reads www/out and
      // never looks at docs/dist: the documentation is two thirds of the site's
      // pages and no gate has an opinion about its head.
      //
      // The dimensions and the alt text are not decoration. A scraper that
      // cannot see the image still has to decide how to lay the card out, and
      // og:image:alt is the only accessible description a link preview ever
      // gets. Verified against www/public/og.png, which is really 1200x630.
      head: [
        {
          tag: "meta",
          attrs: { property: "og:image", content: "https://antifailure.dev/og.png" },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:width", content: "1200" },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:height", content: "630" },
        },
        {
          tag: "meta",
          attrs: {
            property: "og:image:alt",
            content:
              "Antifailure, a disposable copy of your production stack for every pull request",
          },
        },
        {
          tag: "meta",
          attrs: { name: "twitter:card", content: "summary_large_image" },
        },
        {
          // Starlight emits twitter:card but no twitter:image. X falls back to
          // og:image, but Slack, Discord and LinkedIn each read a different
          // subset, so stating it costs one tag and removes the guesswork.
          tag: "meta",
          attrs: { name: "twitter:image", content: "https://antifailure.dev/og.png" },
        },
        {
          // Let an engine show a full-size image and an untruncated snippet.
          // Without this it is entitled to show a thumbnail and one grey line.
          tag: "meta",
          attrs: {
            name: "robots",
            content: "index, follow, max-image-preview:large, max-snippet:-1",
          },
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
