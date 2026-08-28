import Link from "next/link";
import { Container } from "./Container";
import { Logo } from "./Logo";
import { GitHubIcon } from "@/components/icons";
import { FooterWaitlist } from "./FooterWaitlist";
import { FOOTER_MENUS, GITHUB_URL } from "@/lib/nav";

/**
 * The footer.
 *
 * The previous one hid all three link columns below `lg`, so on a phone the
 * footer was a wordmark, a status line and a paragraph of legal text: every
 * navigation link in it was unreachable on the device most people arrive on.
 * It also pinned the brand block to the top of a stretched flex column with
 * `mb-auto`, which opened a several-hundred-pixel hole down the left side at
 * desktop widths.
 *
 * This is built as two bands instead. The top band is the sitemap, which is
 * what a footer is for, laid out on a real grid that reflows to two columns on
 * a phone rather than disappearing. The bottom band is the small print, on the
 * other side of a hairline so it reads as a different kind of thing.
 */
export function SiteFooter() {
  return (
    <footer className="relative z-30 mt-auto border-t border-gray-new-90 bg-white safe-paddings">
      <Container size="1920">
        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-16 py-16 max-xl:gap-x-10 max-lg:grid-cols-1 max-lg:gap-y-12 max-lg:py-12 max-md:py-10">
          {/* Brand. Sized to sit level with the column headings rather than
              floating apart from them. */}
          <div className="flex max-w-[360px] flex-col items-start max-lg:max-w-none">
            <Logo />
            <p className="mt-4 text-[14px] leading-normal tracking-extra-tight text-gray-new-40">
              A disposable copy of your production stack for every pull request.
            </p>

            <FooterWaitlist />

            <div className="mt-auto flex flex-wrap items-center gap-x-5 gap-y-3 pt-8 max-lg:mt-0 max-lg:pt-7">
              <a
                className="inline-flex items-center gap-x-2 py-2.5 text-[14px] tracking-extra-tight text-gray-new-40 transition-colors duration-200 hover:text-black"
                href={GITHUB_URL}
                target="_blank"
                rel="noreferrer"
              >
                <GitHubIcon className="h-4 w-4" />
                View the source
              </a>
              <span className="flex items-center gap-x-2 text-[14px] tracking-extra-tight text-gray-new-40">
                {/* Static. A status indicator that throbs is asking for
                    attention it has not earned. */}
                <span className="h-2 w-2 shrink-0 rounded-full bg-[#33bf00]" />
                Waitlist open
              </span>
            </div>
          </div>

          {/* Sitemap. Three columns on desktop, two on a tablet, two on a
              phone: three would put every label on two lines at 390px. */}
          <nav
            aria-label="Footer"
            className="grid grid-cols-3 gap-x-20 max-xl:gap-x-12 max-lg:gap-x-8 max-md:grid-cols-2 max-md:gap-y-9"
          >
            {FOOTER_MENUS.map((col) => (
              <div key={col.heading}>
                <h2 className="text-[11px] font-medium uppercase tracking-[0.1em] text-gray-new-10">
                  {col.heading}
                </h2>
                <ul className="mt-3 flex flex-col">
                  {col.items.map((item) => (
                    <li key={item.href}>
                      <Link
                        className="block py-2.5 text-[15px] leading-normal tracking-extra-tight text-gray-new-40 transition-colors duration-200 hover:text-black"
                        href={item.href}
                      >
                        {item.text}
                      </Link>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </nav>
        </div>

        {/* Small print. */}
        <div className="flex items-start justify-between gap-x-10 gap-y-5 border-t border-gray-new-90 py-7 max-lg:flex-col max-md:py-6">
          <p className="max-w-[720px] text-[13px] leading-normal tracking-extra-tight text-gray-new-50">
            © Antifailure 2026. Pre-production deployment safety is a product
            category, not a guarantee that every production incident is
            predicted.
          </p>
          <span className="-my-2 flex shrink-0 items-center gap-x-5 text-[13px] tracking-extra-tight text-gray-new-50">
            <Link className="py-2.5 transition-colors duration-200 hover:text-black" href="/privacy">
              Privacy
            </Link>
            <Link className="py-2.5 transition-colors duration-200 hover:text-black" href="/terms">
              Terms
            </Link>
            <Link className="py-2.5 transition-colors duration-200 hover:text-black" href="/security">
              Security
            </Link>
          </span>
        </div>
      </Container>
    </footer>
  );
}
