import type { Metadata } from "next";
import Link from "next/link";
import { Container } from "@/components/layout/Container";
import { SiteLayout } from "@/components/layout/SiteLayout";

export const metadata: Metadata = {
  title: "Not found — Antifailure",
  description: "That page is not here.",
  robots: { index: false, follow: true },
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
