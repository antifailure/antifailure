"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "./Button";
import { Container } from "./Container";
import { Logo } from "./Logo";
import { cn } from "@/lib/cn";
import { FOOTER_MENUS, GITHUB_URL, HEADER_MENUS } from "@/lib/nav";
import { HeaderMini, ProductMiniStyles } from "@/components/home/visuals/headerMinis";
import { Chevron, DiscordIcon, GitHubIcon } from "../icons";

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
    setHeight(panel?.scrollHeight ?? 0);
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

  return (
    <div className={cn("sticky top-0 z-50", overlay && "-mb-16")}>
      <ProductMiniStyles />
      <header
        className={cn(
          "header relative z-50 flex h-16 w-full items-center bg-white max-lg:h-14",
          "after:absolute after:right-0 after:bottom-0 after:left-0 after:h-px after:bg-gray-new-90",
        )}
      >
        <Container className="static z-10 flex w-full items-center justify-between max-md:px-8 max-sm:px-5" size="1920">
          <div className="flex items-center gap-x-[92px] xl:gap-x-10">
            <Logo />
            <nav className="group/main-nav max-lg:hidden" aria-label="Main">
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
                        <Link
                          href={menu.href}
                          className={cn(
                            "relative flex h-16 items-center gap-x-1 rounded-sm px-3.5 text-[15px] font-normal leading-normal tracking-snug whitespace-pre text-black/70 transition-colors duration-200 hover:text-black xl:px-2.5",
                            index === 0 && "-ml-3.5 xl:-ml-2.5",
                            pathname === menu.href && "text-black",
                          )}
                        >
                          {menu.text}
                        </Link>
                      ) : (
                        <button
                          type="button"
                          className={cn(
                            "group/main-nav-trigger relative flex h-16 items-center gap-x-1 rounded-sm px-3.5 text-[15px] font-normal leading-normal tracking-snug whitespace-pre text-black/70 transition-colors duration-200 hover:text-black xl:px-2.5",
                            isActive && "text-black",
                            open !== null && !isActive && "text-gray-new-50",
                          )}
                          aria-expanded={isActive}
                          aria-haspopup="menu"
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

          <div className="flex items-center gap-x-8 max-lg:hidden">
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
              <Link
                href="/signup"
                className="flex items-center gap-1.5 text-black transition-colors hover:text-gray-new-40"
              >
                <DiscordIcon className="h-[18px] w-[18px] text-gray-new-20" />
                <span className="text-sm leading-none tracking-extra-tight">Discord</span>
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
            className="hidden size-8 items-center justify-center max-lg:flex"
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
      </header>

      <div
        className={cn(
          "main-navigation-submenu absolute top-full left-0 z-40 w-full overflow-hidden border-b border-gray-new-90 bg-white transition-[height] duration-300 ease-[cubic-bezier(0.4,0,0.2,1)] max-lg:hidden",
          open === null ? "pointer-events-none border-transparent" : "pointer-events-auto",
        )}
        style={{ height: `${height}px` }}
        onMouseEnter={clearClose}
        onMouseLeave={() => setOpen(null)}
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
              >
                {sections.length > 0 && (
                  <Container
                    className="flex w-full items-start justify-between gap-x-12 overflow-visible pt-7 pb-10 xl:gap-x-8"
                    size="1920"
                  >
                    <ul className="flex flex-1 gap-x-[128px] pl-[195px] xl:gap-x-14 xl:pl-[143px] max-xl:pl-0">
                      {sections.map((section) => (
                        <li key={section.title} className={cn(menu.text === "Product" ? "min-w-[280px]" : "min-w-[220px]")}>
                          <span className="mb-6 block text-[10px] font-medium uppercase leading-none tracking-snug text-gray-new-50">
                            {section.title}
                          </span>
                          <ul className="flex flex-col gap-y-3.5">
                            {section.items.map((item) => {
                              const withMini = menu.text === "Product";
                              return (
                              <li key={item.href}>
                                <Link
                                  href={item.href}
                                  className={cn(
                                    "main-navigation-submenu-link group block text-[15px] leading-none tracking-extra-tight text-black hover:text-black/60",
                                    withMini && "flex items-center gap-3",
                                  )}
                                  onClick={closeNow}
                                >
                                  {withMini ? <HeaderMini title={item.title} /> : null}
                                  <span className="min-w-0 pr-2">
                                    {item.title}
                                    <span className="mt-1.5 block text-[13px] leading-snug text-gray-new-50">
                                      {item.description}
                                    </span>
                                  </span>
                                </Link>
                              </li>
                              );
                            })}
                          </ul>
                        </li>
                      ))}
                    </ul>
                    {menu.featured ? (
                      <Link
                        href={menu.featured.href}
                        onClick={closeNow}
                        className="mr-8 w-[280px] shrink-0 rounded-[10px] bg-[#E4F1EB] p-6 transition-colors hover:bg-[#d7ebe3] max-xl:mr-0"
                      >
                        <span className="mb-3 block text-[10px] font-medium uppercase leading-none tracking-snug text-black/45">
                          Featured
                        </span>
                        <span className="block text-[18px] leading-snug tracking-extra-tight text-black">
                          {menu.featured.title}
                        </span>
                        <span className="mt-2 block text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
                          {menu.featured.description}
                        </span>
                        <span className="mt-5 inline-flex items-center gap-1 text-[13px] font-medium tracking-extra-tight text-black">
                          {menu.featured.cta}
                          <span aria-hidden>→</span>
                        </span>
                      </Link>
                    ) : null}
                  </Container>
                )}
              </div>
            );
          })}
        </div>
      </div>

      {mobile ? (
        <div className="fixed inset-0 top-14 z-40 hidden overflow-y-auto bg-white px-5 pt-6 pb-16 max-lg:block">
          <div className="flex flex-col">
            {HEADER_MENUS.map((menu, index) => {
              const hasSubmenu = Boolean(menu.sections?.length);
              const expanded = mobileSection === index;
              if (!hasSubmenu && menu.href) {
                return (
                  <Link
                    key={menu.text}
                    href={menu.href}
                    className="border-b border-gray-new-90 py-4 text-[18px] tracking-tighter"
                    onClick={closeNow}
                  >
                    {menu.text}
                  </Link>
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
                          <div className="flex flex-col gap-3">
                            {section.items.map((item) => (
                              <Link
                                key={item.href}
                                href={item.href}
                                className="text-[16px] tracking-extra-tight"
                                onClick={closeNow}
                              >
                                {item.title}
                                <span className="mt-0.5 block text-[13px] text-gray-new-50">{item.description}</span>
                              </Link>
                            ))}
                          </div>
                        </div>
                      ))}
                      {menu.featured ? (
                        <Link
                          href={menu.featured.href}
                          onClick={closeNow}
                          className="rounded-[10px] bg-[#E4F1EB] p-4 text-[15px] tracking-extra-tight"
                        >
                          {menu.featured.title}
                        </Link>
                      ) : null}
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
    </div>
  );
}
