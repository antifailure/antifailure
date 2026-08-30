import type { MetadataRoute } from "next";
import { SITE_URL } from "@/lib/site";

/**
 * robots.txt.
 *
 * antifailure.dev/robots.txt returned 404 before this file existed. A missing
 * robots.txt is permissive, so nothing was being blocked, but two things were
 * lost: there was nowhere to advertise a sitemap, and there was no record of a
 * decision about AI crawlers. This file makes both explicit.
 *
 * The position taken here is: let them all in.
 *
 * That is a real choice and it deserves its reasoning. Antifailure is an
 * open-core developer tool. Its documentation is already public on GitHub
 * under a licence that permits reproduction, so blocking a training crawler
 * would protect nothing that is not already freely copyable. What it would
 * cost is being absent from the answer a developer gets when they ask an
 * assistant how to rehearse a Postgres migration, which is now a substantial
 * share of how developer tools are discovered at all. For a product nobody has
 * heard of yet, being quotable is worth more than being withheld.
 *
 * The distinction worth knowing, if this is ever revisited: the search and
 * retrieval crawlers (OAI-SearchBot, Claude-SearchBot, PerplexityBot) are what
 * make a page eligible to be cited with a link back. The training crawlers
 * (GPTBot, ClaudeBot, Google-Extended, CCBot) feed model weights and return no
 * referral. They can be decided separately, and blocking the training half
 * while allowing the search half is a coherent position. It is just not the
 * right one for a project at this stage.
 *
 * Two of these are named for a reason that is not obvious:
 *
 *   Google-Extended is not a crawler. It is a permission toggle that governs
 *   whether content Googlebot already fetched may ground a Gemini answer.
 *   Leaving it out is an implicit opt-out of Gemini.
 *
 *   ChatGPT-User and Perplexity-User fire when a person explicitly asks an
 *   assistant to look at a URL. Blocking those breaks the case where somebody
 *   is actively trying to read this site.
 *
 * robots.txt is only half of it. A CDN can refuse these agents underneath a
 * permissive robots.txt and nothing here would say so. Cloudflare began
 * blocking its Training and Agent categories by default for new domains on
 * 2026-09-15. If this site ever moves behind a CDN, that setting has to be
 * checked against this file.
 */
export const dynamic = "force-static";

/** Retrieval and search crawlers. These cite, with a link back. */
const SEARCH_AND_RETRIEVAL = [
  "OAI-SearchBot",
  "ChatGPT-User",
  "Claude-SearchBot",
  "Claude-User",
  "PerplexityBot",
  "Perplexity-User",
  "Applebot",
  "Bingbot",
  "DuckAssistBot",
  "MistralAI-User",
];

/** Training and dataset crawlers. These do not refer traffic. */
const TRAINING_AND_DATASET = [
  "GPTBot",
  "ClaudeBot",
  "anthropic-ai",
  "Google-Extended",
  "Applebot-Extended",
  "CCBot",
  "Meta-ExternalAgent",
  "meta-externalagent",
  "Amazonbot",
  "Bytespider",
  "cohere-ai",
  "Diffbot",
  "TimpiBot",
  "YouBot",
  "AI2Bot",
  "Omgili",
];

/** Never worth indexing: a waitlist form, and Next's build manifests. */
const DISALLOW = ["/signin", "/signup", "/_next/static/chunks/", "/api/"];

export default function robots(): MetadataRoute.Robots {
  return {
    rules: [
      {
        userAgent: "*",
        allow: "/",
        disallow: DISALLOW,
      },
      {
        userAgent: SEARCH_AND_RETRIEVAL,
        allow: "/",
        disallow: DISALLOW,
      },
      {
        userAgent: TRAINING_AND_DATASET,
        allow: "/",
        disallow: DISALLOW,
      },
    ],
    sitemap: [
      `${SITE_URL}/sitemap.xml`,
      // The documentation is a separate Astro build assembled under /docs on
      // the same origin, so its sitemap has to be advertised here too. One
      // origin, one robots.txt, and a crawler that reads it finds both apps.
      `${SITE_URL}/docs/sitemap-index.xml`,
    ],
    host: SITE_URL,
  };
}
