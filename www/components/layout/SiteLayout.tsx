import type { ReactNode } from "react";
import { SiteFooter } from "./SiteFooter";
import { SiteHeader } from "./SiteHeader";
import { cn } from "@/lib/cn";

export function SiteLayout({
  children,
  overlay = true,
  className,
}: {
  children: ReactNode;
  overlay?: boolean;
  className?: string;
}) {
  return (
    <div className="relative flex min-h-screen flex-col">
      <SiteHeader overlay={overlay} />
      {/* `id` is the target of the skip-to-content link in app/layout.tsx.
          Without it that link points at nothing, which is worse than not
          having one: a keyboard user activates it and stays where they were.
          `tabIndex={-1}` lets the element take focus when jumped to without
          adding it to the tab order. */}
      <main id="main" tabIndex={-1} className={cn("flex flex-1 flex-col", className)}>
        {children}
      </main>
      <SiteFooter />
    </div>
  );
}
