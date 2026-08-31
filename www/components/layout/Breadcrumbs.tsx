import Link from "next/link";
import { breadcrumbTrail, pageName } from "@/lib/routes";

/**
 * The visible breadcrumb trail.
 *
 * This exists so the BreadcrumbList in lib/jsonld.tsx is describing something
 * real. Structured data is meant to reflect what a reader can see; emitting a
 * trail in JSON-LD that appears nowhere on the page is the kind of mismatch
 * that gets the markup discarded rather than rewarded. It is also just useful:
 * eleven product pages sit two levels deep with nothing on the page saying so.
 *
 * Rendered as an ordered list inside <nav aria-label="Breadcrumb">, which is
 * the pattern assistive technology expects. The separators are decorative and
 * hidden, so a screen reader reads "Home, Product, Isolated Twin" rather than
 * "Home slash Product slash Isolated Twin".
 */
export function Breadcrumbs({ path }: { path: string }) {
  const trail = breadcrumbTrail(path);

  // One entry means the home page pointing at itself. Nothing to show.
  if (trail.length < 2) return null;

  return (
    <nav aria-label="Breadcrumb" className="mb-6 max-md:mb-4">
      <ol className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] tracking-extra-tight text-gray-new-50">
        {trail.map((route, i) => {
          const last = i === trail.length - 1;
          const label = pageName(route, "label");

          return (
            <li key={route.path} className="flex items-center gap-x-2">
              {i > 0 ? (
                <span aria-hidden="true" className="text-gray-new-80">
                  /
                </span>
              ) : null}
              {last ? (
                // The current page is not a link. `aria-current` is what tells
                // a screen reader which crumb is where the reader actually is.
                <span aria-current="page" className="text-gray-new-20">
                  {label}
                </span>
              ) : (
                <Link
                  href={route.path}
                  className="underline decoration-black/15 underline-offset-4 transition-colors hover:text-black hover:decoration-black/40"
                >
                  {label}
                </Link>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
