/**
 * PostHog, and the exact boundary it is allowed to see.
 *
 * WHAT THIS IS FOR. The beacon in lib/beacon.ts answers "how many people landed
 * on which shape of page, from which channel". It cannot answer "where did
 * somebody give up", because it deliberately sends no URL, no element and no
 * ordering. Session replay and autocapture answer that, and they answer it by
 * recording a great deal more, so the whole of this file is the fence around
 * what "more" means.
 *
 * WHAT IS CAPTURED, IN FULL. The list below is the one the subprocessor page,
 * the legal page and docs/src/content/docs/security/data-boundary.md all
 * describe, and it is written here beside the configuration that produces it so
 * the two cannot drift.
 *
 *   the page address including its path and query string, and the referrer
 *   a page view on first load and on every client side route change
 *   autocaptured interactions: clicks, taps, and form submissions, with the
 *     element's tag, its css classes and ids, and its visible label text
 *   a session recording: the structure and styling of the page, cursor
 *     movement, clicks, scrolls, and the shape of every form field with its
 *     value replaced by asterisks
 *   browser, operating system, device type, screen size, and the country
 *     PostHog derives from the connecting address at ingest
 *   an anonymous identifier that lives in sessionStorage for one tab
 *
 * WHAT IS MASKED OR NEVER SENT.
 *
 *   EVERY INPUT VALUE. maskAllInputs is set explicitly rather than left to the
 *     library default, and maskInputOptions then names every input type the
 *     recorder knows about, including password and textarea and select. The
 *     careers form and the enterprise contact form are the reason: a person
 *     types their name, their work email, their company and a free paragraph
 *     into them, and a recorder that keeps up with their typing would hold all
 *     four. What a recording shows is a field filling up with asterisks.
 *   ADVERTISING IDENTIFIERS in the URL, through mask_personal_data_properties,
 *     which replaces gclid, fbclid and the rest of that family.
 *   NO COOKIE, and no identifier that outlives the tab. See PERSISTENCE below.
 *   NOTHING AT ALL from a reader who asked not to be measured. See THE GATE.
 *
 * NEITHER FORM ECHOES A TYPED VALUE BACK AS PAGE TEXT, which is why masking
 * input values is enough and there is no text mask here. ApplicationForm.tsx
 * and EnterpriseForm.tsx both use uncontrolled inputs and both render a fixed
 * confirmation sentence, so no name and no address is ever rendered as text for
 * the recorder to read. If that changes, the element that renders it needs the
 * class `ph-no-capture`, and this paragraph needs to stop saying otherwise.
 *
 * PERSISTENCE, AND WHY IT IS NOT THE LIBRARY DEFAULT.
 *
 * posthog-js defaults to `localStorage+cookie`, which sets a cookie and keeps a
 * stable identifier for a year. Three sentences already published on this site
 * say there is no cookie and that nothing here can join two of your visits, and
 * this site shows no cookie banner. `sessionStorage` keeps both of those true:
 * no cookie is set, and the identifier dies with the tab, which is the same
 * lifetime the beacon's own session identifier already has.
 *
 * IT COSTS SOMETHING AND THE COST IS NOT HIDDEN. PostHog's unique user counts
 * become unique tabs, and returning visitor analysis is not available. That is
 * a deliberate trade of one number for a promise that was already in writing.
 * One constant below reverses it, and reversing it makes those three sentences
 * false, so the copy has to move in the same commit.
 */

import type { PostHog, PostHogConfig } from "posthog-js";
import { measurementStatus, onMeasurementChanged } from "./beacon";

/** The project API key. Public by design: it ships in browser JavaScript, it
 *  can only write events into one project, and it reads nothing back.
 *
 *  Defaulted to this project's own key rather than left empty, for the reason
 *  ENDPOINT in lib/beacon.ts gives about itself: a feature that stays off until
 *  somebody sets a variable is a feature that ships inert and looks finished.
 *  A fork sets NEXT_PUBLIC_POSTHOG_KEY to its own key, and setting it to the
 *  empty string turns all of this off for a build, including the network
 *  request that would have fetched the library. */
