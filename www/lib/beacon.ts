/**
 * The site beacon: everything it sends, and everything it refuses to.
 *
 * SPLIT FROM analytics.ts SO THAT IT CAN BE TESTED. This file imports nothing
 * but lib/control-plane-routes.ts and lib/bots.ts, and the first of those
 * imports only lib/site.ts, so nothing on the path pulls in a framework and a
 * plain `node --test` can load it and drive the queue, the session rules and
 * the opt out against stub globals. analytics.ts holds the one React hook and
 * re-exports the rest, because next/navigation does not resolve outside the
 * bundler and importing it made every one of these rules untestable.
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
 * The user agent is READ here, by lib/bots.ts, and never sent. The only effect
 * a crawler's user agent has is that a request is not made.
 *
 * NO COOKIE. NO CROSS-VISIT IDENTIFIER.
 *
 * The session identifier lives in sessionStorage, which the browser deletes
 * when the tab closes. Two visits a day apart are two unrelated sessions and
 * nothing here can join them. That is a real loss of information and it is the
 * deliberate trade: persistent attribution is off until a consent record, a
 * retention decision and a deletion path exist, and shipping it first and
 * asking later is how a product ends up with data it cannot justify keeping.
 *
 * One value does outlive the tab, in localStorage, and it is the opposite of an
 * identifier: a single boolean saying this reader has asked not to be measured.
 * It is set only by the reader, it is never sent anywhere, and its whole effect
 * is to stop requests being made. See OPTOUT_KEY.
 *
 * IT TURNS ITSELF OFF WHEN ASKED.
 *
 * Global Privacy Control and Do Not Track are both honoured, so is the stored
 * preference above, and so is a missing endpoint. A reader who has expressed a
 * preference is not measured, which costs a percentage point of a count and is
 * the correct answer.
 */

import { controlPlaneUrl } from "./control-plane-routes";
import { looksAutomated } from "./bots";

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
  process.env.NEXT_PUBLIC_AF_ANALYTICS_ENDPOINT ?? controlPlaneUrl("site.events");

const SESSION_KEY = "af.session.v1";

/**
 * The reader's stored answer to being measured, and the only value here that
 * outlives the tab.
 *
 * In localStorage on purpose. A preference that has to be re-expressed on every
 * visit is not a preference, it is a nag, and the whole point of an opt out is
 * that it sticks. It holds the string "off" or nothing, it is written only by
 * setMeasurement below, and it is never read by anything that sends a request.
 *
 * It doubles as the exclusion for this team's own browsing. There is no other
 * way to do that first party and without a cross-visit identifier: excluding
 * traffic by address means the server learning addresses, and excluding it by a
 * cookie means a cookie. A person who works here opens the site once with
 * ?af-analytics=off and is out of the numbers from then on.
 */
const OPTOUT_KEY = "af.analytics.optout.v1";

/**
 * How long a session may be idle before the next event starts a new one.
 *
 * Thirty minutes, which is what posthog-js uses and what every analytics
 * product has converged on, so a number computed here is comparable to a number
 * computed anywhere else. It is also the number the dashboard states next to
 * any count of sessions, because a session count without its timeout beside it
 * is not a measurement of anything.
 *
 * WHAT THIS FIXES. Before it, the session was the TAB: sessionStorage holds a
 * value until the tab closes, so a tab left open over a weekend was one session
 * for the whole weekend, and the same reader coming back on Monday was still
 * inside it. That made a session count an undercount by an unknown factor,
 * which is worse than a wrong number because nobody can say which way it is
 * wrong. An idle timeout makes the identifier strictly shorter lived than it
 * was, so this is a privacy improvement as well as a correctness one.
 */
export const SESSION_IDLE_TIMEOUT_MS = 30 * 60 * 1000;

/**
 * The longest a session may run, however active it is.
 *
 * Twenty four hours, again matching posthog-js. Without it a reader who keeps a
 * tab busy has one identifier forever, which is the cross-visit identifier this
 * file promises not to have, arrived at by a different route.
 */
export const SESSION_MAX_LENGTH_MS = 24 * 60 * 60 * 1000;

