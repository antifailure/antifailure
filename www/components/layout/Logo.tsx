import Link from "next/link";
import { cn } from "@/lib/cn";

export function Logo({ className }: { className?: string }) {
  return (
    <Link
      href="/"
      className={cn("flex h-8 shrink-0 items-center gap-2.5", className)}
      aria-label="Antifailure"
    >
      <svg viewBox="0 0 18 18" className="h-6 w-6 shrink-0" fill="none" aria-hidden>
        <path
          d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6"
          stroke="#33bf00"
          strokeWidth="2.1"
          strokeLinecap="square"
        />
      </svg>
      <span className="text-[16px] font-medium leading-none tracking-extra-tight text-black">
        Antifailure
      </span>
    </Link>
  );
}
