// The frame every signed-in page sits in.
//
// A control plane's navigation has one job: say where you are and let you get
// to the other four places without thinking. So it is one row, the current
// page is marked by a rule under it rather than by a coloured pill, and the
// organization you are acting as is in the corner where it can be read at a
// glance. Being in the wrong tenant is the single most confusing state this
// application has, because every page is simply empty, so the answer to "which
// one am I in" is never more than one glance away.

import type { ReactNode } from "react";
import { cn } from "./ui";

export function LogoMark({ className = "h-[18px] w-[18px]" }: { className?: string }) {
  return (
    <svg viewBox="0 0 18 18" className={className} fill="none" aria-hidden>
      <path
        d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6"
        stroke="#33bf00"
        strokeWidth="2.1"
        strokeLinecap="square"
      />
    </svg>
  );
}

const NAV = [
  { href: "/", label: "Environments" },
  { href: "/network", label: "Network" },
  { href: "/audit", label: "Audit" },
];

export function Chrome({
  children,
  current,
  who,
  org,
  role,
}: {
  children: ReactNode;
  /** The href of the section being shown, so one item is marked. */
  current: string;
  who?: string;
  org?: string;
  role?: string | null;
}) {
  return (
    <>
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-3 focus:top-3 focus:z-50 focus:rounded-lg focus:bg-ink focus:px-3 focus:py-2 focus:text-[13px] focus:text-white"
      >
        Skip to content
      </a>

      <header className="sticky top-0 z-40 border-b border-hair bg-cream/95 backdrop-blur-[2px]">
        <div className="mx-auto flex h-[54px] w-full max-w-[1180px] items-center justify-between gap-4 px-4 sm:px-6 lg:px-8">
          <div className="flex min-w-0 items-center gap-5">
            <a href="/" className="flex shrink-0 items-center gap-2">
              <LogoMark />
              <span
                className="hidden font-semibold uppercase text-ink sm:inline"
                style={{ fontSize: 12.5, letterSpacing: "0.12em" }}
              >
                Antifailure
              </span>
            </a>

            <nav aria-label="Sections" className="flex min-w-0 items-center gap-1">
              {NAV.map((item) => {
                const here =
                  item.href === "/" ? current === "/" : current.startsWith(item.href);
                return (
                  <a
                    key={item.href}
                    href={item.href}
                    aria-current={here ? "page" : undefined}
                    className={cn(
                      "relative rounded-md px-2 py-1.5 text-[13px] tracking-snug transition-colors",
                      here ? "text-ink" : "text-muted hover:text-ink",
                    )}
                  >
                    {item.label}
                    {here ? (
                      <span
                        aria-hidden
                        className="absolute inset-x-2 -bottom-[13px] h-[2px] rounded-full bg-ink"
                      />
                    ) : null}
                  </a>
                );
              })}
            </nav>
          </div>

          <div className="flex shrink-0 items-center gap-3">
            {org ? (
              <span
                className="hidden max-w-[22ch] truncate text-[12.5px] text-muted sm:inline"
                title={role ? `${who ?? "Signed in"} — ${role} of ${org}` : org}
              >
                {org}
                {role ? <span className="text-faint"> · {role}</span> : null}
              </span>
            ) : null}
            <form action="/signout" method="post">
              <button
                type="submit"
                className="rounded-md px-2 py-1.5 text-[13px] text-muted transition-colors hover:text-ink"
              >
                Sign out
              </button>
            </form>
          </div>
        </div>
      </header>

      {/* On a phone the header row is full: a mark, three sections, and a way
          out. The organization does not fit there and it is the one thing that
          must not be missing, because being in the wrong tenant renders every
          page empty with nothing on screen to explain it. So it gets its own
          thin line below, on small screens only. */}
      {org ? (
        <div className="border-b border-hair bg-cream px-4 py-1.5 text-[12px] text-muted sm:hidden">
          <span className="truncate">{org}</span>
          {role ? <span className="text-faint"> · {role}</span> : null}
        </div>
      ) : null}

      <main id="main">{children}</main>
    </>
  );
}
