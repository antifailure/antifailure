"use client";

import type { ReactNode } from "react";

/**
 * The only chart in this console, and it is deliberately a bar.
 *
 * WHY NOT A CHART LIBRARY. Nothing here needs one. Every number on the
 * analytics page is a count over a bounded, closed vocabulary: ten acquisition
 * channels, twelve page shapes, five verdicts. A horizontal bar with the label
 * and the number written next to it answers all of them, reads on a phone
 * without a legend, and is a table to a screen reader.
 *
 * WHY NOT A LINE OVER TIME AS THE PRIMARY SHAPE. A line over a sparse series
 * draws a slope between two points that had nothing between them, which is a
 * claim the data does not make. The daily series is still shown, as a column
 * per day with a real zero for a day with nothing in it, so an empty day looks
 * empty rather than being interpolated across.
 *
 * NOTHING HERE ANIMATES. Not on load, not on hover, not ever. A bar that grows
 * when it appears is a bar whose length is wrong for the first half second,
 * which is exactly when somebody is reading it.
 */

export function Meter({
  label,
  value,
  max,
  note,
  tone = "neutral",
}: {
  label: ReactNode;
  value: number;
  /** The largest value in the group, so bars in one group are comparable. */
  max: number;
  /** A second number, written after the count. */
  note?: ReactNode;
  tone?: "neutral" | "accent";
}) {
  // A zero-length bar for a zero, and never a minimum width. A hairline of
  // colour for a count of nothing is the shape that makes somebody believe a
  // channel is working when it produced no visits at all.
  const width = max > 0 ? Math.round((value / max) * 100) : 0;

  return (
    <div className="py-2">
      <div className="flex items-baseline justify-between gap-4">
        <span className="min-w-0 truncate text-[13px] text-ink">{label}</span>
        <span className="shrink-0 tnum text-[13px] font-medium text-ink">
          {value.toLocaleString()}
          {note ? <span className="ml-2 font-normal text-dim">{note}</span> : null}
        </span>
      </div>
      <div className="mt-1.5 h-1.5 w-full rounded-sm bg-[rgba(16,16,16,0.06)]">
        <div
          className={`h-1.5 rounded-sm ${tone === "accent" ? "bg-neon" : "bg-[rgba(16,16,16,0.45)]"}`}
          style={{ width: `${width}%` }}
        />
      </div>
    </div>
  );
}

/**
 * A count per day, as one column per day.
 *
 * Every day in the window is present, including the ones with nothing, because
 * a series that skips its empty days draws a line straight across an outage.
 * A day with no events gets a one-pixel foot rather than nothing at all, so the
 * axis is visibly continuous and the difference between "no events" and "no
 * column" is not something a reader has to work out.
 *
 * The whole thing is also a table for a screen reader: the visual columns are
 * aria-hidden and the same numbers follow in a visually hidden list, because a
 * bar chart with no text alternative is a chart that is simply absent for
 * anybody using one.
 */
export function DayColumns({
  points,
  label,
}: {
  points: readonly { day: string; events: number }[];
  label: string;
}) {
  const max = points.reduce((m, p) => Math.max(m, p.events), 0);

  return (
    <div>
      <div className="flex h-24 items-end gap-[2px]" aria-hidden="true">
        {points.map((p) => (
          <div
            key={p.day}
            title={`${p.day}: ${p.events.toLocaleString()}`}
            className="min-w-0 flex-1 rounded-t-sm bg-[rgba(16,16,16,0.45)]"
            style={{ height: max > 0 ? `${Math.max(1, Math.round((p.events / max) * 96))}px` : "1px" }}
          />
        ))}
      </div>
      <div className="mt-2 flex items-baseline justify-between text-[11.5px] text-dim" aria-hidden="true">
        <span>{points[0]?.day ?? ""}</span>
        <span>{points[points.length - 1]?.day ?? ""}</span>
      </div>
      <ul className="sr-only">
        <li>{label}</li>
        {points.map((p) => (
          <li key={p.day}>
            {p.day}: {p.events}
          </li>
        ))}
      </ul>
    </div>
  );
}