export const POSTHOG_KEY =
  process.env.NEXT_PUBLIC_POSTHOG_KEY ?? "phc_BXsb8vQVdiajf7uG9soRdsEwLtcE7tJWgdAfc4Vvoqau";

/**
 * Where the browser sends events.
 *
 * A path rather than a host, on purpose. The browser talks to this site's own
 * origin and a reverse proxy forwards it, so no request in a reader's network
 * log names a vendor and no content blocker's vendor list matches it. Set
 * NEXT_PUBLIC_POSTHOG_API_HOST to an absolute URL to bypass the proxy, which is
 * what a fork with no proxy of its own does.
 */
export const POSTHOG_API_HOST = process.env.NEXT_PUBLIC_POSTHOG_API_HOST ?? "/ingest";

/**
 * Where a link in the PostHog toolbar should point.
 *
 * Not the proxy. The proxy answers the ingest API and knows nothing about the
 * application, so without this every "view this in PostHog" link built by the
 * library would point at a path on this site that does not exist.
 */
export const POSTHOG_UI_HOST =
  process.env.NEXT_PUBLIC_POSTHOG_UI_HOST ?? "https://us.posthog.com";

/**
 * Where the session replay recorder bundle is fetched from.
 *
 * SEPARATE FROM THE INGEST HOST BECAUSE POSTHOG SERVES THEM SEPARATELY. Events
 * go to the regional ingest host and `/static/recorder.js` comes from an asset
 * host, so a proxy that forwards only the ingest paths still leaves the browser
 * fetching a script directly from a posthog.com address. That would be a vendor
 * request in a reader's network log, which is the thing the proxy exists to
 * prevent, and it would be invisible to anybody who only checked where the
 * events went.
 *
 * Empty means "derive it from POSTHOG_API_HOST", which is right when the proxy
 * forwards `/static/*` as well. Point it somewhere else when it does not.
 */
export const POSTHOG_ASSET_HOST = process.env.NEXT_PUBLIC_POSTHOG_ASSET_HOST ?? "";

/**
 * An absolute URL for a value that may be a path on this origin.
 *
 * posthog-js concatenates the host with a path, so a bare `/ingest` would work
 * by accident in a browser and not at all anywhere the base is not the page.
 * Resolving it here means one rule, testable without a browser.
 *
 * Returns null for the empty string, which is how "not configured" is spelled
 * for both the asset host and, through POSTHOG_KEY, the whole feature.
 */
export function resolveHost(value: string, origin: string): string | null {
  const trimmed = value.trim().replace(/\/+$/, "");
  if (!trimmed) return null;
  if (trimmed.startsWith("/")) return origin.replace(/\/+$/, "") + trimmed;
  return trimmed;
}

/**
 * The configuration, as a value, so a test can assert the masking without a
 * browser and without a network.
 *
 * Every privacy relevant option is written out even where it matches today's
 * library default. A default is a decision somebody else can change in a minor
 * version, and `maskAllInputs` defaulting to true today is not a reason for the
 * legal copy to depend on it staying true.
 *
 * TYPED AS THE LIBRARY'S OWN CONFIGURATION, not as a bag of strings. An option
 * name this library does not have is the way masking silently stops happening:
 * posthog-js ignores what it does not recognise, so `maskAllinputs` would build,
 * ship, record every keystroke, and leave the legal copy claiming otherwise.
 * `Partial<PostHogConfig>` makes that a compile error. `import type` is erased,
 * so this costs no bundle and does not defeat the dynamic import below.
 *
 * Null when there is no host to send to, which is a build somebody switched
 * off rather than a build to start a recorder for.
 */