/**
 * How long the queue waits before sending what it has.
 *
 * Three seconds, matching posthog-js's default flush interval. It is long
 * enough that a page view and the engagement that follows it travel in one
 * request, and short enough that a reader who leaves after four seconds has
 * already been counted.
 *
 * Nothing waits on this timer at the end of a visit: leaving the page flushes
 * immediately through sendBeacon. See flushOnUnload.
 */
const FLUSH_INTERVAL_MS = 3000;

/**
 * The most events one request may carry.
 *
 * Mirrors SITE_BEACON_MAX_BATCH in web/apps/api/src/analytics/beacon.ts, which
 * refuses a larger batch with 413. A queue longer than this is sent as several
 * requests rather than truncated, because truncation looks like success.
 */
const MAX_BATCH = 20;

/**
 * The most events the queue will hold before it starts dropping the oldest.
 *
 * A reader who is offline for an hour on a single-page application accumulates
 * events with nowhere to go. Unbounded, that is a memory leak in somebody
 * else's browser. Bounded, it is a bounded undercount, and the oldest events go
 * first because they are the ones closest to expiring anyway.
 */
const MAX_QUEUED = 100;

/**
 * How stale an event may be when it is finally sent.
 *
 * Mirrors DEFAULT_MAX_SKEW_MS in beacon.ts, which refuses anything dated more
 * than a day away. Dropping it here rather than sending it to be rejected is
 * the difference between a client that knows the contract and one that finds
 * out. It matters because a retry is the one thing that can make an event old:
 * everything else is sent within seconds of happening.
 */
const MAX_EVENT_AGE_MS = 24 * 60 * 60 * 1000;

/** The most times one request is retried before its events are given up on. */
const MAX_ATTEMPTS = 5;

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
  | "contact"
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
  if (path === "/contact") return "contact";
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

// ---------------------------------------------------------------------------
// Opting out
// ---------------------------------------------------------------------------

/**
 * Records, or clears, this reader's decision not to be measured.
 *
 * Exported so a control on the privacy page can call it, and so the query
 * switch below can. Writing "off" is what turns the beacon off for good on this
 * browser; clearing the key is what turns it back on, and clearing rather than
 * writing "on" is deliberate, because the absence of an opt out is not the
 * presence of a consent and this file should not be able to record one.
 */
export function setMeasurement(on: boolean): void {
  try {
    if (on) localStorage.removeItem(OPTOUT_KEY);
    else localStorage.setItem(OPTOUT_KEY, "off");
  } catch {
    // Site data disabled. The reader is then opted out for this tab through
    // storedOptOut returning false and nothing being remembered, which is the
    // wrong direction, so the query switch below also sets the in-memory flag.
  }
  // Assigned in BOTH directions. It was only ever set, so a reader who opted
  // out and then changed their mind cleared the stored preference and stayed
  // opted out for the rest of the visit, because this flag still said no. That
  // was invisible while the only way to opt back in was ?af-analytics=on, which
  // arrives on load before anything has set it. A control on the page is
  // pressed after, and it is the case where an opt in that does nothing looks
  // exactly like a broken switch.
  memoryOptOut = !on;
  // THE DECISION IS CACHED, SO CLEARING IT IS PART OF RECORDING THE CHOICE.
  //
  // measurementAllowed answers once per page and then remembers, because it
  // reads storage and a reader cannot become a crawler part way through a
  // visit. An opt out is the one thing that CAN change part way through a
  // visit, and this site navigates on the client, so the control that calls
  // this does not reload the module. Without this line the preference is
  // written, the control appears to work, and the beacon keeps sending until
  // the tab is closed.
  measuring = null;
  // And what was already captured goes with it. The queue holds events for up
  // to three seconds, so an opt out usually lands with unsent events in hand,
  // and sending them because they were captured a moment before the reader
  // objected is the disclosure the control was pressed to prevent.
  if (!on) discardCapture();
}

/** Set by the query switch when storage refused the write, so an opt out is at
 *  least honoured for the page the reader asked on. */
let memoryOptOut = false;

/** Whether the reader has previously asked not to be measured. */
function storedOptOut(): boolean {
  try {
    return localStorage.getItem(OPTOUT_KEY) === "off";
  } catch {
    return false;
  }
}

/**
 * `?af-analytics=off` and `?af-analytics=on`, read once per page.
 *
 * A link somebody can send to a colleague, which is what makes the team
 * exclusion practical: there is nothing to install and nothing to remember.
 */
