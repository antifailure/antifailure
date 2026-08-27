import Link from "next/link";
import { cn } from "@/lib/cn";
import type { ButtonHTMLAttributes, ReactNode } from "react";

const sizes = {
  new: "h-11 px-7 tracking-extra-tight max-lg:h-9 max-lg:px-[18px] max-lg:text-sm",
  xxs: "h-8 px-4 text-sm tracking-extra-tight font-medium",
} as const;

const themes = {
  filled: "bg-black text-white hover:bg-[#292929] font-medium",
  white: "bg-white text-black hover:bg-gray-new-80 font-medium",
  outlined:
    "border border-black/40 bg-black/[0.02] text-black hover:border-black",
  green: "bg-[#34d59a] text-black hover:bg-[#47d18c] font-medium",
} as const;

export function Button({
  href,
  theme = "filled",
  size = "new",
  className,
  children,
  type = "button",
  ...props
}: {
  href?: string;
  theme?: keyof typeof themes;
  size?: keyof typeof sizes;
  className?: string;
  children: ReactNode;
} & ButtonHTMLAttributes<HTMLButtonElement>) {
  const cls = cn(
    "inline-flex cursor-pointer items-center justify-center whitespace-nowrap rounded-full text-center leading-none transition-colors duration-200",
    sizes[size],
    themes[theme],
    className,
  );

  if (href) {
    return (
      <Link href={href} className={cls}>
        {children}
      </Link>
    );
  }

  return (
    <button type={type} className={cls} {...props}>
      {children}
    </button>
  );
}
