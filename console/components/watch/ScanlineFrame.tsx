"use client";

import type { ReactNode } from "react";

/**
 * The film skin that wraps an agent pane, and nothing else on the page.
 *
 * A warm paper mat, a static scanline overlay, a faint vignette and a Menlo
 * caption strip, all defined in globals.css under .watch-*. It gives the watch
 * wall its own terminal identity without repainting the console around it. The
 * overlay is fixed: no animation, no pulse, on purpose.
 */
export function ScanlineFrame({
  caption,
  chip,
  children,
  footer,
}: {
  caption: ReactNode;
  chip?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <div className="watch-mat overflow-hidden rounded-lg border border-rule">
      <div className="watch-caption flex items-center justify-between gap-2 border-b border-[rgba(46,45,39,0.18)] px-3 py-2 text-[11px]">
        <span className="min-w-0 truncate">{caption}</span>
        {chip}
      </div>
      <div className="watch-screen watch-scanlines watch-vignette aspect-[16/10] w-full">
        {children}
      </div>
      {footer ? (
        <div className="watch-caption border-t border-[rgba(46,45,39,0.18)] px-3 py-2 text-[11px] text-[rgb(70,68,60)]">
          {footer}
        </div>
      ) : null}
    </div>
  );
}
