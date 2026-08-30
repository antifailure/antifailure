import Link from "next/link";
import { Container } from "./Container";
import { FOOTER_MENUS, LEGAL_LINKS } from "@/lib/nav";

function FooterLink({ href, children }: { href: string; children: string }) {
  const className =
    "block py-[5px] text-[14px] leading-snug tracking-tight text-[#8a8a8a] transition-colors duration-200 hover:text-white";
  const external = href.startsWith("http");
  const docs = href === "/docs" || href.startsWith("/docs/");

  if (external) {
    return (
      <a className={className} href={href} target="_blank" rel="noreferrer">
        {children}
      </a>
    );
  }
  if (docs) {
    return (
      <a className={className} href={href}>
        {children}
      </a>
    );
  }
  return (
    <Link className={className} href={href}>
      {children}
    </Link>
  );
}

export function SiteFooter() {
  return (
    <footer className="relative z-30 mt-auto bg-black">
      <Container size="1920">
        <div className="grid grid-cols-6 gap-x-8 pt-16 pb-24 max-xl:grid-cols-3 max-xl:gap-y-12 max-xl:pt-12 max-xl:pb-16 max-md:grid-cols-2 max-md:gap-x-6 max-md:gap-y-10 max-md:pt-10 max-md:pb-14">
          <div className="max-xl:col-span-3 max-md:col-span-2">
            <Link href="/" aria-label="Antifailure home" className="inline-flex">
              <svg viewBox="0 0 18 18" className="h-7 w-7" fill="none" aria-hidden>
                <path
                  d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6"
                  stroke="#ffffff"
                  strokeWidth="2.1"
                  strokeLinecap="square"
                />
              </svg>
            </Link>
          </div>

          {FOOTER_MENUS.map((col) => (
            <nav key={col.heading} aria-label={col.heading}>
              <h2 className="text-[14px] font-medium tracking-tight text-white">{col.heading}</h2>
              <ul className="mt-4">
                {col.items.map((item) => (
                  <li key={`${col.heading}-${item.href}-${item.text}`}>
                    <FooterLink href={item.href}>{item.text}</FooterLink>
                  </li>
                ))}
              </ul>
            </nav>
          ))}
        </div>

        {/* gap-y as well as gap-x: this row wraps to two lines on a phone, and
            with only a column gap the wrapped line sat against the one above it. */}
        <nav
          aria-label="Legal"
          className="flex flex-wrap items-center gap-x-6 gap-y-2 pb-10 text-[13px] tracking-tight text-[#8a8a8a] max-md:gap-x-5 max-md:pb-[max(2rem,env(safe-area-inset-bottom))]"
        >
          {LEGAL_LINKS.map((item) => (
            <Link
              key={item.href}
              className="transition-colors duration-200 hover:text-white"
              href={item.href}
            >
              {item.text}
            </Link>
          ))}
        </nav>
      </Container>
    </footer>
  );
}
