"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { motion } from "motion/react";
import { EASE, EASE_OUT_CUBIC } from "@/lib/easing";
import { useDelayedFlag, useInViewPlay } from "@/lib/useInViewPlay";
import { Caret } from "./motion/Caret";

type Play = { story: boolean; idle: boolean; reduced: boolean; delay: number };

function CardFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className="relative mt-4 overflow-visible rounded-sm border border-black/12 bg-white">
      <div className="scanlines pointer-events-none absolute inset-0 z-[2] mix-blend-overlay" aria-hidden />
      <div className="relative z-[1]">{children}</div>
    </div>
  );
}

const STACK_LAYERS = ["app", "postgres", "workers"] as const;

function StackPane({
  label,
  show,
  layers,
  accent,
}: {
  label: string;
  show: boolean;
  layers: number;
  accent?: boolean;
}) {
  return (
    <motion.div
      className="min-w-0 flex-1 rounded-sm border px-2 py-2"
      style={{ borderColor: accent ? "rgba(51,191,0,0.5)" : "rgba(0,0,0,0.14)" }}
      initial={{ opacity: 0, y: 8 }}
      animate={show ? { opacity: 1, y: 0 } : { opacity: 0, y: 8 }}
      transition={{ duration: 0.4, ease: EASE }}
    >
      <div className={`text-[10px] font-medium ${accent ? "text-[#33bf00]" : "text-black/70"}`}>{label}</div>
      {STACK_LAYERS.map((name, i) => (
        <div key={name} className="mt-2">
          <motion.div
            className="h-[7px] origin-left rounded-[1px]"
            style={{ background: accent ? "rgba(51,191,0,0.4)" : "rgba(0,0,0,0.18)" }}
            initial={{ scaleX: 0 }}
            animate={{ scaleX: layers > i ? 1 : 0 }}
            transition={{ duration: 0.35, delay: show ? i * 0.08 : 0, ease: EASE }}
          />
          <div className="mt-0.5 font-mono text-[8px] tracking-wide text-black/40">{name}</div>
        </div>
      ))}
    </motion.div>
  );
}

function TwinClone({ story, reduced, delay }: Play) {
  const ready = useDelayedFlag(story, delay);
  const [n, setN] = useState(reduced ? 3 : 0);

  useEffect(() => {
    if (!ready) {
      setN(0);
      return;
    }
    if (reduced) {
      setN(3);
      return;
    }
    setN(0);
    const timers = [120, 620, 1100].map((ms, i) => window.setTimeout(() => setN(i + 1), ms));
    return () => timers.forEach(clearTimeout);
  }, [ready, reduced]);

  return (
    <CardFrame>
      <div className="flex items-stretch gap-1 px-2.5 py-3">
        <StackPane label="production" show={n >= 1} layers={n >= 1 ? 3 : 0} />
        <div className="flex w-5 shrink-0 flex-col items-center justify-center">
          <motion.div
            className="h-px w-full origin-left border-t border-dashed border-black/40"
            initial={{ scaleX: 0 }}
            animate={{ scaleX: n >= 2 ? 1 : 0 }}
            transition={{ duration: 0.4, ease: EASE }}
          />
        </div>
        <StackPane label="twin" show={n >= 3} layers={n >= 3 ? 3 : 0} accent />
      </div>
    </CardFrame>
  );
}

const EMAIL_FULL = "user_00418@mask.local";
const TOKEN_LIVE = "sk_live_8f3a9c2e";
const TOKEN_GONE = "deleted";
const SCRAMBLE = "█▓▒░#*+x";

