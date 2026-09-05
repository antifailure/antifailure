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
 *   the page address including its path and query string, its title, and the
 *     referrer
 *   a page view on first load and on every client side route change, and how
 *     far down each page the reader got before leaving it
 *   autocaptured interactions: clicks, taps, and form submissions, with the
 *     element's tag, its css classes and ids, and its visible label text
 *   a session recording: the structure and styling of the page, cursor
 *     movement, clicks, scrolls, and the shape of every form field with its
 *     value replaced by asterisks
 *   browser, operating system, device type, screen and viewport size, browser
 *     language, and timezone
 *   an anonymous identifier that lives in sessionStorage for one tab
 *
 * THAT LIST WAS SHORT BY FOUR THINGS UNTIL SOMEBODY READ THE WIRE. Title,
 * scroll depth, language and timezone are all sent by posthog-js on properties
 * nothing in this file mentions, so a list written from the configuration alone
 * was wrong on the day it was written. It is written from a decoded payload
 * now. Two whole event types were missing the same way: see capture_heatmaps
 * and capture_performance below, which the project's remote config had on.
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
 *   THE READER'S IP ADDRESS, which the proxy does not forward. That is the
 *     proxy's doing rather than this file's, and it is the one place the
 *     arrangement genuinely withholds something from the vendor rather than
 *     just moving where the request goes. The cost is that PostHog's $geoip
 *     properties describe our datacenter and not the reader, so any geography
 *     on a PostHog dashboard is meaningless and should be read that way.
 *   NO COOKIE, and no identifier that outlives the tab. See PERSISTENCE below.
 *   NOTHING AT ALL from a reader who asked not to be measured. See THE GATE.
 *
 * THE PROXY IS TRANSPORT AND IT IS NOT A BOUNDARY. SAY SO, EVERY TIME.
 *
 * Requests go to an endpoint this project runs on its own domain and are
 * forwarded to PostHog. That changes the destination the BROWSER connects to.
 * It does not change WHO RECEIVES THE DATA: PostHog, Inc. receives every event,
 * every autocaptured interaction and every session recording either way. So the
 * proxy buys three real things, and nothing else. A content blocker's vendor
 * list does not match it, so the measurement is not silently half missing. The
 * recorder bundle, which is the largest and most blockable request posthog-js
 * makes, arrives rather than failing while ingest looks healthy. And the reader
 * 's address is dropped on the way through.
 *
 * WHAT IT DOES NOT BUY IS THE SENTENCE "no third party sees this". A network
 * tab that shows no vendor host would make that sentence look verified while it
 * was false, which is worse than the unproxied version, because the arrangement
 * it hides is the one a security review is asking about. PostHog is on the
 * subprocessor list with a row of its own for that reason.
 *
 * IT IS SAME SITE, NOT SAME ORIGIN, AND THE DIFFERENCE IS NOT PEDANTRY HERE.
 * This site is a static export on Azure Static Web Apps with no server at
 * runtime, and a staticwebapp.config.json route cannot rewrite to an external
 * host, so the site itself cannot proxy anything. The proxy lives on the
 * control plane at app.antifailure.dev, which is a DIFFERENT ORIGIN from
 * antifailure.dev and www.antifailure.dev and the same registrable domain. This
 * repository has already spent hours on three people calling something cross
 * site when SameSite=Strict had made it same site only. Copy that says "same
 * origin" about this is false. "An endpoint we run on our own domain" is true.
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
 * Where the browser sends events, and where it fetches the recorder from.
 *
 * ONE BASE FOR BOTH, WHICH IS WHY THERE IS NO SEPARATE ASSET HOST HERE.
 * posthog-js classifies a custom api_host as region "custom" and then routes
 * capture, feature flags, the remote config AND the script bundles at that one
 * base, and the proxy serves /static and /array for that reason. There is
 * deliberately no second host here for the bundles: see the options below.
 *
 * WRITTEN AS A LITERAL RATHER THAN BUILT FROM CONTROL_PLANE_URL, and that is a
 * gate constraint rather than a preference. tools/routecheck refuses any file
 * in www that names CONTROL_PLANE_URL outside the inventory, so the alternative
 * is an entry in www/lib/control-plane-routes.ts. That entry would fail today:
 * routecheck checks every declared route against web/apps/api/src/boundary.ts,
 * which does not register this path yet, and then probes the DEPLOYED control
 * plane, which moves on the tag clock and is several releases behind the proxy.
 *
 * SO WRITE DOWN WHAT THAT COSTS. This is now a call from the site to the
 * control plane that routecheck cannot see, which is exactly the class of call
 * that inventory exists to make visible, and the beacon's own entry there says
 * why it matters more here than for a form: "no reader sees an error". When the
 * proxy is in a released control plane, this belongs in the inventory.
 */
export const POSTHOG_API_HOST =
  process.env.NEXT_PUBLIC_POSTHOG_API_HOST ?? "https://app.antifailure.dev/ph";

/**
 * Where a link in the PostHog toolbar should point.
 *
 * Not the proxy. The proxy answers the ingest API and knows nothing about the
 * application, so without this every "view this in PostHog" link built by the
 * library would point at a path that does not exist.
 *
 * THIS IS THE ONE PLACE A VENDOR HOST BELONGS IN THIS TREE. It is a link a
 * person clicks and the browser never fetches it, which is what separates it
 * from an ingestion or asset host. One of those appearing anywhere else in www
 * is a defect, and test/posthog.test.ts is the check that names them.
 */
export const POSTHOG_UI_HOST: string =
  process.env.NEXT_PUBLIC_POSTHOG_UI_HOST ?? "https://us.posthog.com"; // ui_host

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
  return {
    api_host: apiHost,
    // NO SEPARATE HOST FOR THE SCRIPT BUNDLES, AND ITS ABSENCE IS THE POINT.
    // posthog-js routes /static and /array at a custom api_host on its own,
    // measured on the wire: with nothing set, the recorder arrives through the
    // proxy. An option here would be redundant AND would be the single edit
    // that puts the largest and most blockable request posthog-js makes back on
    // a vendor address, silently, with ingest still healthy and every gate that
    // tests api_host still green. This one is enforced by absence rather than
    // by a value, and the test that says so scans the shipped source for the
    // option name and for the environment variable that would feed it.
    ui_host: POSTHOG_UI_HOST,

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

    // OFF, AND THEY WERE ON, WHICH IS THE POINT OF WATCHING THE WIRE RATHER
    // THAN READING THE CONFIGURATION. Neither of these appears anywhere in this
    // file's options, and one visit produced two `$$heatmap` events and three
    // `$web_vitals` events anyway, because the project's REMOTE CONFIG turns
    // them on and remote config beats an option nobody set. Heatmaps carry
    // click coordinates and web vitals carry page timings, so the published
    // list of what is captured was short by two event types the moment it was
    // written. Nobody asked for either. Turned off here rather than added to
    // the copy, because a promise this file can keep is better than a longer
    // one it has to track.
    capture_heatmaps: false,
    capture_performance: false,

    // THE READER'S USER AGENT STRING, WHICH THIS SITE HAS PUBLISHED A PROMISE
    // ABOUT. lib/bots.ts says the user agent is read in the page and never put
    // on the network, and that is a real property of the beacon and the reason
    // its crawler filter runs in the browser at all. posthog-js attaches
    // `$raw_user_agent` to every event, so leaving it would have quietly
    // falsified a claim made somewhere else in the tree. `$browser`, `$os` and
    // `$device_type` are derived from it and are kept: they are the part that
    // answers a question, and none of them is the string itself.
    property_denylist: ["$raw_user_agent"],
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