function applyQuerySwitch(): void {
  try {
    const value = new URLSearchParams(location.search).get("af-analytics");
    if (value === "off") setMeasurement(false);
    else if (value === "on") setMeasurement(true);
  } catch {
    // A URL that will not parse is not a preference.
  }
}

/**
 * Whether the BROWSER asks not to be tracked, through either of the two
 * signals that exist for saying so.
 *
 * Its own function rather than four lines inside measurementOff, because the
 * control on the privacy page has to tell these two cases apart. A reader whose browser
 * sends Global Privacy Control is not measured whatever the site switch says,
 * and a control that renders "off" without saying why invites them to turn it
 * on, watch nothing change, and conclude the switch is decoration.
 */
function browserAskedNotToBeTracked(): boolean {
  if (typeof navigator === "undefined") return false;
  const nav = navigator as Navigator & { globalPrivacyControl?: boolean; doNotTrack?: string };
  if (nav.globalPrivacyControl === true) return true;
  if (nav.doNotTrack === "1") return true;
  // Safari and older Firefox put it on window rather than on navigator.
  const legacy = (window as unknown as { doNotTrack?: string }).doNotTrack;
  return legacy === "1" || legacy === "yes";
}

/**
 * Why this reader is not being measured, when they are not.
 *
 * A closed set rather than a boolean, because the control on the privacy page
 * has to say which of these it is and they do not behave alike. `browser` and
 * `build` are not the reader's decision and the switch cannot change either, so
 * a control that renders a bare "off" for all four invites somebody to press it,
 * watch nothing happen, and conclude the switch is decoration.
 */
export type MeasurementOff =
  /** This reader stored a preference not to be measured, in this browser. */
  | "reader"
  /** The browser asks not to be tracked, through GPC or Do Not Track. */
  | "browser"
  /** This browser is a crawler or is being driven by a test. */
  | "automated"
  /** This build has no endpoint, so nothing anywhere is counting. */
  | "build";

/** Whether this reader is being measured, and why not when they are not. */
export interface MeasurementStatus {
  measuring: boolean;
  off: MeasurementOff | null;
}

/**
 * The one predicate, which measurementAllowed below is defined in terms of.
 *
 * Written as the reason rather than as a boolean so that there is no second
 * copy of the rules to answer the control with. A status that computed the same
 * question a second way is a status that can disagree with the producers, and a
 * privacy control that disagrees with what is on the wire is worse than none.
 *
 * `browser` is reported ahead of `reader` deliberately. Both can be true at
 * once, and the one that decides whether the switch can do anything is the
 * browser's.
 */
function measurementOff(): MeasurementOff | null {
  if (typeof window === "undefined" || !ENDPOINT) return "build";
  if (browserAskedNotToBeTracked()) return "browser";
  if (memoryOptOut || storedOptOut()) return "reader";
  if (looksAutomated(navigator)) return "automated";
  return null;
}

/**
 * Reads the state above, through the same cache the producers read.
 *
 * Which means it runs the query switch exactly as an event would, and that is
 * right: a reader who arrived on an opt out link and then opened this page
 * should see the link's effect rather than the state before it.
 */
export function measurementStatus(): MeasurementStatus {
  const measuring = measurementAllowed();
  return { measuring, off: measuring ? null : measurementOff() };
}

/**
 * Whether this page should be measured at all, decided once and then cached.
 *
 * Cached because it is asked on every event and two of its three checks read
 * storage, and because a reader cannot become a crawler part way through a
 * visit. The query switch runs before the answer is computed, so a reader who
 * arrives on an opt out link is out from their first page view rather than from
 * their second.
 */
let measuring: boolean | null = null;

function measurementAllowed(): boolean {
  if (measuring !== null) return measuring;
  if (typeof window === "undefined" || !ENDPOINT) {
    measuring = false;
    return false;
  }
  applyQuerySwitch();
  measuring = measurementOff() === null;
  return measuring;
}

// ---------------------------------------------------------------------------
// The session
// ---------------------------------------------------------------------------

interface Attribution {
  source: VisitSource;
  landing: SiteRoute;
  campaign: string | null;
}

/** Exported so a test can build one and ask sessionEnded about it directly.
 *  The rules that end a session are the ones most worth checking against a
 *  clock a test controls rather than against one it has to wait for. */
