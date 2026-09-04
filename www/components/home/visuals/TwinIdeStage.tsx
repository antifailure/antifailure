"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { clamp, EASE_OUT_CUBIC } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";

const DURATION_MS = 7000;
const START_DELAY_MS = 180;
const CLEAR_LEAVE_MS = 1800;

function span(t: number, start: number, end: number) {
  return clamp((t - start) / (end - start));
}

function ease(t: number) {
  return EASE_OUT_CUBIC(clamp(t));
}

function lerp(from: number, to: number, t: number) {
  return from + (to - from) * ease(t);
}

function cursorAt(t: number) {
  if (t < 0.2) {
    return { x: lerp(168, 292, span(t, 0.04, 0.2)), y: lerp(150, 180, span(t, 0.04, 0.2)), click: false };
  }

  if (t < 0.4) {
    return { x: lerp(292, 622, span(t, 0.2, 0.4)), y: lerp(180, 114, span(t, 0.2, 0.4)), click: t > 0.23 && t < 0.29 };
  }

  if (t < 0.68) {
    return { x: lerp(622, 535, span(t, 0.4, 0.68)), y: lerp(114, 285, span(t, 0.4, 0.68)), click: false };
  }

  return { x: lerp(535, 704, span(t, 0.68, 1)), y: lerp(285, 238, span(t, 0.68, 1)), click: t > 0.69 && t < 0.74 };
}

function Pill({ children, tone = "neutral" }: { children: ReactNode; tone?: "neutral" | "good" | "bad" }) {
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center rounded-[6px] border px-2 text-[11px] font-medium leading-none tracking-tight",
        tone === "neutral" && "border-black/10 bg-white text-black/45",
        tone === "good" && "border-[#668f5d]/28 bg-[#f3f8ef] text-[#3f7139]",
        tone === "bad" && "border-[#bd584e]/26 bg-[#fff4f1] text-[#8f3f38]",
      )}
    >
      {children}
    </span>
  );
}

function Cursor({ t, compact = false }: { t: number; compact?: boolean }) {
  const c = cursorAt(t);
  const x = compact ? clamp(c.x * 0.36, 56, 230) : c.x;
  const y = compact ? clamp(c.y * 0.62, 62, 260) : c.y;

  return (
    <div
      className="pointer-events-none absolute z-30 will-change-transform"
      style={{ transform: `translate3d(${x}px, ${y}px, 0)` }}
    >
      <svg
        viewBox="0 0 28 28"
        className={cn("drop-shadow-[0_8px_16px_rgba(0,0,0,0.18)]", compact ? "size-6" : "size-7")}
        aria-hidden
      >
        <path d="M5 3.5 22.6 16l-8.1 1.2-4.1 7.4Z" fill="#111" />
        <path d="M7.1 6.9 18.3 15l-5.5.8-2.7 4.7Z" fill="white" opacity="0.96" />
      </svg>
      <span
        className={cn(
          "absolute left-3 top-3 size-8 rounded-full border border-[#668f5d]/40 bg-[#668f5d]/10 opacity-0 transition-opacity duration-150",
          c.click && "opacity-100",
        )}
      />
    </div>
  );
}

function MetricRow({
  label,
  value,
  tone = "good",
  progress,
  compact = false,
}: {
  label: string;
  value: string;
  tone?: "good" | "bad" | "warn";
  progress: number;
  compact?: boolean;
}) {
  const color = tone === "bad" ? "#bd584e" : tone === "warn" ? "#9a8f4f" : "#668f5d";

  return (
    <div className={cn("grid items-center gap-3", compact ? "grid-cols-[1fr_auto]" : "grid-cols-[1fr_92px_42px]")}>
      <span className={cn("font-medium tracking-tight text-black/50", compact ? "text-[12px]" : "text-[15px]")}>{label}</span>
      {!compact ? (
        <span className="h-2 overflow-hidden rounded-full bg-black/[0.055]">
          <span className="block h-full rounded-full" style={{ width: `${clamp(progress) * 100}%`, backgroundColor: color }} />
        </span>
      ) : null}
      <span className={cn("text-right font-medium tracking-tight text-black/72", compact ? "text-[12px]" : "text-[15px]")}>{value}</span>
    </div>
  );
}

