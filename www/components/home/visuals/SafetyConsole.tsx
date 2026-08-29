"use client";

import { useRef } from "react";
import { cn } from "@/lib/cn";
import { FILM_EASE } from "@/components/home/visuals/primitives";
import { useDelayedFlag, useInViewPlay } from "@/lib/useInViewPlay";

const TABS = [
  { id: "report", label: "Report", Icon: ReportIcon },
  { id: "oracle", label: "Oracle", Icon: ChartIcon },
  { id: "fidelity", label: "Fidelity", Icon: CylinderIcon },
  { id: "twin", label: "Twin", Icon: BranchIcon },
  { id: "firewall", label: "Firewall", Icon: WaveIcon },
] as const;

const SIDEBAR = [
  { group: "Twin", value: "isolated-184", Icon: StarIcon },
  { group: "Safe State", value: "sanitized subset", Icon: BubbleIcon },
  { group: "Firewall", value: "fail closed", Icon: CylinderIcon },
  { group: "Oracle", value: "differential", Icon: CheckIcon },
] as const;

const ROWS = [
  {
    name: "fail_closed",
    condition: "Unknown egress, missing sanitization",
    finding: "BLOCK",
    expected: "Fail closed",
    fail: true,
  },
  {
    name: "boundary",
    condition: "Production data leaves the customer cloud",
    finding: "PASS",
    expected: "In-boundary",
    fail: false,
  },
  {
    name: "pr_gate",
    condition: "Preview URL treated as a ship decision",
    finding: "BLOCK",
    expected: "Pass / warn / block",
    fail: true,
  },
  {
    name: "cleanup",
    condition: "Resources journaled before create",
    finding: "PASS",
    expected: "Proven destroy",
    fail: false,
  },
  {
    name: "oracle",
    condition: "AI discovers. Scenarios decide.",
    finding: "PASS",
    expected: "Deterministic",
    fail: false,
  },
  {
    name: "migration",
    condition: "ACCESS EXCLUSIVE 27.4s on subscriptions",
    finding: "BLOCK",
    expected: "Rollback feasible",
    fail: true,
  },
] as const;

