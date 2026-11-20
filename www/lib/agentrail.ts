/**
 * Agentrail's browser recorder, behind the same decision as everything else
 * this site measures.
 *
 * WHAT IT IS FOR. Agentrail follows an AI agent from the first page it reads to
 * the account it signs in to and the MCP calls it makes, so we can see where
 * agents that try Antifailure get stuck. The control plane half lives in
 * web/apps/api/src/analytics/agentrail.ts; this is the page half.
 *
 * WHAT IT SENDS, from the vendor's own description of agentrail-browser 0.4.1:
 * a page view (host, path, time, user agent, a session id), the method, path,
 * status and duration of this page's calls to its own origin, and WebMCP tool
 * names with their outcome and sizes. Not what is typed, pasted or chosen, not
 * the page's content, and not the query string. Behaviour timing, the browser
 * fingerprint and WebMCP payloads are all off and stay off here.
 *
 * WHAT IT SETS. One first party cookie, `agentrail_session`, holding a random
 * id that ends after thirty minutes idle or a day after it began. That is the
 * one cookie on this site, and the privacy page says so.
 *
 * BUNDLED, NOT A TAG. The vendor offers a script tag on its own host. We bundle
 * the package instead so no third party script runs on our pages, and so the
 * import can sit inside the gate: a reader who has opted out, or whose browser
 * sends Global Privacy Control or Do Not Track, fetches nothing and gets no
 * cookie. The events themselves do go to Agentrail's collector, which is the
 * part a bundle cannot change.
 *
 * NO FIRST PARTY API ORIGINS. The library can add a `traceparent` header to a
 * page's calls to other origins we name, to join them to the journey. The only
 * other origin these pages call is the control plane, and its CORS answer does
 * not allow that header, so naming it would turn the beacon and the demo form
 * into failed preflights. The sign-in step joins the journey on the server
 * instead.
 */
import { isWebsitePreview, measurementStatus, onMeasurementChanged } from "@/lib/beacon";

/** Public by design: it can only send this source's page events. A fork sets
 *  NEXT_PUBLIC_AGENTRAIL_KEY to its own key, or to the empty string for none. */
export const AGENTRAIL_KEY =
  process.env.NEXT_PUBLIC_AGENTRAIL_KEY ?? "ajp_sLcguRY1LQBsgV5UtbugTX9FoTs2xnXbQnmI66kaM0Q";

export const AGENTRAIL_COLLECTOR =
  process.env.NEXT_PUBLIC_AGENTRAIL_COLLECTOR ?? "https://api.agentrail.co/ingest/browser";

export const AGENTRAIL_COOKIE = "agentrail_session";

interface Recorder {
  stop(): void;
}

let recorder: Recorder | null = null;
let starting = false;
let subscribed = false;

function previewContentMounted(): boolean {
  return typeof document !== "undefined" && typeof document.querySelector === "function" &&
    document.querySelector(".af-cms-preview") !== null;
}

/** Whether this page may start the recorder right now. */
export function agentrailAllowed(): boolean {
  if (!AGENTRAIL_KEY || !AGENTRAIL_COLLECTOR) return false;
  if (!measurementStatus().measuring) return false;
  return !isWebsitePreview() && !previewContentMounted();
}

export async function startAgentrail(): Promise<void> {
  if (typeof window === "undefined") return;
  if (!agentrailAllowed()) {
    // A draft preview is a page nobody published. Stop rather than record it.
    if (recorder && (isWebsitePreview() || previewContentMounted())) stopAgentrail();
    return;
  }
  if (recorder || starting) return;
  starting = true;
  try {
    const { start } = await import("agentrail-browser");
    // The decision may have changed while the chunk loaded. The first check
    // cannot authorize this later start.
    if (!agentrailAllowed()) return;
    recorder = start({
      key: AGENTRAIL_KEY,
      collector: AGENTRAIL_COLLECTOR,
      sessionCookie: AGENTRAIL_COOKIE,
      behaviour: false,
      fingerprint: false,
      onError: () => {
        // A vendor that is down is a page that is not measured, never a page
        // that breaks.
      },
    });
  } catch {
    // A blocked or failed chunk is the same: unmeasured, not broken.
  } finally {
    starting = false;
  }
}

/** Stops the recorder and removes its cookie, for a reader who switched
 *  measurement off part way through a visit. */
export function stopAgentrail(): void {
  try {
    recorder?.stop();
  } catch {
    // A vendor that changed stop() must not keep the cookie alive below.
  }
  recorder = null;
  if (typeof document !== "undefined") {
    document.cookie = `${AGENTRAIL_COOKIE}=; Max-Age=0; Path=/; SameSite=Lax`;
  }
}

/** Follows the switch on the privacy page in both directions. Returns the way
 *  to stop following it, which is what a test uses. */
export function watchAgentrail(): () => void {
  if (subscribed) return () => {};
  subscribed = true;
  const unsubscribe = onMeasurementChanged((measuring) => {
    if (measuring) void startAgentrail();
    else stopAgentrail();
  });
  return () => { unsubscribe(); subscribed = false; };
}