function SafetyRing({ t, compact = false }: { t: number; compact?: boolean }) {
  const report = ease(span(t, 0.75, 0.96));
  const score = Math.round(72 - report * 44);
  const size = compact ? "size-[108px]" : "size-[185px]";
  const view = compact ? 120 : 200;
  const center = compact ? 60 : 100;
  const radius = compact ? 45 : 76;
  const stroke = compact ? 11 : 16;

  return (
    <div className={cn("relative grid place-items-center", size)}>
      <svg viewBox={`0 0 ${view} ${view}`} className="absolute inset-0 size-full -rotate-90" aria-hidden>
        <circle cx={center} cy={center} r={radius} fill="none" stroke="rgba(0,0,0,0.045)" strokeWidth={stroke} />
        <circle
          cx={center}
          cy={center}
          r={radius}
          fill="none"
          stroke={report > 0.7 ? "#bd584e" : "#668f5d"}
          strokeWidth={stroke}
          strokeLinecap="round"
          pathLength="1"
          strokeDasharray={`${lerp(0.78, 0.31, report)} 1`}
        />
      </svg>
      <div className="text-center">
        <div className={cn("font-medium leading-none tracking-[-0.055em] text-black", compact ? "text-[30px]" : "text-[43px]")}>
          {score}<span className={cn("text-black/42", compact ? "text-[14px]" : "text-[22px]")}>/100</span>
        </div>
        <div
          className={cn(
            "mx-auto mt-3 w-fit rounded-[7px] border px-3 py-1 text-[13px] font-medium tracking-tight",
            report > 0.7 ? "border-[#bd584e]/24 bg-[#fff4f1] text-[#8f3f38]" : "border-[#668f5d]/22 bg-[#f3f8ef] text-[#3f7139]",
          )}
        >
          {report > 0.7 ? "Block" : "Checking"}
        </div>
      </div>
    </div>
  );
}

function Timeline({ t, compact = false }: { t: number; compact?: boolean }) {
  const report = ease(span(t, 0.76, 0.92));
  const timeline = ease(span(t, 0.18, 0.72));
  const labels = compact ? ["PR", "Run", "Replay", "Report"] : ["Current release", "Candidate PR", "Checkout replay", "Report"];

  return (
    <div className={compact ? "mt-7" : "mt-9"}>
      <div className={cn("flex items-center justify-between font-medium tracking-tight text-black/46", compact ? "text-[11px]" : "text-[14px]")}>
        {labels.map((label) => (
          <span key={label}>{label}</span>
        ))}
      </div>
      <div className={cn("relative", compact ? "mt-4 h-8" : "mt-5 h-14")}>
        <div className={cn("absolute rounded-full bg-black/10", compact ? "inset-x-3 top-3 h-1" : "left-8 right-8 top-5 h-1")} />
        <div
          className={cn("absolute rounded-full bg-[#668f5d]", compact ? "left-3 top-3 h-1" : "left-8 top-5 h-1")}
          style={{ width: `${timeline * 82}%` }}
        />
        {[0, 0.32, 0.62, 1].map((position, index) => {
          const activeDot = timeline >= position - 0.04;
          const failed = index === 3 && report > 0.55;
          return (
            <span
              key={position}
              className={cn("absolute grid -translate-x-1/2 place-items-center rounded-full border bg-white", compact ? "top-0 size-7" : "top-2 size-7")}
              style={{
                left: `${compact ? 7 + position * 86 : 8 + position * 84}%`,
                borderColor: failed ? "rgba(189,88,78,0.36)" : activeDot ? "rgba(102,143,93,0.38)" : "rgba(0,0,0,0.12)",
              }}
            >
              <span className="size-3 rounded-full" style={{ backgroundColor: failed ? "#bd584e" : activeDot ? "#668f5d" : "rgba(0,0,0,0.16)" }} />
            </span>
          );
        })}
      </div>
    </div>
  );
}

