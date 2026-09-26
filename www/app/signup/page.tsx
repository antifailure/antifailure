import { MovedPage, movedMetadata } from "@/components/layout/MovedPage";
import { pageTitle } from "@/lib/site";

/**
 * /signup moved to /request-demo.
 *
 * Sending strangers to a demo request is a decision about the funnel rather
 * than a consequence of a closed door, and this comment said the opposite until
 * somebody checked. `AF_SELF_SERVE_SIGNUP` is ON: the value is
 * `self_serve_signup` in `infra/terraform/stacks/control-plane/staging.tfvars`
 * and `production.tfvars`, both `true` since 61f8578c6 on 2026-09-02. The
 * default in `variables.tf` is `false` and that is what misled the change that
 * wrote this page. A default is not a value. See
 * components/pages/company/RequestDemo.tsx for the whole of it.
 *
 * Existing operators still sign in with GitHub at /signin.
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