export interface StoredSession {
  id: string;
  /** When this session's first event happened, for the maximum length rule. */
  startedAt: number;
  /** When its most recent event happened, for the idle rule. */
  lastSeenAt: number;
  attribution: Attribution;
}

/**
 * The session held in memory, for a browser that will not let this file use
 * sessionStorage at all.
 *
 * Private browsing and a site data block both throw on the ACCESSOR rather than
 * returning null, and the previous version of this file returned early on that
 * throw, which meant every reader in that state was invisible. That is not a
 * conservative failure, it is a silent one: the counts were short by an unknown
 * amount and nothing said so. An in-memory session is the shortest lived
 * identifier this file can produce, so falling back to it costs no privacy and
 * turns a total loss into a per-page-load count.
 */
let memorySession: StoredSession | null = null;

function readSession(): StoredSession | null {
  try {
    const raw = sessionStorage.getItem(SESSION_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as unknown;
    return validSession(parsed) ? parsed : null;
  } catch {
    return memorySession;
  }
}

function writeSession(session: StoredSession): void {
  memorySession = session;
  try {
    sessionStorage.setItem(SESSION_KEY, JSON.stringify(session));
  } catch {
    // Held in memory instead, above. A new page load then starts a new session,
    // which counts per page rather than per visit and beats refusing to render.
  }
}

/**
 * Whether what came out of storage is the shape this file wrote.
 *
 * Checked field by field rather than trusted, because sessionStorage is shared
 * with everything else this origin runs and with every previous version of this
 * file. A record written by an older version, or by a bug, would otherwise be
 * spread into an event and rejected by the catalog one field at a time. One bad
 * record is skipped here and a fresh session replaces it, which is the same
 * rule the recorder applies to a batch: a malformed item never discards
 * anything but itself.
 */
function validSession(value: unknown): value is StoredSession {
  if (!value || typeof value !== "object") return false;
  const s = value as Partial<StoredSession>;
  if (typeof s.id !== "string" || s.id.length < 8 || s.id.length > 64) return false;
  if (typeof s.startedAt !== "number" || !Number.isFinite(s.startedAt)) return false;
  if (typeof s.lastSeenAt !== "number" || !Number.isFinite(s.lastSeenAt)) return false;
  const a = s.attribution;
  if (!a || typeof a !== "object") return false;
  if (typeof a.source !== "string" || typeof a.landing !== "string") return false;
  if (a.campaign !== null && typeof a.campaign !== "string") return false;
  return true;
}

/**
 * Why a session ended, which is only ever read by a test.
 *
 * Named rather than boolean because the two reasons behave differently and a
 * test that could not tell them apart would pass on either. `idle` is the
 * common one; `expired` is the twenty four hour cap, which fires on a tab
 * somebody never closes.
 */
export type SessionEnd = "idle" | "expired";

/** Whether a stored session has ended by the time of this event, and why. */
export function sessionEnded(session: StoredSession, at: number): SessionEnd | null {
  // Absolute value on the idle comparison, so a clock that jumped backwards
  // ends the session rather than extending it indefinitely. A negative idle is
  // not evidence of activity.
  if (Math.abs(at - session.lastSeenAt) > SESSION_IDLE_TIMEOUT_MS) return "idle";
  if (Math.abs(at - session.startedAt) > SESSION_MAX_LENGTH_MS) return "expired";
  return null;
}

/**
 * The identifier for this browsing session, and the channel it started on.
 *
 * Attribution is the FIRST page's channel, held for the length of the session,
 * because "what brought them here" is a property of the arrival and every page
 * after it has an internal referrer.
 *
 * A session that has ended is REPLACED rather than extended, and its
 * replacement runs attribution again from the referrer that is there now. That
 * is the honest answer: a reader who comes back to an open tab an hour later
 * arrived from wherever they arrived from, and carrying the morning's channel
 * onto the afternoon's visit would attribute a second visit to a first one.
 */
function session(at: number): { id: string; attribution: Attribution; entry: boolean } {
  const stored = readSession();
  if (stored && !sessionEnded(stored, at)) {
    writeSession({ ...stored, lastSeenAt: at });
    return { id: stored.id, attribution: stored.attribution, entry: false };
  }

  // A campaign link is its own channel. Somebody arriving through ?c=launch-2026
  // came from the campaign, whatever page happened to host the link, and
  // reporting them as `email` or `social` would put the campaign's readers in
  // the same bar as everybody else who came from there.
  const campaign = campaignFor(location.search);
  const fresh: StoredSession = {
    id: randomId(),
    startedAt: at,
    lastSeenAt: at,
    attribution: {
      source: campaign ? "campaign" : sourceFor(document.referrer, location.href),
      landing: routeIdFor(location.pathname),
      campaign,
    },
  };
  writeSession(fresh);
  return { id: fresh.id, attribution: fresh.attribution, entry: true };
}

/** 128 bits of randomness, from the browser's own generator. */
function randomId(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

// ---------------------------------------------------------------------------
// The queue
// ---------------------------------------------------------------------------

interface Wire {
  id: string;
  name: string;
  at: string;
  session: string;
  payload: Record<string, string | boolean>;
}

/**
 * Events waiting to be sent, and the timer that will send them.
 *
 * WHY A QUEUE AT ALL, WHEN THE PREVIOUS VERSION SENT EACH EVENT AS IT HAPPENED.
 *
 * Because the endpoint, the wire format and the recorder were all built to take
 * a batch and nothing ever sent one. A page produces a view and then, often
 * within a second, an engagement; those were two requests, two preflights and
 * two round trips for four hundred bytes. Worse, a request that failed took its
 * event with it, so a reader on a flaky connection was simply not counted, and
 * nothing anywhere said how many.
 *
 * WHY A RETRY IS SAFE HERE, WHICH IS NOT OBVIOUS AND IS THE WHOLE ARGUMENT.
 *
 * A retried page view is normally a bug: a tab that was asleep wakes up and
 * posts a hundred stale events, and the counts move for a reason nobody can
 * trace. Two properties this system already has make it safe anyway.
 *
 * First, an event is stamped ONCE, with its id and its time, at the moment it
 * happens. A retry resends both unchanged, so the second copy collides with the
 * first on the primary key and is counted as a duplicate rather than added. The
 * recorder reports that count separately, so a retry storm is visible as
 * duplicates rather than as growth.
 *
 * Second, the server refuses anything dated more than a day out, and this queue
 * drops such an event before sending it. So the worst a sleeping tab can do is
 * deliver a day-old event once.
 *
 * WHAT IS NEVER RETRIED. Anything the server answered with a 4xx. A 400 means
 * the envelope was wrong and a 207 means the catalog refused some part of the
 * payload, and neither of those becomes true on the third attempt. Retrying
 * them is how a client turns its own bug into a load test.
 */
let queue: Wire[] = [];
let flushTimer: ReturnType<typeof setTimeout> | null = null;
let attempts = 0;
/** Set when the server says it is not recording, so the page stops asking. */
let stopped = false;
let listenersAttached = false;

function enqueue(event: Wire): void {
  queue.push(event);
  if (queue.length > MAX_QUEUED) queue = queue.slice(queue.length - MAX_QUEUED);
  attachListeners();
  scheduleFlush(FLUSH_INTERVAL_MS);
}

/**
 * Throws away everything captured and not yet sent, and cancels the flush.
 *
 * Declared beside the queue rather than beside setMeasurement because it is the
 * queue's own invariant. A function declaration rather than an expression, so
 * that setMeasurement, which is defined above the queue, can call it.
 */
function discardCapture(): void {
  queue = [];
  if (flushTimer !== null) {
    clearTimeout(flushTimer);
    flushTimer = null;
  }
  attempts = 0;
}

function scheduleFlush(afterMs: number): void {
  if (flushTimer !== null || stopped) return;
  flushTimer = setTimeout(() => {
    flushTimer = null;
    void flush();
  }, afterMs);
}

/**
 * Drops events the server would refuse for being too old.
 *
 * Run at send time rather than at enqueue time, because an event is only ever
 * old by the time it is sent: this is the retry path's rule, not the producer's.
 */
function fresh(events: Wire[], now: number): Wire[] {
  return events.filter((e) => {
    const at = Date.parse(e.at);
    return Number.isFinite(at) && Math.abs(now - at) <= MAX_EVENT_AGE_MS;
  });
}

/**
 * Sends what is queued, in batches the endpoint will accept.
 *
 * Everything is taken off the queue first, so an event produced while a request
 * is in flight waits for the next flush rather than being sent twice. A batch
 * that fails goes back on the FRONT of the queue, because it is older than
 * anything produced since.
 */
async function flush(): Promise<void> {
  if (stopped || queue.length === 0) return;
  const now = Date.now();
  const pending = fresh(queue, now);
  queue = [];
  if (pending.length === 0) {
    attempts = 0;
    return;
  }

  const failed: Wire[] = [];
  for (let i = 0; i < pending.length; i += MAX_BATCH) {
    const batch = pending.slice(i, i + MAX_BATCH);
    const outcome = await post(batch);
    if (outcome === "stop") {
      stopped = true;
      queue = [];
      return;
    }
    if (outcome === "retry") failed.push(...batch);
  }

  if (failed.length === 0) {
    attempts = 0;
    return;
  }

  attempts += 1;
  if (attempts >= MAX_ATTEMPTS) {
    // Given up on deliberately rather than retried forever. The events are
    // dropped, the counts are short by that many, and the alternative is a
    // browser tab that never stops asking a server that is not answering.
    attempts = 0;
    return;
  }
  queue = [...failed, ...queue].slice(0, MAX_QUEUED);
  scheduleFlush(retryDelay(attempts - 1));
}

/**
 * How long to wait before the next attempt.
 *
 * Exponential from three seconds with plus or minus fifty percent of jitter,
 * which is the shape posthog-js uses, capped at sixty seconds rather than their
 * thirty minutes. The cap is different because the situations are: their queue
 * survives in a long-lived application, and this one lives in a page a reader
 * is about to leave. A backoff longer than a visit is a backoff that never
 * fires, and the unload flush below is what covers the rest.
 *
 * Jitter, so that a control plane coming back up is not hit by every open tab
 * on the same tick.
 */
export function retryDelay(attemptsSoFar: number): number {
  const raw = 3000 * 2 ** attemptsSoFar;
  const capped = Math.min(60_000, raw);
  const jitter = (Math.random() - 0.5) * capped;
  return Math.ceil(capped + jitter);
}

type Outcome = "sent" | "retry" | "stop";

/**
 * One request, and what its answer means for the events in it.
 *
 * A 2xx is done, including 207: a partial rejection means the catalog refused
 * something, which the control plane counts and which resending cannot fix. Any
 * other 4xx is the same judgement for the same reason. A 5xx or a network
 * failure is worth another attempt. A 503 means the control plane has no
 * analytics configured, which will not change while this page is open, so the
 * page stops rather than spending five attempts learning it again.
 */
async function post(events: Wire[]): Promise<Outcome> {
  try {
    const response = await fetch(ENDPOINT, {
      method: "POST",
      keepalive: true,
      // No cookie, ever. Said in the request rather than left to the default, so
      // that a change here is a change somebody has to write down.
      credentials: "omit",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ events }),
    });
    if (response.status === 503) return "stop";
    if (response.status >= 500) return "retry";
    return "sent";
  } catch {
    // A network failure, a blocked request or a cancelled navigation. The
    // reader's page is not made worse by a measurement failing.
    return "retry";
  }
}

