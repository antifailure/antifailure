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
  // "never", to match the host, rather than Astro's default of "ignore".
  //
  // www/public/staticwebapp.config.json sets "trailingSlash": "never" for the
  // whole site, so Azure answers /docs/reference/cli/ with a 301 to
  // /docs/reference/cli. Astro under "ignore" wrote the slashed form into
  // every canonical and every sitemap entry, so all 82 documentation pages
  // declared a canonical the host does not serve and the sitemap offered 81
  // URLs that redirect. Documentation is about seventy percent of the site,
  // so that was the majority of it pointing an engine at an address it then
  // has to be told again is somewhere else.
  //
  // The site's own catalogue names this: seo-geo-catalog.md item 8, "pick one
  // trailing-slash policy and enforce it; mixed policies split signals". Two
  // policies were in force, one per build, and nothing compared them.
  //
  // build.format stays "directory", the default, so the files on disk do not
  // move: a page is still <route>/index.html, which is what SWA resolves the
  // slash-free address to. Only the URLs Astro writes change.
  trailingSlash: "never",
  integrations: [
    starlight({
      title: "Antifailure",
      description:
        "A disposable copy of your production stack for every pull request: masked Postgres branches, contained third-party APIs, agents that use the app like people, and load shaped like your real traffic.",
      favicon: "/favicon.svg",
      // This site ships its own 404 at src/pages/404.astro, and Starlight
      // injects one at the same pattern unless told not to. Both were declared,
      // Astro warned on every single build that "/404" is defined twice, and
      // Astro's own message says a collision becomes a hard error in a later
      // version. Which of the two won was undefined.
      //
      // Ours is the one that should win, and not by seniority: somebody who
      // lands on it arrived from a path printed at the end of an engine error
      // message, so it names the four pages they were most likely reaching for
      // and tells them the address in their terminal is the wrong half. The
      // Starlight default is a generic page with none of that.
      //
      // A build that warns every time is a build people stop reading.
      disable404Route: true,
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
      // which `just sidebarcheck` requires to be a unique whole number per
      // directory, because Starlight breaks a tie on the FILE NAME and that is
      // invisible to somebody editing a page.
      //
      // REFERENCE AND SELF-HOSTING ARE LISTED BY HAND AND THE OTHER SEVEN ARE
      // NOT, and the difference is not a preference. `autogenerate` turns a
      // subdirectory into a nested group labelled with the directory name and
      // offers no way to override it: navigation.ts:334 is `label: dirName`.
      // So the two groups with subdirectories rendered as `schemas` and
      // `runbooks`, raw lowercase slugs sitting among nine sentence-case
      // labels, and one of them contained a page called "Runbooks", which is
      // the same thing named twice in two casings.
      //
      // Renaming the directories is not the way out. The comment at the top of
      // this file is the reason: a page's URL is its path under
      // src/content/docs exactly, every engine error links to one, and
      // tools/errcheck refuses to build if any of those stops resolving. A
      // directory name also cannot contain a space, so no directory can ever
      // produce a sentence-case label.
      //
      // Listing by hand is what rots, so it is gated: `just sidebarcheck`
      // fails if a page under either directory is missing from this list or
      // named here twice. Add a page and the gate tells you to put it here.
      sidebar: [
        { label: "Getting started", items: [{ autogenerate: { directory: "getting-started" } }] },
        { label: "Concepts", items: [{ autogenerate: { directory: "concepts" } }] },
        { label: "Guides", items: [{ autogenerate: { directory: "guides" } }] },
        { label: "Providers", items: [{ autogenerate: { directory: "providers" } }] },
        {
          label: "Reference",
          items: [
            "reference/cli",
            "reference/manifest",
            "reference/errors",
            "reference/transforms",
            "reference/control-plane",
            "reference/api",
            "reference/environment-lifetime",
            "reference/mcp",
            {
              label: "Generated schemas",
              items: ["reference/schemas/manifest-v1", "reference/schemas/events-v1"],
            },
            "reference/stability",
          ],
        },
        { label: "Security", items: [{ autogenerate: { directory: "security" } }] },
        {
          label: "Self-hosting",
          items: [
            "self-hosting/control-plane",
            "self-hosting/azure",
            "self-hosting/production",
            "self-hosting/operations",
            "self-hosting/on-call",
            "self-hosting/releasing",
            "self-hosting/status-page",
            "self-hosting/rotating-secrets",
            {
              label: "Runbooks",
              items: [{ autogenerate: { directory: "self-hosting/runbooks" } }],
            },
          ],
        },
        { label: "Enterprise", items: [{ autogenerate: { directory: "enterprise" } }] },
        { label: "Contributing", items: [{ autogenerate: { directory: "contributing" } }] },
      ],
    }),
  ],
});
