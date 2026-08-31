"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Button } from "./Button";
import { Container } from "./Container";
import { Logo } from "./Logo";
import { cn } from "@/lib/cn";
import { FOOTER_MENUS, GITHUB_URL, HEADER_MENUS } from "@/lib/nav";
import { HeaderMini, MenuCardArt, ProductMiniStyles } from "@/components/home/visuals/headerMinis";
import { BookIcon, Chevron, GitHubIcon } from "../icons";

function HeaderLink({
  href,
  className,
  onClick,
  children,
}: {
  href: string;
  className?: string;
  onClick?: () => void;
  children: ReactNode;
}) {
  if (href === "/docs" || href.startsWith("/docs/")) {
    return (
      <a href={href} className={className} onClick={onClick}>
        {children}
      </a>
    );
  }
  return (
    <Link href={href} className={className} onClick={onClick}>
      {children}
    </Link>
  );
}

export function SiteHeader({ overlay = true }: { overlay?: boolean }) {
  const pathname = usePathname();
  const [open, setOpen] = useState<number | null>(null);
  const [mobile, setMobile] = useState(false);
  const [mobileSection, setMobileSection] = useState<number | null>(0);
  const [height, setHeight] = useState(0);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const panelRefs = useRef<(HTMLDivElement | null)[]>([]);

  const clearClose = () => {
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
  };

  const enterMenu = (index: number | null) => {
    clearClose();
    setOpen(index);
  };

  const leaveMenu = () => {
    clearClose();
    timeoutRef.current = setTimeout(() => setOpen(null), 120);
  };

  const closeNow = useCallback(() => {
    clearClose();
    setOpen(null);
    setMobile(false);
  }, []);

  useEffect(() => {
    closeNow();
  }, [pathname, closeNow]);

  useEffect(() => {
    return () => clearClose();
  }, []);

  useEffect(() => {
    if (open === null) {
      setHeight(0);
      return;
    }
    const panel = panelRefs.current[open];
    if (!panel) {
      setHeight(0);
      return;
    }
    const measure = () => setHeight(panel.scrollHeight);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(panel);
    return () => ro.disconnect();
  }, [open]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeNow();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [closeNow]);

  useEffect(() => {
    document.body.style.overflow = mobile ? "hidden" : "";
    return () => {
      document.body.style.overflow = "";
    };
  }, [mobile]);

  useEffect(() => {
    const mq = window.matchMedia("(min-width: 1280px)");
    const onChange = () => {
      if (mq.matches) closeNow();
    };
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [closeNow]);

  return (
    // The banner is out here rather than around the bar alone, because the
    // flyout panel below is a sibling of the bar and not a child of it. With
    // <header> on the inner element the panel's nineteen links belonged to no
    // landmark at all: every other nav on the page sits inside header, main or
    // footer, and the navigation itself did not.
    <header className={cn("sticky top-0 z-50", overlay && "-mb-16 max-xl:-mb-14")}>
      <ProductMiniStyles />
      <div
        className={cn(
          "header relative z-50 flex h-16 w-full items-center bg-white max-xl:h-14",
          "after:absolute after:right-0 after:bottom-0 after:left-0 after:h-px after:bg-gray-new-90",
        )}
      >
        <Container className="static z-10 flex w-full items-center justify-between gap-x-6 max-md:px-8 max-sm:px-5" size="1920">
          {/* The gap used to be 92px below `xl` and 40px above it, which is backwards: the narrow end is where the row runs out of room, and at 1024 it pushed the sign-up button past the right edge of the page. */}
            <div className="flex items-center gap-x-6 xl:gap-x-10">
            <Logo />
            <nav className="group/main-nav max-xl:hidden" aria-label="Main">
              <ul className="flex items-center">
                {HEADER_MENUS.map((menu, index) => {
                  const hasSubmenu = Boolean(menu.sections?.length);
                  const isActive = open === index;
                  return (
                    <li
                      key={menu.text}
                      className="flex h-16 items-center"
                      onMouseEnter={() => enterMenu(hasSubmenu ? index : null)}
                      onMouseLeave={leaveMenu}
                    >
                      {menu.href && !hasSubmenu ? (
                        <HeaderLink
                          href={menu.href}
                          className={cn(
                            "relative flex h-16 items-center gap-x-1 rounded-sm px-2.5 text-[15px] font-normal leading-normal tracking-snug whitespace-pre text-black/70 transition-colors duration-200 hover:text-black",
                            index === 0 && "-ml-2.5",
                            pathname === menu.href && "text-black",
                          )}
                        >
                          {menu.text}
                        </HeaderLink>
                      ) : (
                        <button
                          type="button"
                          className={cn(
                            "group/main-nav-trigger relative flex h-16 items-center gap-x-1 rounded-sm px-2.5 text-[15px] font-normal leading-normal tracking-snug whitespace-pre text-black/70 transition-colors duration-200 hover:text-black",
                            isActive && "text-black",
                            open !== null && !isActive && "text-gray-new-50",
                          )}
                          aria-expanded={isActive}
                          aria-haspopup="menu"
                          // The panel already carried this id and nothing
                          // pointed at it, so the button announced that it
                          // expands something without ever saying what.
                          aria-controls={`submenu-${index}`}
                          onClick={() => enterMenu(isActive ? null : index)}
                        >
                          {menu.text}
                          <Chevron
                            className={cn(
                              "h-2.5 w-2.5 text-gray-new-50 opacity-60 transition-transform duration-200",
                              isActive && "rotate-180 text-black opacity-100",
                            )}
                          />
                        </button>
                      )}
                    </li>
                  );
                })}
              </ul>
            </nav>
          </div>

          <div className="flex items-center gap-x-8 max-xl:hidden">
            <div className="flex items-center gap-x-6">
              <a
                href={GITHUB_URL}
                target="_blank"
                rel="noopener noreferrer"
                className="group flex items-center gap-1.5 rounded-sm text-black transition-colors duration-200 hover:text-gray-new-40"
              >
                <GitHubIcon className="h-[18px] w-[18px] text-gray-new-20" />
                <span className="text-sm leading-none tracking-extra-tight">GitHub</span>
              </a>
              {/* There is no Discord. The link that used to sit here was
                  labelled Discord and went to the waitlist form, which is a
                  broken promise in the header of every page. */}
              <Link
                href="/docs"
                className="flex items-center gap-1.5 text-black transition-colors hover:text-gray-new-40"
              >
                <BookIcon className="h-[18px] w-[18px] text-gray-new-20" />
                <span className="text-sm leading-none tracking-extra-tight">Docs</span>
              </Link>
            </div>
            <div className="flex gap-x-3.5">
              <Button href="/signin" theme="outlined" size="xxs" className="h-9 px-[18px]">
                Log in
              </Button>
              <Button href="/signup" theme="filled" size="xxs" className="h-9 px-[18px]">
                Sign up
              </Button>
            </div>
          </div>

          <button
            type="button"
            className="hidden size-11 items-center justify-center max-xl:flex"
            aria-label={mobile ? "Close menu" : "Open menu"}
            aria-expanded={mobile}
            onClick={() => setMobile((v) => !v)}
          >
            <span className="flex flex-col gap-1.5">
              <span className={cn("block h-px w-5 bg-black transition", mobile && "translate-y-[4px] rotate-45")} />
              <span className={cn("block h-px w-5 bg-black transition", mobile && "-translate-y-[4px] -rotate-45")} />
            </span>
          </button>
        </Container>
      </div>

      <div
        className={cn(
          "main-navigation-submenu absolute top-full left-0 z-40 w-full overflow-hidden border-b border-gray-new-90 bg-white transition-[height] duration-500 ease-[cubic-bezier(0.16,1,0.3,1)] max-xl:hidden",
          open === null ? "pointer-events-none border-transparent" : "pointer-events-auto",
        )}
        // Closed, this panel is a pixel tall with its overflow clipped, and
        // clipping removes nothing from the tab order. All nineteen links
        // stayed focusable and stayed in the accessibility tree, so tabbing
        // from the logo went through nineteen invisible destinations before
        // reaching the page. `pointer-events: none` only ever covered the mouse.
        //
        // `inert` is the attribute for this: not focusable, not read, not
        // clickable, and it takes the whole subtree with it.
        inert={open === null || undefined}
        style={{ height: `${height}px` }}
        onMouseEnter={clearClose}
        onMouseLeave={leaveMenu}
      >
        <div className="relative w-full">
          {HEADER_MENUS.map((menu, index) => {
            const isActive = open === index;
            const sections = menu.sections ?? [];
            return (
              <div
                key={menu.text}
                id={`submenu-${index}`}
                ref={(el) => {
                  panelRefs.current[index] = el;
                }}
                className={cn(
                  "absolute top-0 left-0 w-full transition-opacity duration-200",
                  isActive ? "opacity-100" : "pointer-events-none opacity-0",
                )}
                // The panels are stacked on each other and all but one is
                // transparent. Transparent is still focusable, so without this
                // an open menu would hand a keyboard user every other menu's
                // links as well as its own.
                inert={!isActive || undefined}
              >
                {sections.length > 0 && (
                  <Container className="overflow-visible pt-8 pb-10" size="1920">
                    <div className="flex items-start gap-x-20 pl-[195px] xl:gap-x-16 xl:pl-[143px] max-xl:pl-0">
                      <ul className="flex shrink-0 gap-x-16">
                        {sections.map((section) => (
                          <li key={section.title} className="w-[240px]">
                            <span className="mb-6 block text-[11px] font-medium uppercase leading-none tracking-[0.1em] text-black/55">
                              {section.title}
                            </span>
                            <ul className="flex flex-col gap-y-6">
                              {section.items.map((item) => (
                                <li key={item.href}>
                                  <HeaderLink href={item.href} className="group block" onClick={closeNow}>
                                    <span className="block text-[16px] font-medium leading-none tracking-tight text-black transition-colors duration-200 group-hover:text-black/55">
                                      {item.title}
                                    </span>
                                    <span className="mt-1.5 block text-[13.5px] leading-snug tracking-tight text-black/60">
                                      {item.description}
                                    </span>
                                  </HeaderLink>
                                </li>
                              ))}
                            </ul>
                          </li>
                        ))}
                      </ul>
                      {menu.featured?.length ? (
                        <div className="w-[540px] max-w-full shrink-0">
                          <div className="flex flex-col gap-3">
                            {menu.featured.map((card) => (
                              <Link
                                key={card.href}
                                href={card.href}
                                onClick={closeNow}
                                className="flex h-[128px] items-center justify-between gap-6 rounded-[14px] border border-black/[0.1] bg-[#f6f6f4] py-4 pr-4 pl-6 transition-colors duration-200 hover:bg-[#E4F1EB]"
                              >
                                <span className="min-w-0 max-w-[260px]">
                                  <span className="block text-[16px] font-medium leading-snug tracking-tight text-black">
                                    {card.title}
                                  </span>
                                  <span className="mt-1.5 block text-[13.5px] leading-5 tracking-tight text-black/60">
                                    {card.description}
                                  </span>
                                </span>
                                {card.visual === "twin" || card.visual === "fleet" ? (
                                  <MenuCardArt kind={card.visual} />
                                ) : null}
                              </Link>
                            ))}
                          </div>
                        </div>
                      ) : null}
                    </div>
                  </Container>
                )}
              </div>
            );
          })}
        </div>
      </div>

      {mobile ? (
        <div className="fixed inset-0 top-14 z-40 hidden overflow-y-auto bg-white px-5 pt-6 pb-[max(4rem,env(safe-area-inset-bottom))] max-xl:block">
          <div className="flex flex-col">
            {HEADER_MENUS.map((menu, index) => {
              const hasSubmenu = Boolean(menu.sections?.length);
              const expanded = mobileSection === index;
              if (!hasSubmenu && menu.href) {
                return (
                  <HeaderLink
                    key={menu.text}
                    href={menu.href}
                    className="border-b border-gray-new-90 py-4 text-[18px] tracking-tighter"
                    onClick={closeNow}
                  >
                    {menu.text}
                  </HeaderLink>
                );
              }
              return (
                <div key={menu.text} className="border-b border-gray-new-90">
                  <button
                    type="button"
                    className="flex w-full items-center justify-between py-4 text-left text-[18px] tracking-tighter"
                    aria-expanded={expanded}
                    onClick={() => setMobileSection(expanded ? null : index)}
                  >
                    {menu.text}
                    <Chevron className={cn("h-3 w-3 text-gray-new-50 transition", expanded && "rotate-180")} />
                  </button>
                  {expanded ? (
                    <div className="flex flex-col gap-5 pb-5">
                      {menu.sections?.map((section) => (
                        <div key={section.title}>
                          <div className="mb-3 text-[10px] font-medium uppercase tracking-snug text-gray-new-50">
                            {section.title}
                          </div>
                          {/* The same thumbnails the desktop dropdown gets. The
                              phone menu is the first thing most visitors open,
                              and eleven identical text rows told them nothing
                              about what any of these pages contain. */}
                          <div className="flex flex-col gap-3.5">
                            {section.items.map((item) => (
                              <HeaderLink
                                key={item.href}
                                href={item.href}
                                className="group flex items-center gap-3 text-[16px] tracking-extra-tight"
                                onClick={closeNow}
                              >
                                <HeaderMini title={item.title} />
                                <span className="min-w-0">
                                  {item.title}
                                  <span className="mt-0.5 block text-[13px] leading-snug text-gray-new-50">
                                    {item.description}
                                  </span>
                                </span>
                              </HeaderLink>
                            ))}
                          </div>
                        </div>
                      ))}
                      {menu.featured?.length
                        ? menu.featured.map((card) => (
                            <Link
                              key={card.href}
                              href={card.href}
                              onClick={closeNow}
                              className="rounded-[12px] border border-black/[0.08] bg-[#f7f7f5] p-4 text-[15px] tracking-tight"
                            >
                              {card.title}
                              <span className="mt-1 block text-[13px] text-gray-new-50">{card.description}</span>
                            </Link>
                          ))
                        : null}
                    </div>
                  ) : null}
                </div>
              );
            })}
            <div className="mt-8 flex flex-col gap-3">
              {FOOTER_MENUS[0].items.map((item) => (
                <Link key={item.href} href={item.href} className="text-[16px] text-gray-new-40" onClick={closeNow}>
                  {item.text}
                </Link>
              ))}
            </div>
            <div className="mt-8 flex gap-3">
              <Button href="/signin" theme="outlined" className="flex-1">
                Log in
              </Button>
              <Button href="/signup" theme="filled" className="flex-1">
                Sign up
              </Button>
            </div>
          </div>
        </div>
      ) : null}
    </header>
  );
}