function StateForm({ story, reduced, delay }: Play) {
  const ready = useDelayedFlag(story, delay);
  const [email, setEmail] = useState(reduced ? EMAIL_FULL : "");
  const [token, setToken] = useState(reduced ? TOKEN_GONE : "");
  const [chrome, setChrome] = useState(reduced ? 1 : 0);
  const [phase, setPhase] = useState<"idle" | "type" | "redact" | "done">(reduced ? "done" : "idle");

  useEffect(() => {
    if (!ready) {
      setChrome(0);
      setEmail("");
      setToken("");
      setPhase("idle");
      return;
    }
    if (reduced) {
      setChrome(1);
      setEmail(EMAIL_FULL);
      setToken(TOKEN_GONE);
      setPhase("done");
      return;
    }
    setChrome(0);
    setEmail("");
    setToken(TOKEN_LIVE);
    setPhase("idle");
    const t0 = window.setTimeout(() => setChrome(1), 50);
    const t1 = window.setTimeout(() => setPhase("type"), 280);
    return () => {
      window.clearTimeout(t0);
      window.clearTimeout(t1);
    };
  }, [ready, reduced]);

  useEffect(() => {
    if (phase !== "type") return;
    let i = 0;
    const type = window.setInterval(() => {
      i += 1;
      setEmail(EMAIL_FULL.slice(0, i));
      if (i >= EMAIL_FULL.length) {
        window.clearInterval(type);
        window.setTimeout(() => setPhase("redact"), 280);
      }
    }, 38);
    return () => window.clearInterval(type);
  }, [phase]);

  useEffect(() => {
    if (phase !== "redact") return;
    let n = 0;
    const id = window.setInterval(() => {
      n += 1;
      if (n < 10) {
        const mix = Array.from({ length: TOKEN_LIVE.length }, (_, i) =>
          n > i ? TOKEN_GONE[Math.min(i, TOKEN_GONE.length - 1)] ?? SCRAMBLE[i % SCRAMBLE.length] : SCRAMBLE[(n + i) % SCRAMBLE.length],
        ).join("");
        setToken(mix.slice(0, TOKEN_LIVE.length));
      } else {
        setToken(TOKEN_GONE);
        setPhase("done");
        window.clearInterval(id);
      }
    }, 52);
    return () => window.clearInterval(id);
  }, [phase]);

  return (
    <CardFrame>
      <div className="flex flex-col px-4 py-3 pb-4">
        <div className="text-[9px] tracking-[0.18em] text-black/40">SAFE-STATE.</div>
        <div className="mt-2 text-[13px] font-medium">Sanitized snapshot</div>
        <label className="mt-3 text-[10px] text-black/45">Email</label>
        <div
          className="relative mt-1 overflow-hidden rounded bg-[#f0f0ee] px-2 py-1.5 text-[11px] text-black/80"
          style={{
            boxShadow: chrome ? "inset 0 0 0 1px rgba(0,0,0,0.12)" : "inset 0 0 0 1px rgba(0,0,0,0.04)",
            transition: "box-shadow 0.55s cubic-bezier(0.16,1,0.3,1)",
          }}
        >
          <span
            className="pointer-events-none absolute inset-y-0 left-0 bg-white/10"
            style={{
              width: chrome ? "100%" : "0%",
              opacity: chrome ? 0 : 0.35,
              transition: "width 0.6s cubic-bezier(0.16,1,0.3,1), opacity 0.4s 0.45s",
            }}
          />
          {email}
          {phase === "type" && email.length < EMAIL_FULL.length ? <Caret /> : null}
        </div>
        <label className="mt-2 text-[10px] text-black/45">Token</label>
        <div
          className="relative mt-1 overflow-hidden rounded bg-[#f0f0ee] px-2 py-1.5 font-mono text-[11px]"
          style={{
            boxShadow: chrome ? "inset 0 0 0 1px rgba(0,0,0,0.12)" : "inset 0 0 0 1px rgba(0,0,0,0.04)",
            color: phase === "done" ? "rgba(10,10,10,0.38)" : "rgba(10,10,10,0.7)",
            letterSpacing: phase === "redact" ? "0.04em" : "0",
            transition: "box-shadow 0.55s 0.08s cubic-bezier(0.16,1,0.3,1), color 0.35s",
          }}
        >
          <span
            className="pointer-events-none absolute inset-0 origin-left bg-[#33bf00]/15"
            style={{
              transform: phase === "redact" || phase === "done" ? "scaleX(1)" : "scaleX(0)",
              transition: "transform 0.55s cubic-bezier(0.16,1,0.3,1)",
            }}
          />
          <span className="relative">{token || "\u00a0"}</span>
        </div>
      </div>
    </CardFrame>
  );
}

