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
    <div className={cn("flex h-3.5 items-end gap-2 text-black/70 max-md:h-2.5 max-md:gap-1.5", className)}>
      <span className="block h-3.5 w-3 flex-none text-[#33bf00] max-md:size-2.5" aria-hidden>
        →
      </span>
      <span className="font-mono text-xs font-medium uppercase leading-none max-md:text-[10px]">
        {children}
      </span>
    </div>
  );
}
