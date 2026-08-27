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
      <main className={cn("flex flex-1 flex-col", className)}>{children}</main>
      <SiteFooter />
    </div>
  );
}
