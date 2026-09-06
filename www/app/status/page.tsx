import { MovedPage, movedMetadata } from "@/components/layout/MovedPage";
import { STATUS_URL } from "@/lib/nav";
import { pageTitle } from "@/lib/site";

// Not a page of this site, and it must never become one: the status page is
// served from GitHub so that it stays up when the host of this site does not.
// This route exists because /status is the address a person types during an
// outage, and until it existed that address was a 404 on the site whose
// footer now links the page. The production host answers it with a 301 from
// public/staticwebapp.config.json; this markup is for a preview or a local
// build served without the host config, the same arrangement as the moved
// product pages.
export const metadata = movedMetadata(STATUS_URL, pageTitle("Status"));

export default function Page() {
  return <MovedPage to={STATUS_URL} label="the status page" />;
}