export function posthogOptions(origin: string): Partial<PostHogConfig> | null {
  const apiHost = resolveHost(POSTHOG_API_HOST, origin);
  if (!apiHost) return null;
  const assetHost = resolveHost(POSTHOG_ASSET_HOST, origin);
  return {
    api_host: apiHost,
    ui_host: POSTHOG_UI_HOST,
    // Null is the library's own "derive it from api_host", so an unset asset
    // host is not a broken one.
    asset_host: assetHost,

    // Pinned rather than left to follow the library. `defaults` is a dated
    // bundle of behaviours and an unpinned one changes what is captured on an
    // npm update, which is a change to the published copy made by a lockfile.
    defaults: "2026-08-30",

    // ON, and asked for. This is the half that answers where somebody gave up.
    autocapture: true,
    capture_pageview: "history_change",
    capture_pageleave: true,

    // ON. The masking below is what makes it safe to have on.
    disable_session_recording: false,
    session_recording: {
      // THE LINE THE LEGAL COPY DEPENDS ON. Set explicitly, not inherited.
      maskAllInputs: true,
      // And again by type, so that a future edit setting maskAllInputs to false
      // does not silently unmask a name and an email. Password is named even
      // though this site has no password field, because the list reads as the
      // rule rather than as an inventory of today's forms.
      maskInputOptions: {
        color: true,
        date: true,
        "datetime-local": true,
        email: true,
        month: true,
        number: true,
        range: true,
        search: true,
        tel: true,
        text: true,
        time: true,
        url: true,
        week: true,
        textarea: true,
        select: true,
        password: true,
      },
      // The recorder can lift Schema.org JSON-LD out of the page as its own
      // event. What this site puts there is public structured data about the
      // site itself, so it would leak nothing, and it is off because the list
      // of what is captured is a promise and a shorter promise is easier to
      // keep true.
      captureJsonLd: false,
      // A cross origin iframe is somebody else's document. This site embeds a
      // booking widget on the contact page, and recording inside it would
      // record a third party's form.
      recordCrossOriginIframes: false,
    },

    // No cookie, and no identifier that outlives the tab. See PERSISTENCE at
    // the top of this file for what this costs and what it keeps true.
    persistence: "sessionStorage",
    // Belt and braces for the sentence "there is no cookie": the opt out flag
    // the library writes when measurement is switched off goes to localStorage
    // rather than to a cookie.
    opt_out_capturing_persistence_type: "localStorage",
    // Nobody signs in on this site, so no person profile is ever created and
    // every event is anonymous.
    person_profiles: "identified_only",

    // Do Not Track, honoured by the library as well as by the gate below. Two
    // independent mechanisms rather than one, because the gate is this
    // repository's code and this is the vendor's, and a reader who has asked
    // should not depend on ours being right.
    respect_dnt: true,
    // Advertising identifiers stripped out of the URL before it is sent.
    mask_personal_data_properties: true,

    // Nothing on this site uses these, and a feature that is off cannot capture
    // anything or fetch a script.
    disable_surveys: true,
    disable_web_experiments: true,
    capture_exceptions: false,
    opt_in_site_apps: false,
  };
}

/** The live client, once started. Null until then, and null forever for a
 *  reader who is not being measured. */
let client: PostHog | null = null;
let starting = false;
let subscribed = false;

/**
 * THE GATE.
 *
 * Nothing above runs until this says so, and this asks lib/beacon.ts rather
 * than asking the browser a second way. That matters twice over. The beacon
 * already honours Global Privacy Control, Do Not Track, the reader's stored
 * preference and an automated browser, and a second implementation of those
 * four rules is a second implementation that can disagree with the switch on
 * the privacy page. And posthog-js honours only one of the four natively, so
 * three of them are wired here or they are not honoured at all.
 *
 * IT REFUSES BEFORE THE LIBRARY IS FETCHED, NOT AFTER. The import below is
 * dynamic and it is inside the gate, so a reader who has opted out causes no
 * request for posthog-js, no recorder, and no snapshot of the page they are on.
 * A recorder that loads and is then told to stop has already captured the page,
 * which is the specific failure this shape avoids: opting out cannot be
 * something that happens one network round trip too late.
 */