/**
 * Sends what is queued as the reader leaves, without waiting for anything.
 *
 * sendBeacon rather than fetch, because a fetch started during pagehide is
 * cancelled by some browsers even with keepalive, and sendBeacon is the API
 * that exists for exactly this. Its body is text/plain, which is not a mistake:
 * sendBeacon cannot answer a CORS preflight, and application/json would force
 * one, so the request would simply never be made. The control plane parses the
 * body as JSON regardless of what the header says, and a test holds it to that.
 *
 * A batch too large for one beacon is sent as several. Nothing is retried here,
 * because there is no page left to retry from.
 */
function flushOnUnload(): void {
  if (stopped || queue.length === 0) return;
  const pending = fresh(queue, Date.now());
  queue = [];
  for (let i = 0; i < pending.length; i += MAX_BATCH) {
    const body = JSON.stringify({ events: pending.slice(i, i + MAX_BATCH) });
    try {
      const blob = new Blob([body], { type: "text/plain;charset=UTF-8" });
      if (!navigator.sendBeacon(ENDPOINT, blob)) {
        // The browser refused to queue it, usually because the payload is over
        // its limit. Nothing to do about it from a page that is closing.
        return;
      }
    } catch {
      return;
    }
  }
}

/**
 * The two moments a visit can end, and why both are listened for.
 *
 * `visibilitychange` to hidden is the one that fires reliably on mobile, where
 * a reader switches applications and the tab is frozen without ever unloading.
 * `pagehide` is the one that fires on a real navigation away. Neither on its
 * own covers both, and listening for `beforeunload` instead of these is what
 * makes a page ineligible for the back-forward cache, which is a real cost to a
 * reader paid for a number.
 *
 * Attached lazily, on the first event, so a page that records nothing adds no
 * listeners at all.
 */