function FirewallTerminal({ story, reduced, delay }: Play) {
  const ready = useDelayedFlag(story, delay);
  const [typed, setTyped] = useState(["", ""] as [string, string]);
  const [chips, setChips] = useState(0);
  const [count, setCount] = useState(reduced ? 48 : 0);
  const [sheen, setSheen] = useState(false);
  const lines = useMemo(() => ["12:34:01 stripe.PaymentIntent", "12:34:04 sendgrid.MailSend"] as const, []);

  useEffect(() => {
    if (!ready) {
      setTyped(["", ""]);
      setChips(0);
      setCount(0);
      setSheen(false);
      return;
    }
    if (reduced) {
      setTyped([lines[0], lines[1]]);
      setChips(2);
      setCount(48);
      return;
    }
    setTyped(["", ""]);
    setChips(0);
    setCount(0);
    setSheen(false);
    let cancelled = false;
    const timers: number[] = [];
    let i = 0;
    const type1 = window.setInterval(() => {
      if (cancelled) return;
      i += 1;
      setTyped([lines[0].slice(0, i), ""]);
      if (i >= lines[0].length) {
        window.clearInterval(type1);
        setChips(1);
        let j = 0;
        const type2 = window.setInterval(() => {
          if (cancelled) return;
          j += 1;
          setTyped([lines[0], lines[1].slice(0, j)]);
          if (j >= lines[1].length) {
            window.clearInterval(type2);
            setChips(2);
            setSheen(true);
            const t0 = performance.now();
            const tick = (now: number) => {
              if (cancelled) return;
              const u = EASE_OUT_CUBIC(Math.min(1, (now - t0) / 1050));
              setCount(Math.round(48 * u));
              if (u < 1) timers.push(requestAnimationFrame(tick));
            };
            timers.push(requestAnimationFrame(tick));
          }
        }, 24);
        timers.push(type2);
      }
    }, 24);
    timers.push(type1);
    return () => {
      cancelled = true;
      timers.forEach((id) => {
        window.clearInterval(id);
        window.clearTimeout(id);
        cancelAnimationFrame(id);
      });
    };
  }, [ready, reduced, lines]);

  return (
    <CardFrame>
      <div className="flex flex-col font-mono text-[10.5px]">
        <div className="flex items-center gap-1.5 px-3 pt-2.5 text-[10px] text-black/50">
          <span className="h-1.5 w-1.5 rounded-full bg-[#33bf00]" />
          Running
        </div>
        <div className="mt-2 space-y-1 px-3 text-black/55">
          <div className="flex items-center justify-between gap-2">
            <span>{typed[0] || "\u00a0"}</span>
            <span
              className="rounded px-1.5 py-[1px] text-[9px] text-black"
              style={{
                background: chips >= 1 ? "#33bf00" : "transparent",
                color: chips >= 1 ? "#000" : "transparent",
                transform: chips >= 1 ? "scaleX(1)" : "scaleX(0.4)",
                transformOrigin: "right center",
                transition: "transform 0.35s cubic-bezier(0.16,1,0.3,1), background 0.25s, color 0.25s",
              }}
            >
              simulate
            </span>
          </div>
          <div className="flex items-center justify-between gap-2">
            <span>{typed[1] || "\u00a0"}</span>
            <span
              className="rounded px-1.5 py-[1px] text-[9px]"
              style={{
                background: chips >= 2 ? "#33bf00" : "transparent",
                color: chips >= 2 ? "#000" : "transparent",
                transform: chips >= 2 ? "scaleX(1)" : "scaleX(0.4)",
                transformOrigin: "right center",
                transition: "transform 0.35s cubic-bezier(0.16,1,0.3,1), background 0.25s, color 0.25s",
              }}
            >
              capture
            </span>
          </div>
        </div>
        <div className="relative mt-3 overflow-hidden bg-[#33bf00] px-3 py-1.5 text-black">
          {sheen ? (
            <span
              className="wt-sheen pointer-events-none absolute inset-y-0 w-16 bg-white/35"
              style={{ animation: "wt-sheen 0.85s cubic-bezier(0.16,1,0.3,1) 1" }}
            />
          ) : null}
          <span className="relative">12:34:09 simulated {count} / 48</span>
        </div>
      </div>
    </CardFrame>
  );
}

