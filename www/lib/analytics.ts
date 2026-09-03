/**
 * The site beacon.
 *
 * WHAT THIS SENDS, IN FULL. There is no second list anywhere.
 *
 *   a random identifier that lives for one browsing session
 *   a route id from a closed list, never the URL and never the path
 *   a source from a closed list, derived from the referrer here and discarded
 *   a campaign id matching ^[a-z0-9][a-z0-9_-]{0,31}$, or nothing
 *   a timestamp, and whether this was the first page of the session
 *
 * WHAT NEVER LEAVES THE BROWSER.
 *
 * The referrer, the URL, the query string, the user agent, the screen size, the
 * language, the timezone, the address. The normalization below runs HERE, in
 * the page, so the raw referrer is never put on the network at all. That is a
 * stronger claim than "the server discards it", and it is the whole reason this
 * file exists rather than a server that parses a Referer header.
 *
 * NO COOKIE. NO localStorage. NO CROSS-VISIT IDENTIFIER.
 *
 * The session identifier lives in sessionStorage, which the browser deletes
 * when the tab closes. Two visits a day apart are two unrelated sessions and
 * nothing here can join them. That is a real loss of information and it is the
 * deliberate trade: persistent attribution is off until a consent record, a
 * retention decision and a deletion path exist, and shipping it first and
 * asking later is how a product ends up with data it cannot justify keeping.
 *
 * IT TURNS ITSELF OFF WHEN ASKED.
 *
 * Global Privacy Control and Do Not Track are both honoured, and so is a
 * missing endpoint. A reader who has expressed a preference is not measured,
 * which costs a percentage point of a count and is the correct answer.
 */

import { usePathname } from "next/navigation";
import { useEffect, useRef } from "react";
import { CONTROL_PLANE_URL } from "./site";

/**
 * Where the beacon goes.
 *
 * The control plane's own address by default, following CONTROL_PLANE_URL for
 * the same reason every other cross-site link does: a preview deployment
 * pointed at a staging control plane must not post its counts into production.
 *
 * Set NEXT_PUBLIC_AF_ANALYTICS_ENDPOINT to the empty string to turn the beacon
 * off entirely for a build. Defaulted ON rather than off, because a feature
 * that is off until somebody sets a variable is a feature that ships inert and
 * looks finished, which is the defect this repository has shipped most often.
 */
const ENDPOINT =
  process.env.NEXT_PUBLIC_AF_ANALYTICS_ENDPOINT ?? `${CONTROL_PLANE_URL}/v1/site/events`;

const SESSION_KEY = "af.session.v1";
const ATTRIBUTION_KEY = "af.attribution.v1";

/**
 * The page shapes this site has.
 *
 * Mirrors SITE_ROUTES in web/apps/api/src/analytics/catalog.ts, and a test in
 * the control plane reads both files and fails when they disagree. Two lists
 * that must agree and no gate over them is how the second one quietly stops
 * matching and every event starts arriving as `other`.
 */
export type SiteRoute =
  | "home"
  | "product"
  | "product_detail"
  | "solutions"
  | "solutions_detail"
  | "pricing"
  | "blog"
  | "blog_post"
  | "legal"
  | "signin"
  | "signup"
  | "other";

export type VisitSource =
  | "direct"
  | "search"
  | "ai"
  | "social"
  | "github"
  | "news"
  | "email"
  | "campaign"
  | "referral"
  | "internal";

/**
 * One value, because there is one producer.
 *
 * The docs, GitHub and install buttons are server-rendered links with no click
 * handler, and adding one would turn two server components into client
 * components to record a number. Declaring them here anyway would put three
 * bars on a chart that read zero forever, which reads as a broken chart rather
 * than as an absent feature.
 *
 * The interesting variation is the `route` dimension: waitlist dialogs opened
 * per page, against submissions per page, is form conversion by page.
 */
export type Cta = "waitlist_open";

/**
 * A path, as one of the shapes above.
 *
 * The slug is dropped on purpose. `/blog/why-a-green-ci-proves-nothing` becomes
 * `blog_post`, so the chart says blog posts convert and cannot say which reader
 * read which post. Knowing the first is the question; knowing the second is
 * surveillance with a different name.
 */