function attachListeners(): void {
  if (listenersAttached || typeof document === "undefined") return;
  listenersAttached = true;
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") flushOnUnload();
  });
  window.addEventListener("pagehide", flushOnUnload);
  // A reader who came back online is worth an immediate attempt rather than
  // the rest of whatever backoff was in flight, and the backoff at that moment
  // can be nearly a minute. So the pending timer is CANCELLED rather than
  // scheduled alongside: scheduleFlush does nothing while one is already armed,
  // so without the cancel this listener was a line that ran and changed
  // nothing, which a test caught by watching the queue sit there.
  //
  // The attempt count resets too. Those failures were the network being away,
  // which is now over, and carrying them forward would spend the budget for
  // failures that have not happened yet.
  window.addEventListener("online", () => {
    if (stopped || queue.length === 0) return;
    if (flushTimer !== null) {
      clearTimeout(flushTimer);
      flushTimer = null;
    }
    attempts = 0;
    scheduleFlush(0);
  });
}

/** Queues one event, if this page is being measured at all. */
function emit(name: string, payload: Record<string, string | boolean>): void {
  if (!measurementAllowed()) return;
  const now = Date.now();
  const s = session(now);
  enqueue({
    id: randomId(),
    name,
    at: new Date(now).toISOString(),
    session: s.id,
    payload,
  });
}