function DesktopScreen({ t }: { t: number }) {
  const report = ease(span(t, 0.76, 0.92));
  const metrics = ease(span(t, 0.56, 0.9));

  return (
    <div className="absolute inset-0 hidden items-center justify-center px-8 py-7 sm:flex">
      <div className="relative h-[390px] w-[760px]">
        <div className="relative h-full overflow-hidden rounded-[28px] border border-black/[0.08] bg-white shadow-[0_22px_70px_rgba(15,34,38,0.14)]">
          <div className="absolute inset-x-0 bottom-0 h-[45%] bg-[linear-gradient(to_bottom,rgba(255,255,255,0),rgba(224,232,211,0.82))]" />
          <div className="relative p-8">
            <div className="flex items-start justify-between gap-6">
              <div>
                <div className="text-[13px] font-medium tracking-[0.28em] text-black/32">ANTIFAILURE RUN</div>
                <h3 className="mt-5 text-[37px] font-medium leading-none tracking-[-0.055em] text-black">Deployment safety score</h3>
              </div>
              <div
                className={cn(
                  "grid h-10 place-items-center rounded-[8px] px-4 text-[14px] font-medium tracking-tight",
                  t > 0.22 ? "border border-[#668f5d]/24 bg-[#f3f8ef] text-[#3f7139]" : "bg-black text-white",
                )}
              >
                {t > 0.22 ? "Run created" : "Create run"}
              </div>
            </div>

            <Timeline t={t} />

            <div className="mt-3 grid grid-cols-[220px_1fr] items-center gap-12">
              <SafetyRing t={t} />
              <div className="space-y-5">
                <MetricRow label="Lock duration" value={report > 0.55 ? "27.4s" : "1.8s"} tone="bad" progress={lerp(0.28, 0.94, metrics)} />
                <MetricRow label="Checkout p99" value={report > 0.55 ? "6.9s" : "820ms"} tone="bad" progress={lerp(0.22, 0.82, metrics)} />
                <MetricRow label="Safe state restored" value="100%" progress={ease(span(t, 0.34, 0.5))} />
                <MetricRow label="Side effects captured" value="2/2" progress={ease(span(t, 0.56, 0.72))} />
              </div>
            </div>
          </div>

          <div
            className="absolute right-8 bottom-7 w-[315px] rounded-[12px] border border-[#bd584e]/18 bg-white/78 p-4 shadow-[0_16px_42px_rgba(15,34,38,0.12)] backdrop-blur-sm"
            style={{ opacity: report, transform: `translate3d(0, ${(1 - report) * 18}px, 0)` }}
          >
            <div className="flex items-center justify-between">
              <span className="text-[14px] font-medium tracking-tight text-[#8f3f38]">Release blocked</span>
              <Pill tone="bad">Fail</Pill>
            </div>
            <p className="mt-3 text-[13px] leading-5 tracking-tight text-black/58">Exclusive lock stalls checkout. Cleanup proof is attached.</p>
          </div>
        </div>
        <Cursor t={t} />
      </div>
    </div>
  );
}

function CompactScreen({ t }: { t: number }) {
  const report = ease(span(t, 0.76, 0.92));
  const metrics = ease(span(t, 0.56, 0.9));

  return (
    <div className="absolute inset-0 p-4 sm:hidden">
      <div className="relative h-full overflow-hidden rounded-[24px] border border-black/[0.08] bg-white shadow-[0_14px_42px_rgba(15,34,38,0.12)]">
        <div className="absolute inset-x-0 bottom-0 h-[54%] bg-[linear-gradient(to_bottom,rgba(255,255,255,0),rgba(224,232,211,0.78))]" />
        <div
          className="relative p-5 will-change-transform"
          style={{
            opacity: 1 - report * 0.78,
            transform: `translate3d(0, ${report * -10}px, 0)`,
          }}
        >
          <div className="flex items-center justify-between">
            <span className="text-[10px] font-medium tracking-[0.24em] text-black/32">ANTIFAILURE RUN</span>
            <Pill tone={report > 0.6 ? "bad" : "good"}>{report > 0.6 ? "Fail" : "Running"}</Pill>
          </div>
          <h3 className="mt-6 max-w-[230px] text-[26px] font-medium leading-none tracking-[-0.055em] text-black">Deployment safety score</h3>
          <Timeline t={t} compact />
          <div className="mt-6 grid grid-cols-[100px_1fr] items-center gap-4">
            <SafetyRing t={t} compact />
            <div className="space-y-3">
              <MetricRow compact label="Lock" value={report > 0.55 ? "27.4s" : "1.8s"} tone="bad" progress={lerp(0.28, 0.94, metrics)} />
              <MetricRow compact label="Checkout" value={report > 0.55 ? "6.9s" : "820ms"} tone="bad" progress={lerp(0.22, 0.82, metrics)} />
              <MetricRow compact label="Cleanup" value="ok" progress={ease(span(t, 0.72, 0.92))} />
            </div>
          </div>
        </div>

        <div
          className="absolute inset-x-5 bottom-16 rounded-[14px] border border-[#bd584e]/18 bg-white/90 p-5 shadow-[0_18px_48px_rgba(15,34,38,0.14)] backdrop-blur-sm will-change-transform"
          aria-hidden={report < 0.5}
          style={{ opacity: report, transform: `translate3d(0, ${(1 - report) * 18}px, 0)` }}
        >
          <div className="flex items-center justify-between">
            <span className="text-[13px] font-medium tracking-tight text-[#8f3f38]">Release blocked</span>
            <Pill tone="bad">Fail</Pill>
          </div>
          <div className="mt-5 flex items-end justify-between rounded-[8px] border border-[#bd584e]/16 bg-[#fff4f1]/55 px-3 py-3">
            <span className="text-[31px] font-medium leading-none tracking-[-0.045em] text-black">27.4s</span>
            <span className="pb-0.5 text-[12px] font-medium tracking-tight text-[#8f3f38]">lock</span>
          </div>
          <p className="mt-4 text-[12px] leading-5 text-black/58">Checkout replay stalled. Cleanup proof is attached.</p>
        </div>
        <Cursor t={t} compact />
      </div>
    </div>
  );
}