function WorkloadTree({ story, reduced, delay }: Play) {
  const ready = useDelayedFlag(story, delay);
  const [n, setN] = useState(reduced ? 3 : 0);

  useEffect(() => {
    if (!ready) {
      setN(0);
      return;
    }
    if (reduced) {
      setN(3);
      return;
    }
    setN(0);
    const timers = [140, 600, 1250].map((ms, i) => window.setTimeout(() => setN(i + 1), ms));
    return () => timers.forEach(clearTimeout);
  }, [ready, reduced]);

  return (
    <CardFrame>
      <div className="flex flex-col items-start px-5 py-4 pb-5">
        <motion.div
          className="rounded-full bg-white px-2.5 py-0.5 text-[11px] font-medium text-black"
          initial={{ scale: 0.7, opacity: 0 }}
          animate={n >= 1 ? { scale: 1, opacity: 1 } : { scale: 0.7, opacity: 0 }}
          transition={{ type: "spring", stiffness: 420, damping: 22 }}
        >
          main
        </motion.div>
        <svg width="2" height="20" className="ml-4 overflow-visible">
          <motion.line
            x1="1"
            x2="1"
            y1="0"
            y2="20"
            stroke="rgba(0,0,0,0.35)"
            strokeWidth="1.4"
            initial={{ pathLength: 0 }}
            animate={{ pathLength: n >= 2 ? 1 : 0 }}
            transition={{ duration: 0.45, ease: EASE }}
          />
        </svg>
        <motion.div
          className="ml-2 flex items-center gap-2 text-[11px] text-black/80"
          initial={{ scale: 0.85, opacity: 0 }}
          animate={n >= 2 ? { scale: 1, opacity: 1 } : { scale: 0.85, opacity: 0 }}
          transition={{ type: "spring", stiffness: 380, damping: 24 }}
        >
          <span className="h-3.5 w-3.5 rounded-sm border border-black/30" />
          /checkout
        </motion.div>
        <svg width="2" height="16" className="ml-4 overflow-visible">
          <motion.line
            x1="1"
            x2="1"
            y1="0"
            y2="16"
            stroke="rgba(0,0,0,0.35)"
            strokeWidth="1.4"
            initial={{ pathLength: 0 }}
            animate={{ pathLength: n >= 3 ? 1 : 0 }}
            transition={{ duration: 0.45, ease: EASE }}
          />
        </svg>
        <motion.div
          className="ml-2 flex items-center gap-2 text-[11px] text-black/80"
          initial={{ scale: 0.85, opacity: 0 }}
          animate={n >= 3 ? { scale: 1, opacity: 1 } : { scale: 0.85, opacity: 0 }}
          transition={{ type: "spring", stiffness: 380, damping: 24 }}
        >
          <span className="h-3.5 w-3.5 rounded-sm border border-black/30" />
          /upgrade
        </motion.div>
      </div>
    </CardFrame>
  );
}