export function routeIdFor(pathname: string): SiteRoute {
  // The trailing .html and any trailing slash come off first. A static export
  // is a tree of files, and which of /pricing, /pricing/ and /pricing.html a
  // reader's URL bar holds is decided by the host rather than by this site:
  // Azure Static Web Apps serves the clean path, and a plain file server serves
  // the file. Without this, every page under a file server classified as
  // `other`, which reads as readers landing nowhere. Found by serving the real
  // built output and watching the row arrive.
  const path = pathname.replace(/\.html$/, "").replace(/\/+$/, "") || "/";
  if (path === "/") return "home";
  if (path === "/product") return "product";
  if (path.startsWith("/product/")) return "product_detail";
  if (path === "/solutions") return "solutions";
  if (path.startsWith("/solutions/")) return "solutions_detail";
  if (path === "/pricing") return "pricing";
  if (path === "/blog") return "blog";
  if (path.startsWith("/blog/")) return "blog_post";
  if (path === "/signin") return "signin";
  if (path === "/signup") return "signup";
  if (["/privacy", "/terms", "/dpa", "/subprocessors", "/data-retention", "/sla"].includes(path)) {
    return "legal";
  }
  return "other";
}

/**
 * A referrer, as a channel.
 *
 * The registrable domain decides, and then the referrer is dropped. `ai` is its
 * own channel rather than part of `search` because an answer engine sending a
 * reader is a different thing from a results page, and rolling them together is
 * how somebody cannot tell which one is working.
 *
 * An unrecognised host is `referral` rather than its own value, which is the
 * point: knowing that somebody arrived from another site is the useful part,
 * and knowing which site turns a bounded enum back into free text.
 */
export function sourceFor(referrer: string, here: string): VisitSource {
  if (!referrer) return "direct";
  let host: string;
  try {
    const url = new URL(referrer);
    host = url.hostname.toLowerCase();
    if (url.hostname === new URL(here).hostname) return "internal";
  } catch {
    return "direct";
  }

  const has = (...needles: string[]) => needles.some((n) => host === n || host.endsWith(`.${n}`));

  if (has("google.com", "bing.com", "duckduckgo.com", "search.brave.com", "ecosia.org", "yandex.com", "baidu.com")) {
    return "search";
  }
  if (has("chatgpt.com", "openai.com", "claude.ai", "anthropic.com", "perplexity.ai", "gemini.google.com", "copilot.microsoft.com")) {
    return "ai";
  }
  if (has("github.com", "gist.github.com")) return "github";
  if (has("news.ycombinator.com", "lobste.rs", "reddit.com", "slashdot.org")) return "news";
  if (has("x.com", "twitter.com", "linkedin.com", "bsky.app", "mastodon.social", "youtube.com", "facebook.com")) {
    return "social";
  }
  if (has("mail.google.com", "outlook.com", "outlook.office.com", "mail.yahoo.com")) return "email";
  return "referral";
}

/**
 * A campaign identifier, from the one query parameter this site reads.
 *
 * `?c=launch-2026` and nothing else. Not utm_source, not utm_medium, not
 * utm_content, and never the whole query string: each of those is another field
 * that a link can be made to carry an identifier in, and one bounded parameter
 * answers the only question a campaign needs to answer, which is which link
 * they clicked.
 *
 * A value that does not match the pattern is dropped rather than truncated,
 * because a truncated identifier is still an identifier.
 */
export function campaignFor(search: string): string | null {
  const value = new URLSearchParams(search).get("c");
  if (!value) return null;
  return /^[a-z0-9][a-z0-9_-]{0,31}$/.test(value) ? value : null;
}

/** Whether the reader has asked not to be measured. */
function optedOut(): boolean {
  const nav = navigator as Navigator & { globalPrivacyControl?: boolean; doNotTrack?: string };
  if (nav.globalPrivacyControl === true) return true;
  if (nav.doNotTrack === "1") return true;
  // Safari and older Firefox put it on window rather than on navigator.
  const legacy = (window as unknown as { doNotTrack?: string }).doNotTrack;
  return legacy === "1" || legacy === "yes";
}

interface Attribution {
  source: VisitSource;
  landing: SiteRoute;
  campaign: string | null;
}

/**
 * The identifier for this browsing session, and the channel it started on.
 *
 * Both live in sessionStorage together, so they die together. Attribution is
 * the FIRST page's channel, held for the length of the session, because "what
 * brought them here" is a property of the arrival and every page after it has
 * an internal referrer.
 *
 * Every read and write is wrapped, because a browser in private mode, or one
 * with site data disabled, throws on the accessor itself rather than returning
 * null. An analytics module that can break a page is worse than one that
 * records nothing.
 */
