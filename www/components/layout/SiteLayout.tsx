import type { ReactNode } from "react";
import { SiteFooter } from "./SiteFooter";
import { SiteHeader } from "./SiteHeader";
import { cn } from "@/lib/cn";
import { SitewideSections } from "@/components/cms/SitewideSections";

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
      <main id="main" tabIndex={-1} className={cn("flex min-w-0 flex-1 flex-col", className)}>
        <SitewideSections>{children}</SitewideSections>
      </main>
      <SiteFooter />
    </div>
  );
}
