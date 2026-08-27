"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import type { ReactNode } from "react";
import { DOC_NAV } from "@/lib/docs";

export function DocsShell({ children }: { children: ReactNode }) {
  const pathname = usePathname();

  return (
    <div className="mx-auto flex min-h-[calc(100vh-64px)] max-w-[1180px] gap-12 px-6 lg:px-10">
      <aside className="sticky top-16 hidden h-[calc(100vh-64px)] w-[220px] shrink-0 overflow-y-auto py-10 lg:block">
        <Nav pathname={pathname} />
      </aside>
      <article className="min-w-0 flex-1 py-10 pb-24 lg:py-14">
        <nav className="mb-10 flex flex-wrap gap-x-4 gap-y-2 border-b border-black/10 pb-6 text-[13px] lg:hidden">
          {DOC_NAV.flatMap((g) => g.items).map((item) => (
            <Link
              key={item.href}
              href={item.href}
              className={pathname === item.href ? "text-black" : "text-black/45"}
            >
              {item.title}
            </Link>
          ))}
        </nav>
        <div className="max-w-[720px]">{children}</div>
      </article>
    </div>
  );
}

function Nav({ pathname }: { pathname: string }) {
  return (
    <>
      {DOC_NAV.map((group) => (
        <div key={group.title} className="mb-8">
          <div className="text-[10px] font-medium uppercase tracking-snug text-gray-new-50">
            {group.title}
          </div>
          <ul className="mt-3 space-y-2 text-[13.5px]">
            {group.items.map((item) => {
              const active = pathname === item.href;
              return (
                <li key={item.href}>
                  <Link
                    href={item.href}
                    className={active ? "text-black" : "text-black/45 hover:text-black/80"}
                  >
                    {item.title}
                  </Link>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </>
  );
}
