"use client";

import { useState } from "react";
import { cn } from "@/lib/cn";
import { CopyIcon } from "@/components/icons";

export function CopyCodeButton({
  code = "npx antifailure init",
  copyText = "npx antifailure init",
  variant = "white",
  className,
}: {
  code?: string;
  copyText?: string;
  variant?: "white" | "green";
  className?: string;
}) {
  const [copied, setCopied] = useState(false);

  return (
    <button
      type="button"
      className={cn(
        "group inline-flex h-11 items-center gap-x-3 font-mono text-[13px] font-medium tracking-extra-tight",
        variant === "white" &&
          "w-[34.2%] justify-between rounded-none bg-white px-4 text-black hover:bg-[#F6FDFA] max-xl:w-[300px] max-lg:w-[36%] max-lg:px-3 max-sm:w-full",
        variant === "green" &&
          "rounded-full bg-[#34d59a] px-7 text-black hover:bg-[#47d18c] max-lg:h-9 max-lg:px-[18px]",
        className,
      )}
      onClick={async () => {
        await navigator.clipboard.writeText(copyText);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
    >
      {variant === "white" ? (
        <span className="text-black/40">
          $ <span className="text-black">{code}</span>
        </span>
      ) : (
        <span>$ {code}</span>
      )}
      <CopyIcon className={cn("h-3.5 w-3.5", copied ? "opacity-100" : "opacity-60 group-hover:opacity-100")} />
      <span className="sr-only">{copied ? "Copied" : "Copy"}</span>
    </button>
  );
}
