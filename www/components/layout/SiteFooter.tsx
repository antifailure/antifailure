import Link from "next/link";
import { Container } from "./Container";
import { FOOTER_MENUS, LEGAL_LINKS } from "@/lib/nav";

function FooterLink({ href, children }: { href: string; children: string }) {
  // 44px of row on a coarse pointer, and the mouse rendering untouched.
  //
  // These twenty eight links sit flush: the row is 29px and the gap between
  // rows is 0, so unlike the legal row and the mark below there is nothing for
  // a hit area to grow into. Padding with a negative margin would give each
  // link a 44px box overlapping its neighbour by 15px, and the later sibling
  // paints on top, so every link would lose its bottom 15px to the one after
  // it and the last would keep a target nobody else had. That is worse than
  // leaving it, and it is invisible in a screenshot.
  //
  // So the rhythm itself has to grow, and it only has to grow for a thumb. A
  // fine pointer keeps 29px, which is above the 24px WCAG 2.5.8 minimum and is
  // what makes five columns scannable; a coarse one gets 44px and pays 105px
  // per grid row of columns for it. `[@media(hover:hover)]` in HeroServices is
  // the same capability query from the other side.
  const className =
    "block py-[5px] text-[14px] leading-snug tracking-tight text-[#8a8a8a] transition-colors duration-200 hover:text-white pointer-coarse:py-[12.5px]";
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
    <Link prefetch={false} className={className} href={href}>
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
            {/* `p-2 -m-2` is 28px of mark inside a 44px target: the mark sits
                alone in its grid cell with nothing to overlap, so the padding
                buys the hit area and the negative margin gives back the space,
                which is the same trade the header logo makes. */}
            <Link prefetch={false} href="/" aria-label="Antifailure home" className="inline-flex p-2 -m-2">
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
            with only a column gap the wrapped line sat against the one above it.
            gap-y is 24px rather than 8px because the links below carry a 44px
            hit area that reaches 12px past the text on each side, and two
            wrapped lines 8px apart would have overlapped by 16px. On one line,
            which is every width from 768 up, gap-y does not apply and nothing
            about this row moves. */}
        <nav
          aria-label="Legal"
          className="flex flex-wrap items-center gap-x-6 gap-y-6 pb-10 text-[13px] tracking-tight text-[#8a8a8a] max-md:gap-x-5 max-md:pb-[max(2rem,env(safe-area-inset-bottom))]"
        >
          {LEGAL_LINKS.map((item) => (
            <Link prefetch={false}
              key={item.href}
              // `leading-5` before the padding, and it is load bearing rather
              // than tidy. 13px text inherits the 1.5 line height, so the line
              // box is 19.5px and 12px of padding either side reaches 43.5, not
              // 44. Half a pixel short is still short, and it measures 43.5 in
              // a browser while the arithmetic that produced it says 44, which
              // is the kind of gap a screenshot cannot show. Pinning the line
              // box at 20px makes the number the comment claims the number the
              // element renders.
              //
              // 10px of side padding is deliberate rather than round: DPA is
              // 25px wide and needs 19px to reach 44, and the column gap is
              // 20px at 375, so two neighbours meeting in the middle of it end
              // up exactly touching with nothing to spare.
              className="px-2.5 py-3 -mx-2.5 -my-3 leading-5 transition-colors duration-200 hover:text-white"
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
