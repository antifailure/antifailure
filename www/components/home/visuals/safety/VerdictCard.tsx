"use client";

import { useRef } from "react";
import { u } from "../SafetyCards";
import { useInViewPlay } from "@/lib/useInViewPlay";

/**
 * Authored against the 288x350 reference card. This visual is mounted in the
 * region below the card heading, so local y = 0 is reference y = 90.
 *
 * Log pane left = card x 136; pills at 148, width 136, pitch 19.5.
 * Mark center = (79.5, 112) of this box / card (79.5, 202).
 */
const ROW_H = 13;
const ROW_PITCH = 19.5;
const ROW_W = 136;
const PANE_X = 136;
const LOG_X = 148;
const MARK_X = 79.5;
const MARK_Y = 112;
const OUTER_R = 33;
const INNER_R = 20.5;
const MARK_SIZE = 24;

type Level = "PASS" | "WARN" | "BLOCK" | "STEP";

const TONE: Record<Level, { color: string; background: string }> = {
  PASS: { color: "#285D49", background: "rgba(51,191,0,0.12)" },
  WARN: { color: "#92400e", background: "rgba(217,119,6,0.12)" },
  BLOCK: { color: "#b91c1c", background: "rgba(220,38,38,0.10)" },
  STEP: { color: "#797d86", background: "transparent" },
};

const ROWS: { level: Level; time: string }[] = [
  { level: "STEP", time: "4:25:29 PM" },
  { level: "PASS", time: "4:25:30 PM" },
  { level: "STEP", time: "4:25:31 PM" },
  { level: "STEP", time: "4:25:33 PM" },
  { level: "WARN", time: "4:25:34 PM" },
  { level: "STEP", time: "4:25:35 PM" },
  { level: "PASS", time: "4:25:37 PM" },
  { level: "STEP", time: "4:25:38 PM" },
  { level: "STEP", time: "4:25:39 PM" },
  { level: "BLOCK", time: "4:25:41 PM" },
  { level: "STEP", time: "4:25:42 PM" },
  { level: "WARN", time: "4:25:43 PM" },
  { level: "PASS", time: "4:25:45 PM" },
  { level: "STEP", time: "4:25:46 PM" },
];

const LOOP_SHIFT = u(ROWS.length * ROW_PITCH);
const KEYFRAMES = `@keyframes af-verdict-stream{from{transform:translate3d(0,0,0)}to{transform:translate3d(0,-${LOOP_SHIFT},0)}}`;

function LogRow({ level, time }: { level: Level; time: string }) {
  const tone = TONE[level];
  return (
    <div
      className="flex shrink-0 items-center justify-between border border-black/[0.08] bg-white"
      style={{
        width: u(ROW_W),
        height: u(ROW_H),
        borderRadius: u(ROW_H / 2),
        paddingLeft: u(3.5),
        paddingRight: u(6),
      }}
    >
      <span
        className="font-sans tracking-extra-tight"
        style={{
          color: tone.color,
          background: tone.background,
          borderRadius: u(3),
          paddingLeft: u(2.5),
          paddingRight: u(2.5),
          paddingTop: u(1.25),
          paddingBottom: u(1.25),
          fontSize: u(6.5),
          lineHeight: 1,
        }}
      >
        {level}
      </span>
      <span
        className="font-sans tabular-nums tracking-extra-tight text-gray-new-50"
        style={{ fontSize: u(6.5), lineHeight: 1 }}
      >
        {time}
      </span>
    </div>
  );
}

export function VerdictCard() {
  const ref = useRef<HTMLDivElement>(null);
  const { reduced, story } = useInViewPlay(ref, 0.2);

  return (
    <div className="absolute inset-0 font-sans select-none" ref={ref} aria-hidden>
      <style>{KEYFRAMES}</style>

      <div
        className="absolute rounded-full bg-white"
        style={{
          left: u(MARK_X - OUTER_R),
          top: u(MARK_Y - OUTER_R),
          width: u(OUTER_R * 2),
          height: u(OUTER_R * 2),
        }}
      />
      <div
        className="absolute rounded-full border border-black/[0.08]"
        style={{
          left: u(MARK_X - OUTER_R),
          top: u(MARK_Y - OUTER_R),
          width: u(OUTER_R * 2),
          height: u(OUTER_R * 2),
        }}
      />
      <div
        className="absolute rounded-full border border-black/[0.08]"
        style={{
          left: u(MARK_X - INNER_R),
          top: u(MARK_Y - INNER_R),
          width: u(INNER_R * 2),
          height: u(INNER_R * 2),
        }}
      />
      <svg
        viewBox="0 0 18 18"
        fill="none"
        className="absolute"
        style={{
          left: u(MARK_X - MARK_SIZE / 2),
          top: u(MARK_Y - MARK_SIZE / 2),
          width: u(MARK_SIZE),
          height: u(MARK_SIZE),
        }}
      >
        <path
          d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6"
          stroke="#33bf00"
          strokeWidth="2.1"
          strokeLinecap="square"
        />
      </svg>

      <div
        className="absolute h-px"
        style={{
          left: u(MARK_X + OUTER_R),
          top: u(MARK_Y),
          width: u(PANE_X - MARK_X - OUTER_R),
          background: "rgba(0,0,0,0.10)",
        }}
      />

      <div
        className="absolute"
        style={{
          left: u(PANE_X),
          top: 0,
          right: 0,
          bottom: 0,
          opacity: story ? 1 : 0,
          transition: reduced ? undefined : "opacity 600ms ease",
        }}
      >
        <div
          className="absolute inset-0 overflow-hidden"
          style={{
            maskImage:
              "linear-gradient(to bottom, transparent 0%, #000 18%, #000 76%, transparent 100%)",
            WebkitMaskImage:
              "linear-gradient(to bottom, transparent 0%, #000 18%, #000 76%, transparent 100%)",
          }}
        >
          <div
            className="flex flex-col"
            style={{
              paddingLeft: u(LOG_X - PANE_X),
              paddingTop: u(8),
              gap: u(ROW_PITCH - ROW_H),
              animation: reduced ? undefined : "af-verdict-stream 54s linear infinite",
              animationPlayState: reduced ? "paused" : "running",
            }}
          >
            {ROWS.map((row, i) => (
              <LogRow key={`a-${i}`} level={row.level} time={row.time} />
            ))}
            {ROWS.map((row, i) => (
              <LogRow key={`b-${i}`} level={row.level} time={row.time} />
            ))}
          </div>
        </div>
        <div
          className="pointer-events-none absolute inset-x-0 top-0"
          style={{
            height: u(48),
            background: "linear-gradient(to bottom, #fff 12%, rgba(255,255,255,0) 100%)",
          }}
        />
        <div
          className="pointer-events-none absolute inset-x-0 bottom-0"
          style={{
            height: u(56),
            background: "linear-gradient(to top, #fff 18%, rgba(255,255,255,0) 100%)",
          }}
        />
      </div>
    </div>
  );
}