function session(): { id: string; attribution: Attribution; entry: boolean } | null {
  let id: string | null = null;
  let attribution: Attribution | null = null;
  try {
    id = sessionStorage.getItem(SESSION_KEY);
    const raw = sessionStorage.getItem(ATTRIBUTION_KEY);
    if (raw) attribution = JSON.parse(raw) as Attribution;
  } catch {
    return null;
  }

  if (id && attribution) return { id, attribution, entry: false };

  // A campaign link is its own channel. Somebody arriving through ?c=launch-2026
  // came from the campaign, whatever page happened to host the link, and
  // reporting them as `email` or `social` would put the campaign's readers in
  // the same bar as everybody else who came from there.
  const campaign = campaignFor(location.search);
  const fresh = {
    id: randomId(),
    attribution: {
      source: campaign ? ("campaign" as const) : sourceFor(document.referrer, location.href),
      landing: routeIdFor(location.pathname),
      campaign,
    },
  };
  try {
    sessionStorage.setItem(SESSION_KEY, fresh.id);
    sessionStorage.setItem(ATTRIBUTION_KEY, JSON.stringify(fresh.attribution));
  } catch {
    // Nothing is stored, so the next page starts a new session. The counts are
    // then per page rather than per visit, which is a worse number and not a
    // broken one, and it beats refusing to render.
  }
  return { ...fresh, entry: true };
}

/** 128 bits of randomness, from the browser's own generator. */
function randomId(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

interface Wire {
  id: string;
  name: string;
  at: string;
  session: string;
  payload: Record<string, string | boolean>;
}

/**
 * Sends one event, and does not care whether it arrives.
 *
 * `keepalive` so a beacon fired as the reader navigates away is still sent, and
 * a failure is swallowed with no retry: retrying a page view is how a tab that
 * has been asleep wakes up and posts a hundred events dated yesterday, and the
 * event is worth less than the request.
 *
 * The identifier and the timestamp are stamped together, once, and would be
 * resent unchanged if this ever grew a retry. The control plane's table is
 * partitioned on the timestamp, so a retry that restamps it inserts a second
 * row rather than colliding with the first.
 */
function send(event: Wire): void {
  if (!ENDPOINT) return;
  void fetch(ENDPOINT, {
    method: "POST",
    keepalive: true,
    // No cookie, ever. Said in the request rather than left to the default, so
    // that a change here is a change somebody has to write down.
    credentials: "omit",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ events: [event] }),
  }).catch(() => {
    // The reader's page is not made worse by a measurement failing.
  });
}

function emit(name: string, payload: Record<string, string | boolean>): void {
  if (typeof window === "undefined" || !ENDPOINT || optedOut()) return;
  const s = session();
  if (!s) return;
  send({ id: randomId(), name, at: new Date().toISOString(), session: s.id, payload });
}

/**
 * One page view.
 *
 * `entry` is what separates the arrival from the pages after it. Attribution is
 * read off the arrival, so a chart of "which channel landed on which page" is
 * about where people come in, and the pages after it are counted as internal
 * rather than as new arrivals from nowhere.
 */
export function pageViewed(route: SiteRoute): void {
  if (typeof window === "undefined" || !ENDPOINT || optedOut()) return;
  const s = session();
  if (!s) return;
  const payload: Record<string, string | boolean> = {
    route,
    source: s.entry ? s.attribution.source : "internal",
    entry: s.entry,
  };
  if (s.entry && s.attribution.campaign) payload.campaign = s.attribution.campaign;
  send({ id: randomId(), name: "site.page_viewed", at: new Date().toISOString(), session: s.id, payload });
}

/** A call to action was pressed. */
export function ctaEngaged(cta: Cta): void {
  emit("site.cta_engaged", { cta, route: routeIdFor(location.pathname) });
}

/**
 * A waitlist submission, carrying the channel the SESSION started on.
 *
 * That is the whole point of this event and it is why attribution is held for
 * the session rather than read per page. A visitor arrives from a search
 * result, reads three pages, and signs up on the pricing page; taking the
 * referrer at submission time would attribute every one of those to
 * `internal`, and the acquisition funnel would report that nothing works.
 */
export function waitlistSubmitted(outcome: "joined" | "already" | "refused"): void {
  if (typeof window === "undefined" || !ENDPOINT || optedOut()) return;
  const s = session();
  if (!s) return;
  const payload: Record<string, string | boolean> = {
    source: s.attribution.source,
    landing: s.attribution.landing,
    outcome,
  };
  if (s.attribution.campaign) payload.campaign = s.attribution.campaign;
  send({
    id: randomId(),
    name: "site.waitlist_submitted",
    at: new Date().toISOString(),
    session: s.id,
    payload,
  });
}

/**
 * Fires one page view per route the reader lands on.
 *
 * Mounted once, in the layout, so it survives client-side navigation between
 * pages. The ref is what stops React's development double-render, and a route
 * the reader has come back to, from being counted twice: usePathname changes on
 * navigation and the effect runs again, which is exactly what is wanted, and it
 * also runs again on a re-render that changed nothing, which is not.
 */
export function usePageViews(): void {
  const pathname = usePathname();
  const last = useRef<string | null>(null);

  useEffect(() => {
    if (last.current === pathname) return;
    last.current = pathname;
    pageViewed(routeIdFor(pathname));
  }, [pathname]);
}
