import Link from "next/link";
import { cn } from "@/lib/cn";
import type { ButtonHTMLAttributes, ReactNode } from "react";

const sizes = {
  // 44px tall and 16px of type at every width, including the narrow ones.
  // `max-lg:h-9 max-lg:text-sm` used to shrink this to 36px and 14px below
  // 1024, which is exactly backwards: the phone is where a thumb needs the
  // target and where small type costs the most, and it was the only place the
  // site's primary action was under 44px. The narrower horizontal padding
  // below `lg` stays, because that is what keeps two buttons on one line.
  new: "h-11 px-7 tracking-extra-tight max-lg:px-[18px]",
  xxs: "h-8 px-4 text-sm tracking-extra-tight font-medium",
} as const;

/**
 * A theme owns the whole colour of a button, including the one case that used
 * to be spelled as a className over `outlined`. That override happened to work:
 * text-white is emitted after text-black, so it won. Written the other way
 * round, a dark label over a light theme, it would have lost in silence and the
 * label would have disappeared into the button. A theme cannot lose that race,
 * because only one theme string is ever emitted.
 */
const themes = {
  filled: "bg-black text-white hover:bg-[#292929] font-medium",
  white: "bg-white text-black hover:bg-gray-new-80 font-medium",
  outlined:
    "border border-black/40 bg-black/[0.02] text-black hover:border-black",
  "outlined-inverse":
    "border border-white/40 bg-white/[0.02] text-white hover:border-white",
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
    const docs = href === "/docs" || href.startsWith("/docs/");
    if (docs) {
      return (
        <a href={href} className={cls}>
          {children}
        </a>
      );
    }
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