export async function startProductAnalytics(): Promise<void> {
  if (typeof window === "undefined") return;
  if (client || starting) return;
  if (!POSTHOG_KEY) return;
  if (!measurementStatus().measuring) return;

  starting = true;
  try {
    const options = posthogOptions(window.location.origin);
    if (!options) return;
    const posthog = (await import("posthog-js")).default;
    posthog.init(POSTHOG_KEY, options);
    client = posthog;
  } catch {
    // A blocked or failed chunk is a page that is not measured. It is never a
    // page that breaks, which is the whole reason analytics is loaded this way.
  } finally {
    starting = false;
  }
}

/**
 * Stops everything, for a reader who switched measurement off part way through
 * a visit, INCLUDING WHAT WAS ALREADY CAPTURED AND NOT YET SENT.
 *
 * THE FAILURE THIS SHAPE EXISTS FOR, FOUND IN A BROWSER AND NOT IN A REVIEW.
 * `opt_out_capturing()` on its own is not enough and it looks like it is.
 * Pressing the switch produced no request, the page looked right, the flag was
 * written, and then navigating to the next page sent a 47KB `$snapshot` of the
 * page the reader had just objected to being recorded on, plus the
 * `$autocapture` event for the click on the switch itself. Both were flushed by
 * posthog-js's own unload handler out of buffers that opt out does not empty:
 * read `opt_out_capturing` in posthog-js and the line that discards the
 * recorder's buffer is inside a branch that only runs when `cookieless_mode` is
 * configured, which needs a project setting we do not have. So a control that
 * promises "anything captured and not yet sent is thrown away with it" was
 * sending the largest thing it had captured, one navigation later.
 *
 * Four calls, in this order, and each one is doing a different job.
 *
 * 1. DISCARD THE RECORDING BUFFER. `dispose({ discardBufferedEvents: true })`
 *    is the vendor's own discard, the same call its cookieless opt out path
 *    makes, so this is their intended way to drop a buffered recording rather
 *    than a reach into an internal.
 * 2. STOP RECORDING, so nothing refills what was just emptied.
 * 3. OPT OUT, which refuses every future capture and writes the flag.
 * 4. TURN BATCHING OFF, which is what stops the queued analytics events. Its
 *    unload handler flushes the request queue and the retry queue only when
 *    `request_batching` is on; with it off, the same handler has nothing to
 *    flush and the queued events die with the page. There is no public call
 *    that empties those queues, and `before_send` cannot help because it runs
 *    at capture time, before anything is queued.
 *
 * Every one of the four is wrapped on its own. A vendor that changes one of
 * these must not stop the other three from running.
 */
export function stopProductAnalytics(): void {
  if (!client) return;
  try {
    client.sessionRecording?.dispose({ discardBufferedEvents: true });
  } catch {
    // Recording may never have started, and a vendor that renamed this must
    // not prevent the opt out below.
  }
  try {
    client.stopSessionRecording();
  } catch {
    // Same.
  }
  try {
    client.opt_out_capturing();
  } catch {
    // Same.
  }
  try {
    client.set_config({ request_batching: false });
  } catch {
    // Same.
  }
}

/**
 * Follows the switch on the privacy page, in both directions.
 *
 * lib/beacon.ts announces every change to the decision, which is the only place
 * that decision is made, so this cannot go out of step with what the switch
 * says or with what the beacon does. Registering here rather than at module
 * scope keeps this file free of side effects on import, which is what lets a
 * test load it.
 *
 * Returns the way to stop following it, which is what a test uses so that its
 * subscription cannot outlive it and answer for the next one.
 */
export function watchMeasurement(): () => void {
  if (subscribed) return () => {};
  subscribed = true;
  return onMeasurementChanged((measuring) => {
    if (measuring) void startProductAnalytics();
    else stopProductAnalytics();
  });
}
