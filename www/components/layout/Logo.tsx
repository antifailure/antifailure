"use client";

import Link from "next/link";
import { cn } from "@/lib/cn";
import { CmsMedia, CmsText } from "@/components/cms/Editable";
import { useCmsString } from "@/components/cms/CmsProvider";

export function Logo({ className }: { className?: string }) {
  const href = useCmsString("header.logo.href", "/", { label: "Logo destination", sectionId: "header", kind: "url" });
  const label = useCmsString("header.logo.label", "Antifailure", { label: "Logo accessible label", sectionId: "header" });
  return (
    <Link prefetch={false}
      href={href}
      // `h-11` rather than the 32px the mark and wordmark happen to occupy:
      // this is the only link in the phone header besides the menu button, and
      // that button is already `size-11`. The contents are centred, so the
      // extra height is hit area and nothing moves.
      className={cn("flex h-11 shrink-0 items-center gap-2.5", className)}
      aria-label={label}
    >
      <CmsMedia cmsKey="header.logo.image" label="Header logo" sectionId="header" className="h-6 w-6 shrink-0">
      <svg viewBox="0 0 18 18" className="h-6 w-6 shrink-0" fill="none" aria-hidden>
        <path
          d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6"
          stroke="#33bf00"
          strokeWidth="2.1"
          strokeLinecap="square"
        />
      </svg>
      </CmsMedia>
      <CmsText cmsKey="header.logo.wordmark" label="Wordmark" sectionId="header" defaultValue="Antifailure" as="span" className="text-[16px] font-medium leading-none tracking-extra-tight text-black" />
    </Link>
  );
}
