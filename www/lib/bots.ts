/**
 * Whether the thing reading this page is a program.
 *
 * WHY THIS RUNS IN THE BROWSER AND NOT AT THE EDGE.
 *
 * The usual place to filter a crawler is the server, against the User-Agent
 * header. This product cannot do that, and the reason is the same one that
 * shapes the rest of www/lib/analytics.ts: the user agent never reaches the
 * server. It is not sent, it is not logged, and there is no header parsing to
 * add a rule to. So the filter has to be here, in the page, where the string
 * already is.
 *
 * That trade is worth writing down rather than glossing. A server side filter
 * sees every request including the ones that never execute JavaScript, which is
 * most crawlers. This one only sees the crawlers that DO run JavaScript, and it
 * can be defeated by any of them that chooses a different user agent. So it is
 * a floor on cleanliness rather than a guarantee, the dashboard says so next to
 * the numbers, and nobody should quote these counts as audited.
 *
 * What this buys in exchange is that no new value crosses the network. The user
 * agent is read here, compared here, and discarded here. The only effect of a
 * match is that a request is NOT made, which is the one form of measurement
 * that cannot leak anything.
 *
 * THE LIST.
 *
 * The substrings below are adapted from DEFAULT_BLOCKED_UA_STRS in posthog-js,
 * which is MIT licensed:
 *
 *   Copyright (c) 2020-2024 PostHog Inc.
 *   Released under the MIT License.
 *
 * The notice is kept HERE rather than in THIRD_PARTY_NOTICES.md, and that is
 * not carelessness: that file is generated from the dependency graph, PostHog
 * is not a dependency of anything here, and an entry added to it by hand would
 * be wiped the next time it is regenerated. Adapted source carries its notice
 * in the file that adapted it.
 *
 * It is their list of crawler user agent fragments, kept because a list of
 * which strings identify which crawler is a fact about the world rather than a
 * design, and rewriting it from memory would produce a worse list with no
 * benefit. The matching code below is this repository's own.
 *
 * WHY SUBSTRINGS AND NOT A REGULAR EXPRESSION.
 *
 * A crawler names itself in the middle of a longer string, usually alongside a
 * browser it is pretending to be, so an anchored pattern matches nothing. Each
 * entry here is chosen to be long enough that it cannot appear inside a real
 * browser's user agent: `pinterestbot` rather than `pinterest`, because the
 * Pinterest application's own browser carries `Pinterest/iOS` and is a person.
 */

/**
 * Crawler fragments, lowercased, grouped so a reader can tell why each is here.
 *
 * Every one of these is compared with indexOf against a lowercased user agent.
 */
export const CRAWLER_FRAGMENTS: readonly string[] = [
  // Search engines.
  'googlebot',
  'googleother',
  'google-inspectiontool',
  'google-read-aloud',
  'google favicon',
  'google web preview',
  'adsbot-google',
  'apis-google',
  'feedfetcher-google',
  'mediapartners-google',
  'storebot-google',
  'duplexweb-google',
  'bingbot',
  'bingpreview',
  'msnbot',
  'yandexbot',
  'baiduspider',
  'duckduckbot',
  'petalbot',
  'slurp',
  'applebot',

  // Answer engines and model crawlers. Their own category because this site's
  // acquisition catalog treats `ai` as a channel distinct from `search`, and a
  // crawler for one is not a reader from the other.
  'gptbot',
  'oai-searchbot',
  'chatgpt-user',
  'perplexitybot',
  'claudebot',
  'anthropic-ai',
  'bytespider',
  'meta-externalagent',
  'google-cloudvertexbot',

  // Link unfurlers. A message posted in a chat fetches the page once per
  // channel, which is one page view per share and no reader at all.
  'facebookexternal',
  'facebookcatalog',
  'twitterbot',
  'linkedinbot',
  'slackbot',
  'discordbot',
  'telegrambot',
  'whatsapp',
  'pinterestbot',
  'redditbot',

  // Search engine optimisation and backlink crawlers.
  'ahrefsbot',
  'ahrefssiteaudit',
  'semrushbot',
  'siteauditbot',
  'splitsignalbot',
  'dataforseobot',
  'mj12bot',
  'rogerbot',
  'screaming frog',
  'sitebulb',
  'backlinksextendedbot',
  'awariobot',
  'trendictionbot',
  'leikibot',
  'seokicks',

  // Archives, monitors and scanners.
  'archive.org_bot',
  'ia_archiver',
  'uptimerobot',
  'better uptime bot',
  'sentryuptimebot',
  'nessus',
  'deepscan',
  'turnitin',
  'hubspot',
  'prerender',

  // Automation and measurement tooling, which is this team's own traffic more
  // often than anybody else's. A Lighthouse run is a page view that no person
  // ever looked at.
  'chrome-lighthouse',
  'headlesschrome',
  'phantomjs',
  'playwright',
  'puppeteer',
  'cypress',
  'selenium',
  'vercel-screenshot',
  'vercelbot',

  // Generic shapes, last because they are the widest. Each one is a convention
  // a crawler follows to announce itself, and none of them appears in a real
  // browser's user agent.
  //
  // Deliberately no entry that is an address. Yandex announces itself with
  // "+http://yandex.com/bots" and `yandexbot` above already matches the same
  // string, so the address form bought nothing and it trips the gate that
  // checks no file in the beacon names an external host. A list of hosts this
  // code never contacts, sitting next to a claim that it contacts none, is the
  // shape of thing somebody has to re-read every time.
  'bot.htm',
  'bot.php',
  '(bot;',
  'bot/',
  'crawler',
  'spider',
];

/** Whether a user agent names a crawler. Lowercased once by the caller's
 *  string, so a list entry is compared as written. */
export function isCrawlerAgent(userAgent: string | undefined | null): boolean {
  if (!userAgent) return false;
  const lower = userAgent.toLowerCase();
  return CRAWLER_FRAGMENTS.some((fragment) => lower.includes(fragment));
}

/** The shape of navigator.userAgentData this file reads, which is two fields of
 *  an experimental interface. Declared narrowly rather than imported, because a
 *  browser that does not have it must not be a type error. */
interface UserAgentBrands {
  brands?: { brand?: string }[];
}

/**
 * Whether the reader is a program, by every signal available in the page.
 *
 * Three checks, in the order they are cheap. The user agent string, the brand
 * list a Chromium browser exposes separately (a crawler that spoofs one and
 * forgets the other is caught by the half it forgot), and the automation flag
 * a driven browser sets on itself.
 *
 * Every accessor is wrapped. userAgentData is experimental, a browser may
 * expose it and throw on read under a permissions policy, and an analytics
 * module that can break a page is worse than one that records nothing.
 */
export function looksAutomated(nav: Navigator | undefined): boolean {
  if (!nav) return false;
  try {
    if (isCrawlerAgent(nav.userAgent)) return true;
  } catch {
    // A navigator without a userAgent is not a signal either way.
  }
  try {
    const brands = (nav as Navigator & { userAgentData?: UserAgentBrands }).userAgentData?.brands;
    if (Array.isArray(brands) && brands.some((b) => isCrawlerAgent(b?.brand))) return true;
  } catch {
    // Experimental surface. Its absence is not a signal.
  }
  try {
    // Set by WebDriver, which is Playwright, Puppeteer, Selenium and every
    // headless run in this repository's own test suite.
    if (nav.webdriver === true) return true;
  } catch {
    // Older browsers do not define it.
  }
  return false;
}