export function SafetyConsole() {
  const ref = useRef<HTMLDivElement>(null);
  const { story, reduced } = useInViewPlay(ref, 0.18);
  const lit = useDelayedFlag(story || reduced, reduced ? 0 : 280);

  return (
    <div
      ref={ref}
      className="pointer-events-none mt-16 select-none max-xl:mt-12 max-lg:mt-10"
      data-console="safety"
      aria-hidden
    >
      <div className="relative overflow-hidden rounded-[32px] bg-[#1a1a1a] max-md:rounded-[24px]">
        <div
          className="noise pointer-events-none absolute inset-0 opacity-70 mix-blend-overlay"
          aria-hidden
        />
        <div
          className="pointer-events-none absolute inset-0"
          style={{
            background:
              "radial-gradient(70% 50% at 8% 0%, rgba(255,255,255,0.09), transparent 52%)",
          }}
          aria-hidden
        />

        <div className="relative px-2.5 pt-2.5 pb-2.5 max-md:px-2 max-md:pt-2 max-md:pb-2">
          <TabRow />
          <div className="relative -mt-px overflow-hidden rounded-[24px] rounded-tl-none bg-[#0a0a0a] shadow-[0_0_0_1px_rgba(255,255,255,0.05)] max-md:rounded-[18px] max-md:rounded-tl-none">
            <Toolbar />
            <div className="grid min-h-[520px] grid-cols-[188px_1fr] max-lg:grid-cols-1 max-lg:min-h-0">
              <aside className="border-r border-white/[0.055] px-4 pt-5 pb-6 max-lg:hidden">
                {SIDEBAR.map((item) => (
                  <div key={item.group} className="mb-6 last:mb-0">
                    <div className="text-[11px] tracking-extra-tight text-white/28">{item.group}</div>
                    <div className="mt-1.5 flex items-center gap-2 text-[13px] tracking-extra-tight text-white/72">
                      <item.Icon />
                      {item.value}
                    </div>
                  </div>
                ))}
              </aside>
              <div className="relative min-h-[520px] max-lg:min-h-[380px]">
                <Table lit={lit} />
                <div
                  className="pointer-events-none absolute inset-x-0 bottom-0 h-[48%] bg-gradient-to-t from-[#0a0a0a] from-22% via-[#0a0a0a]/80 to-transparent"
                  aria-hidden
                />
                <div className="absolute inset-x-0 bottom-0 flex flex-col items-center px-8 pb-9 pt-16 text-center max-md:px-5 max-md:pb-7 max-md:pt-12">
                  <div className="text-[28px] font-semibold tracking-extra-tight text-white max-md:text-[20px]">
                    Safety properties
                  </div>
                  <p className="mt-2.5 max-w-[440px] text-[14px] leading-5 tracking-extra-tight text-white/42 max-md:text-[12px]">
                    The platform answers whether this deployment is safe to ship under the
                    conditions that actually matter.
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function TabRow() {
  return (
    <div className="relative z-10 flex items-end gap-1.5">
      <FolderTab />
      {TABS.slice(1).map((tab) => (
        <span
          key={tab.id}
          className="mb-[11px] inline-flex h-[30px] items-center gap-1.5 rounded-full bg-black/25 px-3 text-[10.5px] font-medium tracking-[0.1em] text-white/38 uppercase ring-1 ring-white/[0.1] max-md:mb-2 max-md:h-7 max-md:px-2.5 max-md:text-[10px] max-sm:hidden"
        >
          <tab.Icon className="text-white/32" />
          {tab.label}
        </span>
      ))}
    </div>
  );
}

function FolderTab() {
  return (
    <span className="relative inline-flex h-12 w-[158px] items-center max-md:h-10 max-md:w-[132px]">
      <svg
        viewBox="0 0 158 48"
        className="absolute inset-0 h-full w-full"
        preserveAspectRatio="none"
        aria-hidden
      >
        <path
          d="M0 48 V16 C0 7.2 7.2 0 16 0 H108 C122 0 126 8 136 20 C144 32 150 42 158 48 Z"
          fill="#0a0a0a"
        />
      </svg>
      <span className="relative z-10 flex items-center gap-2 pl-[18px] text-[11px] font-medium tracking-[0.12em] text-white/85 uppercase max-md:pl-3.5 max-md:text-[10px]">
        <ReportIcon className="text-white/85" />
        Report
      </span>
    </span>
  );
}

function Toolbar() {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-white/[0.06] px-4 py-2.5 max-md:px-3">
      <div className="flex items-center gap-2">
        <span className="inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-[12px] tracking-extra-tight text-white/45 ring-1 ring-white/[0.08]">
          <CalendarIcon />
          Last 24 hours
        </span>
        <span className="inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-[12px] tracking-extra-tight text-white/45 ring-1 ring-white/[0.08] max-sm:hidden">
          <FilterIcon />
          Add filter
        </span>
      </div>
      <div className="truncate text-[12px] tracking-extra-tight text-white/35">
        pr 184 <span className="text-white/20">/</span>{" "}
        <span className="text-white/55">add_billing_status</span>
      </div>
    </div>
  );
}

function Table({ lit }: { lit: boolean }) {
  return (
    <div className="font-sans text-[12px] tracking-extra-tight max-md:overflow-x-auto">
      <div className="grid grid-cols-[minmax(108px,0.9fr)_1.5fr_0.7fr_1fr_52px] gap-x-3 border-b border-white/[0.06] px-4 py-2 text-[11px] text-white/30 max-md:grid-cols-[minmax(0,0.9fr)_minmax(0,1.2fr)_auto] max-md:px-3">
        <span>Name</span>
        <span>Condition</span>
        <span>Finding</span>
        <span className="max-md:hidden">Expected</span>
        <span className="text-right max-md:hidden">Tags</span>
      </div>
      {ROWS.map((row, i) => {
        const fail = row.fail && lit;
        return (
          <div
            key={row.name}
            className={cn(
              "grid grid-cols-[minmax(108px,0.9fr)_1.5fr_0.7fr_1fr_52px] items-center gap-x-3 px-4 py-3 max-md:grid-cols-[minmax(0,0.9fr)_minmax(0,1.2fr)_auto] max-md:px-3 max-md:py-2.5",
              i < ROWS.length - 1 && "border-b border-white/[0.04]",
            )}
            style={{
              background: fail
                ? "rgba(118, 24, 28, 0.52)"
                : i % 2 === 1
                  ? "rgba(255,255,255,0.015)"
                  : "transparent",
              transition: `background 520ms ${FILM_EASE}`,
              transitionDelay: fail ? `${i * 70}ms` : "0ms",
            }}
          >
            <span className="flex min-w-0 items-center gap-2 text-white/70">
              {row.fail ? <FailMark on={fail} /> : <DottedCircle />}
              <span className="truncate">{row.name}</span>
            </span>
            <span className="truncate text-white/45">{row.condition}</span>
            <span
              className={cn(
                "truncate",
                row.fail ? "text-red-300/90" : "text-white/40",
              )}
            >
              {row.finding}
            </span>
            <span className="truncate text-white/40 max-md:hidden">{row.expected}</span>
            <span className="text-right text-white/25 max-md:hidden">—</span>
          </div>
        );
      })}
    </div>
  );
}

function FailMark({ on }: { on: boolean }) {
  return (
    <span
      className="relative inline-flex size-[15px] shrink-0 items-center justify-center"
      style={{
        opacity: on ? 1 : 0.35,
        transition: `opacity 400ms ${FILM_EASE}`,
      }}
    >
      <span className="absolute inset-0 rounded-full bg-[#e23b3b]/20" />
      <svg viewBox="0 0 14 14" className="relative size-[15px] text-[#ff5a5a]" fill="none" aria-hidden>
        <circle cx="7" cy="7" r="5.4" stroke="currentColor" strokeWidth="1.2" />
        <path d="M7 4.2v3.3" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
        <circle cx="7" cy="9.6" r="0.7" fill="currentColor" />
      </svg>
    </span>
  );
}

function DottedCircle() {
  return (
    <svg viewBox="0 0 14 14" className="size-[15px] shrink-0 text-white/30" fill="none" aria-hidden>
      <circle cx="7" cy="7" r="5.2" stroke="currentColor" strokeWidth="1.15" strokeDasharray="1.6 1.7" />
    </svg>
  );
}

function ReportIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={cn("size-3.5 shrink-0", className)} fill="none" aria-hidden>
      <circle cx="8" cy="8" r="5.5" stroke="currentColor" strokeWidth="1.2" strokeDasharray="2.4 1.6" />
      <circle cx="8" cy="8" r="1.4" fill="currentColor" />
    </svg>
  );
}

function ChartIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={cn("size-3.5 shrink-0", className)} fill="none" aria-hidden>
      <path d="M2.5 12.5 6 8l2.5 2.5 5-6" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function CylinderIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={cn("size-3.5 shrink-0", className)} fill="none" aria-hidden>
      <ellipse cx="8" cy="4.2" rx="4.2" ry="1.6" stroke="currentColor" strokeWidth="1.2" />
      <path d="M3.8 4.2v7.2c0 .9 1.9 1.6 4.2 1.6s4.2-.7 4.2-1.6V4.2" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function BranchIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={cn("size-3.5 shrink-0", className)} fill="none" aria-hidden>
      <circle cx="5" cy="3.5" r="1.4" stroke="currentColor" strokeWidth="1.2" />
      <circle cx="5" cy="12.5" r="1.4" stroke="currentColor" strokeWidth="1.2" />
      <circle cx="11.5" cy="8" r="1.4" stroke="currentColor" strokeWidth="1.2" />
      <path d="M5 4.9v6.2M5 8h5" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function WaveIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={cn("size-3.5 shrink-0", className)} fill="none" aria-hidden>
      <path d="M2 8c1.4-3 2.6-3 4 0s2.6 3 4 0 2.6-3 4 0" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function StarIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-3.5 shrink-0 text-white/40" fill="none" aria-hidden>
      <path
        d="M8 2.4 9.5 6h3.6L10.6 8.4l1.2 3.6L8 10.1 4.2 12l1.2-3.6L2.9 6h3.6L8 2.4Z"
        stroke="currentColor"
        strokeWidth="1.1"
      />
    </svg>
  );
}

function BubbleIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-3.5 shrink-0 text-white/40" fill="none" aria-hidden>
      <path
        d="M3.5 4.2h9v6.2H7.2L4.4 12.6V10.4H3.5V4.2Z"
        stroke="currentColor"
        strokeWidth="1.2"
      />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-3.5 shrink-0 text-white/40" fill="none" aria-hidden>
      <rect x="3" y="3" width="10" height="10" rx="1.4" stroke="currentColor" strokeWidth="1.2" />
      <path d="M5.4 8.1 7.2 9.8 10.6 6.2" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function CalendarIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-3.5 shrink-0 text-white/40" fill="none" aria-hidden>
      <rect x="2.6" y="3.6" width="10.8" height="9.6" rx="1.2" stroke="currentColor" strokeWidth="1.2" />
      <path d="M2.6 6.6h10.8M5.4 2.6v2.2M10.6 2.6v2.2" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function FilterIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-3.5 shrink-0 text-white/40" fill="none" aria-hidden>
      <path d="M3 4.2h10M5 8h6M6.5 11.8h3" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  );
}