export function TwinIdeStage() {
  const ref = useRef<HTMLElement>(null);
  const { inView, reduced, story } = useInViewPlay(ref, 0.18);
  const [progress, setProgress] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [finished, setFinished] = useState(false);
  const progressRef = useRef(0);
  const playedOnceRef = useRef(false);
  const inViewRef = useRef(false);
  const frameRef = useRef<number | null>(null);
  const lastFrameRef = useRef<number | null>(null);
  const startTimerRef = useRef<number | null>(null);
  const leaveTimerRef = useRef<number | null>(null);

  const setTimeline = (nextProgress: number) => {
    const next = clamp(nextProgress);
    progressRef.current = next;
    setProgress(next);
  };

  useEffect(() => {
    inViewRef.current = inView;
  }, [inView]);

  useEffect(() => {
    if (!reduced) return;
    playedOnceRef.current = true;
    setTimeline(1);
    setPlaying(false);
    setFinished(true);
  }, [reduced]);

  useEffect(() => {
    if (reduced) return;

    if (!inView) {
      if (startTimerRef.current) window.clearTimeout(startTimerRef.current);
      startTimerRef.current = null;
      setPlaying(false);
      lastFrameRef.current = null;

      if (leaveTimerRef.current) window.clearTimeout(leaveTimerRef.current);
      leaveTimerRef.current = window.setTimeout(() => {
        if (inViewRef.current) return;
        playedOnceRef.current = false;
        setFinished(false);
        setTimeline(0);
      }, CLEAR_LEAVE_MS);
      return;
    }

    if (leaveTimerRef.current) window.clearTimeout(leaveTimerRef.current);
    leaveTimerRef.current = null;
  }, [inView, reduced]);

  useEffect(() => {
    if (reduced || !inView || !story || finished || playing || playedOnceRef.current) return;

    if (startTimerRef.current) window.clearTimeout(startTimerRef.current);
    startTimerRef.current = window.setTimeout(() => {
      if (!inViewRef.current || playedOnceRef.current) return;
      setPlaying(true);
    }, START_DELAY_MS);

    return () => {
      if (startTimerRef.current) window.clearTimeout(startTimerRef.current);
      startTimerRef.current = null;
    };
  }, [inView, story, reduced, finished, playing]);

  useEffect(() => {
    const shouldPlay = playing && inView && !reduced;

    if (!shouldPlay) {
      lastFrameRef.current = null;
      if (frameRef.current) window.cancelAnimationFrame(frameRef.current);
      frameRef.current = null;
      return;
    }

    const tick = (now: number) => {
      if (lastFrameRef.current === null) lastFrameRef.current = now;
      const delta = Math.min(32, now - lastFrameRef.current);
      lastFrameRef.current = now;

      const next = Math.min(1, progressRef.current + delta / DURATION_MS);
      setTimeline(next);

      if (next >= 1) {
        playedOnceRef.current = true;
        setFinished(true);
        setPlaying(false);
        lastFrameRef.current = null;
        frameRef.current = null;
        return;
      }

      frameRef.current = window.requestAnimationFrame(tick);
    };

    frameRef.current = window.requestAnimationFrame(tick);

    return () => {
      if (frameRef.current) window.cancelAnimationFrame(frameRef.current);
      frameRef.current = null;
    };
  }, [playing, inView, reduced]);

  useEffect(() => {
    return () => {
      if (startTimerRef.current) window.clearTimeout(startTimerRef.current);
      if (leaveTimerRef.current) window.clearTimeout(leaveTimerRef.current);
      if (frameRef.current) window.cancelAnimationFrame(frameRef.current);
    };
  }, []);

  const t = reduced ? 1 : progress;

  return (
    <figure
      ref={ref}
      className="relative overflow-hidden rounded-[10px] border border-black/[0.11] bg-white font-sans tracking-tight shadow-[0_18px_60px_rgba(0,0,0,0.05)]"
      aria-label="A short Antifailure product demo showing a risky pull request, a contained run, replayed checkout traffic, and a failed release report with cleanup proof."
    >
      <div className="relative h-[456px] overflow-hidden bg-[#f8f6ef] max-sm:h-[430px]" aria-hidden="true">
        <div className="absolute inset-0 bg-[radial-gradient(circle_at_18%_14%,rgba(102,143,93,0.12),transparent_30%),radial-gradient(circle_at_82%_76%,rgba(180,165,116,0.11),transparent_34%),linear-gradient(to_right,rgba(58,52,41,0.026)_1px,transparent_1px),linear-gradient(to_bottom,rgba(58,52,41,0.022)_1px,transparent_1px)] bg-[size:100%_100%,100%_100%,40px_40px,40px_40px]" />
        <DesktopScreen t={t} />
        <CompactScreen t={t} />
      </div>
    </figure>
  );
}
