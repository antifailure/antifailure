import { cn } from "@/lib/cn";
import type { ElementType, ReactNode } from "react";

const sizes = {
  "1920": "max-w-[1856px] px-8",
  "1600": "max-w-[1600px] px-8",
  "1344": "max-w-[1408px] px-8",
} as const;

export function Container({
  size,
  className,
  as: Tag = "div",
  children,
}: {
  size: keyof typeof sizes;
  className?: string;
  as?: ElementType;
  children: ReactNode;
}) {
  return (
    <Tag className={cn("relative mx-auto max-lg:max-w-none max-lg:px-8 max-md:px-5", sizes[size], className)}>
      {children}
    </Tag>
  );
}
