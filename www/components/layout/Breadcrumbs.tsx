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
      {/* gray-new-40 rather than 50, and it is a contrast fix rather than a
          preference. 50 is #797d86, which at 13px measures 3.85:1 on the paper
          ground, 4.13:1 on white and 3.55:1 on the sage bands, so the crumb
          trail failed 4.5:1 on every page of the site that renders it. 40 is
          the next token up and clears it on all three: 5.53, 5.93 and 5.10.
          Nothing outside the scale was invented; the scale already had a
          passing grey one step away. */}
      <ol className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] tracking-extra-tight text-gray-new-40">
        {trail.map((route, i) => {
          const last = i === trail.length - 1;
          const label = pageName(route, "label");

          return (
            <li key={route.path} className="flex items-center gap-x-2">
              {i > 0 ? (
                // 80 is #c9cbcf, 1.51:1 on paper, which is a separator you
                // cannot see. 50 is the lightest token that reads, and it
                // stays lighter than the crumbs either side of it so the
                // hierarchy is unchanged. It is stated rather than implied
                // that this one does NOT reach 4.5:1: it is aria-hidden and
                // carries no information, and darkening it to the label's own
                // weight would make the separator compete with the labels.
                <span aria-hidden="true" className="text-gray-new-50">
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
                <Link prefetch={false}
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
