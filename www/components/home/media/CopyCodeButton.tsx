"use client";

import { useState } from "react";
import { cn } from "@/lib/cn";
import { CopyIcon } from "@/components/icons";

const INSTALL = "curl -fsSL https://antifailure.dev/install.sh | sh";

/**
 * The install line, as a button that copies it.
 *
 * It carried a fixed `h-11` with no `whitespace-nowrap`, so the command wrapped
 * to three lines inside a control eleven units tall and the first and last
 * lines were clipped outside the pill. That was mine: the button was sized for
 * `npx antifailure init` and I put a fifty character curl command in it without
 * re-rendering the result.
 *
 * So: the text never wraps, the height is a floor rather than a fixture, and
 * the type steps down on narrow screens where fifty monospace characters plus
 * an icon will not otherwise fit inside 350px. `truncate` is the backstop, and
 * the full command goes to the clipboard whatever is shown.
 */
export function CopyCodeButton({
  code = INSTALL,
  copyText = INSTALL,
  variant = "white",
  className,
}: {
  code?: string;
  copyText?: string;
  variant?: "white" | "green" | "terminal";
  className?: string;
}) {
  const [copied, setCopied] = useState(false);

  return (
    <button
      type="button"
      aria-label={`Copy: ${copyText}`}
      className={cn(
        "group inline-flex min-h-11 max-w-full cursor-pointer items-center gap-x-3 overflow-hidden font-mono text-[13px] font-medium tracking-extra-tight whitespace-nowrap",
        "max-lg:text-[12px] max-sm:min-h-10 max-sm:gap-x-2 max-sm:text-[10.5px]",
        variant === "white" &&
          "w-[34.2%] justify-between rounded-full bg-white px-4 text-black hover:bg-[#F6FDFA] max-xl:w-[300px] max-lg:w-[36%] max-lg:px-3 max-sm:w-full",
        variant === "green" &&
          "justify-between rounded-full bg-[#34d59a] px-7 text-black hover:bg-[#47d18c] max-lg:px-5 max-sm:px-4",
        // For the dark panel: sized to its content, so the command is readable
        // rather than truncated, and quiet enough not to read as a third
        // call to action next to two pills.
        variant === "terminal" &&
          "w-auto justify-between gap-x-4 rounded-full border border-white/20 bg-white/[0.06] px-5 text-white/80 hover:border-white/45 hover:bg-white/[0.10] hover:text-white",
        className,
      )}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(copyText);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          // Clipboard access can be refused outright (insecure context, a
          // permissions policy, Firefox without the flag). Saying nothing is
          // better than a button that appears to have worked.
          setCopied(false);
        }
      }}
    >
      <span className="min-w-0 truncate">
        <span
          className={
            variant === "white"
              ? "text-black/40"
              : variant === "terminal"
                ? "text-white/40"
                : "text-black/50"
          }
        >
          ${" "}
        </span>
        {code}
      </span>
      <CopyIcon
        className={cn(
          "h-3.5 w-3.5 shrink-0",
          copied ? "opacity-100" : "opacity-60 group-hover:opacity-100",
        )}
      />
      <span className="sr-only" role="status">
        {copied ? "Copied" : "Copy"}
      </span>
    </button>
  );
}
