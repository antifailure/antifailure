# fixed

The marketing site's product and mobile menus are navigation landmarks again.
Both panels render outside `<header>`, in plain divs, so somebody browsing by
landmark went from the header straight past the site's whole product menu, and
below the `xl` breakpoint there was no navigation landmark on the page at all.
The triggers now name the panel they open.

Seventy kilobytes of marketing content with no importer is deleted:
`lib/company-content.tsx`, `lib/solutions-content.tsx` and
`lib/marketing-content.tsx`, left behind when the pages moved to
`components/pages`. Two of them were still named as the source of a route's
last modified date, so the home page and every solutions page in the sitemap
took their date from a file nothing renders. The first also held links to four
pages that answer 404.
