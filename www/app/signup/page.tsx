import { MovedPage, movedMetadata } from "@/components/layout/MovedPage";
import { pageTitle } from "@/lib/site";

/**
 * /signup moved to /request-demo.
 *
 * Self-serve organization creation is off (`AF_SELF_SERVE_SIGNUP` defaults
 * off), so a page headed "Create an account" was describing a door that does
 * not open for a stranger. The hosted plane is entered by a booked demo now,
 * and existing operators still sign in with GitHub at /signin.
 *
 * The production host serves the 301 in public/staticwebapp.config.json, so
 * almost nobody reaches the markup below. It exists for the same reason the
 * product moved-pages do: the build is `output: "export"`, which refuses
 * next.config redirects because there is no server to evaluate them, and a
 * preview or a local `next start` serves the static files without the host
 * config, where a missing page is a 404 on a URL that is in bookmarks and was
 * indexed. The MovedPage carries a canonical to /request-demo and noindex, so
 * the one indexable result is the demo page rather than two spellings of it.
 */
export const metadata = movedMetadata("/request-demo", pageTitle("Request a demo"));

export default function Page() {
  return <MovedPage to="/request-demo" label="request a demo" />;
}
