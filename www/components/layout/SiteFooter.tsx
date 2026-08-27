import Link from "next/link";
import { Container } from "./Container";
import { Logo } from "./Logo";
import { FOOTER_MENUS } from "@/lib/nav";

export function SiteFooter() {
  return (
    <footer className="relative z-30 mt-auto border-t border-gray-new-90 bg-white safe-paddings">
      <Container className="flex justify-between gap-x-10 py-12 max-sm:py-5" size="1920">
        <div className="flex flex-col items-start max-lg:w-full">
          <div className="mb-auto max-lg:mb-11">
            <Logo />
            <span className="mt-3.5 block text-[13px] leading-none tracking-extra-tight text-gray-new-40 xl:mt-3">
              Pre-production deployment safety
            </span>
          </div>
          <div className="flex flex-col items-start justify-between gap-5 max-lg:w-full max-lg:flex-row max-sm:flex-col">
            <div className="flex items-center gap-2 text-[13px] tracking-extra-tight text-gray-new-40">
              <span className="h-2 w-2 rounded-full bg-[#33bf00]" />
              Design-partner waitlist open
            </div>
            <div className="flex max-w-2xl flex-col gap-y-2 text-[13px] leading-none tracking-extra-tight text-gray-new-40">
              <p>
                © Antifailure 2026. All rights reserved. Pre-production deployment safety is a
                product category, not a guarantee that every production incident is predicted.
              </p>
              <p className="flex flex-wrap gap-x-3 gap-y-1">
                <Link className="hover:text-black" href="/privacy">
                  Privacy Notice
                </Link>
                <Link className="hover:text-black" href="/terms">
                  Terms of Use
                </Link>
                <Link className="hover:text-black" href="/security">
                  Data boundary
                </Link>
              </p>
            </div>
          </div>
        </div>

        <div className="flex w-fit gap-x-[88px] max-xl:gap-x-6 max-lg:hidden">
          {FOOTER_MENUS.map((col) => (
            <div className="grid content-start gap-y-7" key={col.heading}>
              <span className="text-[10px] uppercase leading-none text-gray-new-10">{col.heading}</span>
              <ul className="flex flex-col gap-y-5">
                {col.items.map((item) => (
                  <li key={item.href} className="-my-px flex min-w-[148px] py-px">
                    <Link
                      className="text-[15px] leading-none tracking-extra-tight text-gray-new-40 transition-colors duration-200 hover:text-black"
                      href={item.href}
                    >
                      {item.text}
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </Container>
    </footer>
  );
}
