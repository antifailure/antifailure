import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

/**
 * The mark on a panel whose numbers were written rather than measured.
 *
 * Every mocked panel on this site renders inside realistic chrome: a GitHub
 * check, a terminal, a report with a pull request number on it. That is the
 * right way to show a product and it is exactly why the numbers inside have to
 * say what they are. An audit of the site in August 2026 found four figures
 * presented as measurements, two of which this engine cannot produce at all,
 * on pages that carried no example label anywhere.
 *
 * One component rather than a line of grey text per panel, because a caption
 * written fresh each time drifts: it ends up quieter on the page where it
 * matters most. This one reads the same everywhere, and the copy under it says
 * what is real, not only what is not.
 */
export function Illustrative({
  label = "Illustrative",
  children,
  className,
}: {
  /** "Illustrative" for a shaped panel. "Example finding" for one report. */
  label?: "Illustrative" | "Example finding";
  /** What was written rather than measured. One sentence, specific. */
  children?: ReactNode;
  className?: string;
}) {
  return (
    <p
      className={cn(
        "mt-4 flex flex-wrap items-baseline gap-x-3 gap-y-1.5 text-[13px] leading-5 tracking-extra-tight text-gray-new-40",
        className,
      )}
    >
      <span className="shrink-0 border border-black/15 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.14em] text-black/55">
        {label}
      </span>
      {children ? <span className="min-w-0 max-w-[640px]">{children}</span> : null}
    </p>
  );
}
