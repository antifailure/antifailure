import { cn } from "@/lib/cn";
import type { ReactNode } from "react";

export function SectionLabel({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex items-center gap-2 text-black/70 max-md:gap-1.5", className)}>
      <svg
        viewBox="0 0 12 12"
        className="size-3 flex-none text-[#33bf00] max-md:size-2.5"
        fill="none"
        aria-hidden
      >
        <path
          d="M1.8 6h8M6.6 3.2 9.8 6 6.6 8.8"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      <span className="font-mono text-xs font-medium uppercase leading-none max-md:text-[10px]">
        {children}
      </span>
    </div>
  );
}
