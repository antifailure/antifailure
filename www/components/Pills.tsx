"use client";

import { useState, type ReactNode } from "react";
import { CopyIcon } from "./icons";

export function CopyCli({
  command = "$ npx antifailure init",
  variant = "light",
}: {
  command?: string;
  variant?: "light" | "green" | "dark" | "mint";
}) {
  const [copied, setCopied] = useState(false);

  const styles =
    variant === "green"
      ? "h-11 rounded-full bg-[#33bf00] text-black"
      : variant === "mint"
        ? "h-12 min-w-[248px] justify-between rounded-[10px] bg-[#d7efe8] text-black"
        : variant === "light"
          ? "h-11 rounded-full bg-[#ececec] text-black"
          : "h-11 rounded-full border border-black/15 bg-black/5 text-black";

  return (
    <button
      type="button"
      className={`inline-flex items-center gap-3 px-5 font-mono text-[13px] ${styles}`}
      onClick={async () => {
        await navigator.clipboard.writeText(command.replace(/^\$\s/, ""));
        setCopied(true);
        setTimeout(() => setCopied(false), 1800);
      }}
    >
      {copied ? "Copied — not published yet" : command}
      <CopyIcon />
      <span className="sr-only">{copied ? "Copied" : "Copy"}</span>
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
