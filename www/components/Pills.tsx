"use client";

import { useState, type ReactNode } from "react";
import { CopyIcon } from "./icons";

export function CopyCli({
  command = "$ curl -fsSL https://antifailure.dev/install.sh | sh",
  variant = "light",
}: {
  command?: string;
  variant?: "light" | "green" | "dark" | "mint";
}) {
  const [copied, setCopied] = useState(false);

  // Every variant used to pin a height (`h-11`, `h-12`) with no
  // `whitespace-nowrap`, so the fifty character install command wrapped to
  // three lines inside a fixed box and the first and last were clipped outside
  // it. Heights are floors now, the command never wraps, and the type steps
  // down where fifty monospace characters will not otherwise fit.
  const styles =
    variant === "green"
      ? "min-h-11 rounded-full bg-[#33bf00] text-black"
      : variant === "mint"
        ? "min-h-12 max-w-full justify-between rounded-[10px] bg-[#d7efe8] text-black"
        : variant === "light"
          ? "min-h-11 rounded-full bg-[#ececec] text-black"
          : "min-h-11 rounded-full border border-black/15 bg-black/5 text-black";

  return (
    <button
      type="button"
      aria-label={`Copy: ${command.replace(/^\$\s/, "")}`}
      className={`inline-flex max-w-full cursor-pointer items-center gap-3 overflow-hidden px-5 font-mono text-[13px] whitespace-nowrap max-sm:gap-2 max-sm:px-4 max-sm:text-[10.5px] ${styles}`}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(command.replace(/^\$\s/, ""));
          setCopied(true);
          setTimeout(() => setCopied(false), 1800);
        } catch {
          setCopied(false);
        }
      }}
    >
      <span className="min-w-0 truncate">{copied ? "Copied" : command}</span>
      <CopyIcon className="h-3.5 w-3.5 shrink-0" />
      <span className="sr-only" role="status">
        {copied ? "Copied" : "Copy"}
      </span>
    </button>
  );
}

export function Pill({
  href,
  onClick,
  children,
  variant = "solid",
}: {
  href?: string;
  onClick?: () => void;
  children: ReactNode;
  variant?: "solid" | "ghost" | "green";
}) {
  const cls =
    variant === "solid"
      ? "bg-black text-white"
      : variant === "green"
        ? "bg-[#33bf00] text-black"
        : "border border-black bg-transparent text-black";
  const className = `inline-flex h-11 items-center justify-center rounded-full px-6 text-[14.5px] font-medium ${cls}`;
  if (onClick) {
    return (
      <button type="button" onClick={onClick} className={className}>
        {children}
      </button>
    );
  }
  return (
    <a href={href ?? "#top"} className={className}>
      {children}
    </a>
  );
}