// ---------------------------------------------------------------------------
// The producers
// ---------------------------------------------------------------------------

/**
 * One page view.
 *
 * `entry` is what separates the arrival from the pages after it. Attribution is
 * read off the arrival, so a chart of "which channel landed on which page" is
 * about where people come in, and the pages after it are counted as internal
 * rather than as new arrivals from nowhere.
 */
export function pageViewed(route: SiteRoute): void {
  if (!measurementAllowed()) return;
  const now = Date.now();
  const s = session(now);
  const payload: Record<string, string | boolean> = {
    route,
    source: s.entry ? s.attribution.source : "internal",
    entry: s.entry,
  };
  if (s.entry && s.attribution.campaign) payload.campaign = s.attribution.campaign;
  enqueue({
    id: randomId(),
    name: "site.page_viewed",
    at: new Date(now).toISOString(),
    session: s.id,
    payload,
  });
}

/** A call to action was pressed. */
export function ctaEngaged(cta: Cta): void {
  emit("site.cta_engaged", { cta, route: routeIdFor(location.pathname) });
}

/**
 * Somebody asked to be contacted, carrying the channel the SESSION started on.
 *
 * That is the whole point of this event and it is why attribution is held for
 * the session rather than read per page. A visitor arrives from a search
 * result, reads three pages, and submits on the pricing page; taking the
 * referrer at submission time would attribute every one of those to
 * `internal`, and the acquisition funnel would report that nothing works.
 *
 * `notified` means the deployment had a mailer and somebody was told;
 * `recorded` means the lead is in the database and a person reads the queue.
 * The form renders a different sentence for each, so the event says which too.
 */
export function leadSubmitted(outcome: "notified" | "recorded" | "refused"): void {
  if (!measurementAllowed()) return;
  const now = Date.now();
  const s = session(now);
  const payload: Record<string, string | boolean> = {
    source: s.attribution.source,
    landing: s.attribution.landing,
    outcome,
  };
  if (s.attribution.campaign) payload.campaign = s.attribution.campaign;
  enqueue({
    id: randomId(),
    name: "site.lead_submitted",
    at: new Date(now).toISOString(),
    session: s.id,
    payload,
  });
}