function OracleChip({ story, reduced, delay }: Play) {
  const ready = useDelayedFlag(story, delay);
  const [phase, setPhase] = useState<"off" | "scan" | "block">(reduced ? "block" : "off");

  useEffect(() => {
    if (!ready) {
      setPhase("off");
      return;
    }
    if (reduced) {
      setPhase("block");
      return;
    }
    setPhase("off");
    const t1 = window.setTimeout(() => setPhase("scan"), 180);
    const t2 = window.setTimeout(() => setPhase("block"), 1600);
    return () => {
      window.clearTimeout(t1);
      window.clearTimeout(t2);
    };
  }, [ready, reduced]);

  return (
    <CardFrame>
      <div className="flex items-center justify-center px-3 py-10">
        <div className="relative">
          {phase === "block" && !reduced ? (
            <span
              className="wt-ring pointer-events-none absolute inset-[-10px] rounded-xl border border-[#33bf00]/70"
              style={{ animation: "wt-ring 0.7s cubic-bezier(0.16,1,0.3,1) 1" }}
            />
          ) : null}
          <div
            className="relative overflow-hidden rounded-lg border bg-[#f0f0ee] px-3 py-2 font-mono text-[11px]"
            style={{
              borderColor: phase === "block" ? "rgba(51,191,0,0.45)" : "rgba(0,0,0,0.12)",
              boxShadow: phase === "block" ? "0 0 0 1px rgba(51,191,0,0.15)" : "none",
              transition: "border-color 0.3s, box-shadow 0.3s",
            }}
          >
            {phase === "scan" ? (
              <span
                className="wt-scan pointer-events-none absolute inset-0"
                style={{
                  background:
                    "linear-gradient(90deg, transparent 0%, rgba(51,191,0,0.22) 45%, transparent 70%)",
                  backgroundSize: "60% 100%",
                  animation: "wt-scan 1.15s linear infinite",
                }}
              />
            ) : null}
            <span className="relative flex items-center gap-2">
              <span
                className="h-1.5 w-1.5 rounded-full"
                style={{ background: phase === "block" ? "#f87171" : "#33bf00" }}
              />
              {phase === "off" ? "\u00a0" : phase === "scan" ? "analyzing" : "BLOCK / schema.lock"}
            </span>
          </div>
        </div>
      </div>
    </CardFrame>
  );
}

const cards: {
  title: string;
  body: string;
  Visual: (p: Play) => React.ReactNode;
}[] = [
  {
    title: "Isolated Twin.",
    body: "Temporary copy of the relevant application stack.",
    Visual: TwinClone,
  },
  {
    title: "Safe State.",
    body: "Referentially consistent, production-shaped, and sanitized.",
    Visual: StateForm,
  },
  {
    title: "Side-Effect Firewall.",
    body: "Simulate Stripe and email. Never charge or notify a real user.",
    Visual: FirewallTerminal,
  },
  {
    title: "Workload Studio.",
    body: "Observed patterns, deterministic journeys, and exploratory users.",
    Visual: WorkloadTree,
  },
  {
    title: "Safety Oracle.",
    body: "Equivalent workloads on baseline and candidate, then pass, warning, or block.",
    Visual: OracleChip,
  },
];

const CARD_START_MS = [0, 750, 1500, 2250, 3000];

export function FeatureCards() {
  const ref = useRef<HTMLElement>(null);
  const { story, idle, reduced } = useInViewPlay(ref, 0.18);

  return (
    <section id="cards" ref={ref} className="bg-[#f7f7f5] px-6 pb-24 pt-4 lg:px-10">
      <div className="grid grid-cols-1 items-start gap-6 sm:grid-cols-2 lg:grid-cols-5 lg:gap-6">
        {cards.map((card, i) => (
          <div key={card.title}>
            <p className="text-[15px] leading-snug">
              <span className="font-semibold text-black">{card.title} </span>
              <span className="text-black/45">{card.body}</span>
            </p>
            <card.Visual story={story} idle={idle} reduced={reduced} delay={CARD_START_MS[i] ?? 0} />
          </div>
        ))}
      </div>
    </section>
  );
}
