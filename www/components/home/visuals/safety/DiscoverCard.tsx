"use client";

import { useRef } from "react";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { u } from "../SafetyCards";

/**
 * Waveform of the route mix, measured off the 288x351 reference crop.
 * Local y = 0 is card y = 90. 17 capsules, width 10, vertically centered on
 * y = 121.
 *
 * NO CAPSULE IS CUT BY THE CARD. It used to claim a bleed it did not have: at
 * pitch 15.5 from origin -1 the run spanned -1 to 288 across a 288 wide card,
 * so the first capsule lost one pixel and the last ended flush with the edge
 * with nothing cut off it at all. I then read "cut off the last bars on either
 * side" as an instruction to cut them properly and moved the run to -5 at
 * pitch 16, which put half of each end capsule outside the card. That was the
 * wrong half of the instruction. The end capsules are DELETED.
 *
 * So 17 capsules rather than 19, origin 11 at pitch 16. The last one starts at
 * 11 + 16 x 16 = 267 and ends at 277, which leaves 11 either side: the run is
 * inset and symmetric, and the overflow clip on the card now has nothing to
 * take.
 */
const BAR_LEFT = 11;
const BAR_PITCH = 16;
const BAR_WIDTH = 10;

/** [top, bottom] per bar, in reference pixels from the top of this box. */
const BARS: readonly (readonly [number, number])[] = [
  [100, 142],
  [75, 167],
  [50, 192],
  [84, 158],
  [33, 209],
  [70, 172],
  [88, 154],
  [24.5, 217.5],
  [60, 182],
  [40, 202],
  [81, 161],
  [54, 188],
  [92, 150],
  [36, 206],
  [67, 175],
  [85, 157],
  [47, 195],
];

const BAR_GRADIENT = "linear-gradient(180deg, #33bf00 0%, #4CB782 42%, #E0A21A 78%, #d97706 100%)";

const PILL_TOP = 184;
const PILL_W = 143;
const PILL_H = 36.5;

export function DiscoverCard() {
  const ref = useRef<HTMLDivElement>(null);
  const { story, reduced } = useInViewPlay(ref);
  const grown = reduced || story;

  return (
    <div className="absolute inset-0 font-sans select-none" ref={ref} aria-hidden>
      {BARS.map(([top, bottom], i) => (
        <span
          className="absolute block origin-bottom will-change-transform"
          key={i}
          style={{
            left: u(BAR_LEFT + i * BAR_PITCH),
            top: u(top),
            width: u(BAR_WIDTH),
            height: u(bottom - top),
            borderRadius: u(BAR_WIDTH / 2),
            background: BAR_GRADIENT,
            opacity: grown ? 1 : 0,
            transform: grown ? "scaleY(1)" : "scaleY(0.14)",
            transition: reduced
              ? "none"
              : `transform 620ms cubic-bezier(0.16, 1, 0.3, 1) ${i * 18}ms, opacity 260ms ease-out ${i * 18}ms`,
          }}
        />
      ))}

      <div
        className="absolute z-[1] flex items-center border border-black/[0.08] bg-white shadow-[0_6px_20px_rgba(0,0,0,0.10)]"
        style={{
          left: "50%",
          top: u(PILL_TOP),
          width: u(PILL_W),
          height: u(PILL_H),
          borderRadius: u(PILL_H / 2),
          paddingLeft: u(9),
          paddingRight: u(10),
          gap: u(6),
          opacity: grown ? 1 : 0,
          transform: "translateX(-50%)",
          transition: reduced ? "none" : "opacity 420ms ease-out 460ms",
        }}
      >
        <span
          className="flex shrink-0 items-center justify-center rounded-full bg-[#33bf00]/[0.10]"
          style={{ width: u(16), height: u(16) }}
        >
          <svg fill="none" style={{ width: u(9.5), height: u(9.5) }} viewBox="0 0 18 18">
            <path
              d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6"
              stroke="#33bf00"
              strokeLinecap="square"
              strokeWidth="2.1"
            />
          </svg>
        </span>
        <span
          className="whitespace-nowrap font-sans font-medium leading-none tracking-extra-tight text-black-pure"
          style={{ fontSize: u(8.6), letterSpacing: u(-0.14) }}
        >
          Worst regression first
        </span>
      </div>
    </div>
  );
}
