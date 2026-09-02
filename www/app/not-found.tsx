import type { Metadata } from "next";
import Link from "next/link";
import { Container } from "@/components/layout/Container";
import { SiteLayout } from "@/components/layout/SiteLayout";
import { pageTitle } from "@/lib/site";

export const metadata: Metadata = {
  // `absolute`, because the root layout appends the site name to any bare
  // string and this title already carries it. Live production serves the site
  // name twice on this page today, and swapping the separator alone would
  // only have made the doubling tidier. This is the third page to fall into
  // the trap lib/seo.ts documents, after the six moved paths, which is a good
  // argument for the helper being the only way a title is ever written.
  title: { absolute: pageTitle("Not found") },
  description: "That page is not here.",
  // This `robots` is required, and removing it is worse than the redundancy it
  // causes. The root layout declares `index: true` for the whole site, and a
  // child's metadata is what overrides it, so deleting this key does not leave
  // one clean tag behind: it ships `index, follow` on the 404. The check below
  // caught exactly that within a minute of trying it.
  //
  // Two tags remain, `noindex` from Next's own handling of this route and
  // `noindex, follow` from here, and they were never actually in conflict:
  // bare `noindex` already implies `follow`, so both say the same thing. The
  // redundancy cannot be removed from this side, only made consistent. What is
  // asserted below is therefore the property that matters, that no robots tag
  // on this page permits indexing, rather than a tag count nothing can hold to.
  robots: { index: false, follow: true },
  // No canonical, and this is the strongest of the three claims on this page.
  //
  // The root layout sets `canonical: SITE_URL` for the whole site, so every
  // 404 inherited it and told crawlers that this page IS the home page. That
  // is a worse claim than either robots tag: it is not "do not index me", it
  // is "I am something else".
  //
  // Google's documented behaviour makes it concrete rather than theoretical.
  // Faced with `noindex` and a rel=canonical pointing elsewhere, it generally
  // honours the canonical over the noindex, and its canonicalization guidance
  // separately says to check that a canonical target does not itself carry a
  // noindex. Both halves of that point the same way here: a 404 saying it is
  // the home page, while also saying do not index, is an invitation to
  // consolidate the two and to carry the noindex across with them.
  //
  // `null` rather than a self-referential URL, because this file is served for
  // every mistyped path in the site under `output: "export"`. There is no one
  // address it could name that would be true.
  alternates: {
    canonical: null,
    types: { "text/markdown": "https://antifailure.dev/404.md" },
  },
};

// Where somebody who mistyped a URL most plausibly meant to go. Deliberately
// short: a 404 that lists thirty links is a sitemap, and a sitemap is not what
// a person who is already lost is looking for.
const ROUTES = [
  { href: "/docs", label: "Documentation", note: "Every concept, guide and reference." },
  { href: "/docs/getting-started/quickstart", label: "Quickstart", note: "An empty machine to a working environment." },
  { href: "/docs/reference/errors", label: "Error reference", note: "Every code the engine returns, and what to do." },
  { href: "/product", label: "Product", note: "What it builds for a pull request." },
  { href: "/pricing", label: "Design partners", note: "How to get an early environment." },
];

export default function NotFound() {
  return (
    <SiteLayout overlay={false}>
      <Container size="1344" className="flex flex-1 flex-col justify-center py-24 max-lg:py-16">
        <p className="font-mono text-[13px] uppercase tracking-[0.14em] text-gray-new-40">
          404
        </p>
        <h1 className="mt-4 max-w-[18ch] text-[44px] font-medium leading-[1.05] tracking-tighter text-black max-lg:text-[34px] max-sm:text-[28px]">
          That page is not here.
        </h1>
        <p className="mt-5 max-w-[52ch] text-[15px] leading-[1.6] tracking-extra-tight text-gray-new-40">
          Nothing is published at this address. If you followed a link out of an
          Antifailure error message, that is a bug in the product rather than
          something you did: every error code is supposed to name a page that
          exists, and a build gate checks it. It is worth{" "}
          <a
            className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
            href="https://github.com/antifailure/antifailure/issues/new"
          >
            reporting
          </a>
          .
        </p>

        <ul className="mt-12 max-w-[46rem] border-t border-gray-new-90">
          {ROUTES.map((r) => {
            const docs = r.href === "/docs" || r.href.startsWith("/docs/");
            const cls =
              "group grid grid-cols-[minmax(0,14rem)_minmax(0,1fr)] items-baseline gap-x-8 py-4 max-sm:grid-cols-1 max-sm:gap-y-1";
            const inner = (
              <>
                <span className="text-[15px] font-medium tracking-extra-tight text-black group-hover:underline group-hover:underline-offset-4">
                  {r.label}
                </span>
                <span className="text-[14px] leading-[1.5] tracking-extra-tight text-gray-new-40">
                  {r.note}
                </span>
              </>
            );
            return (
              <li key={r.href} className="border-b border-gray-new-90">
                {docs ? (
                  <a href={r.href} className={cls}>
                    {inner}
                  </a>
                ) : (
                  <Link href={r.href} className={cls}>
                    {inner}
                  </Link>
                )}
              </li>
            );
          })}
        </ul>
      </Container>
    </SiteLayout>
  );
}
