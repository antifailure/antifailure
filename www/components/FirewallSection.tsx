"use client";

import { useEffect, useRef, useState } from "react";
import { motion } from "motion/react";
import { EASE } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";

const RING_DOTS: [number, number, number][] = [
  [38, 24, 0.25],
  [36.124, 31, 0.43],
  [31, 36.124, 0.61],
  [24, 38, 0.79],
  [17, 36.124, 0.25],
  [11.876, 31, 0.43],
  [10, 24, 0.61],
  [11.876, 17, 0.79],
  [17, 11.876, 0.25],
  [24, 10, 0.43],
  [31, 11.876, 0.61],
  [36.124, 17, 0.79],
];

function RingIcon({ play }: { play: boolean }) {
  return (
    <svg viewBox="0 0 48 48" className="mx-auto mb-5 h-8 w-8">
      {RING_DOTS.map(([cx, cy, o], i) => (
        <motion.circle
          key={i}
          cx={cx}
          cy={cy}
          r="1.7"
          fill="#0a0a0a"
          initial={{ opacity: 0 }}
          animate={play ? { opacity: o } : { opacity: 0 }}
          transition={{ duration: 0.35, delay: i * 0.04, ease: EASE }}
        />
      ))}
    </svg>
  );
}

const BASE = [
  ["1", "api.stripe.com", "PaymentIntent.create", "simulate", "clone-local"],
  ["2", "api.sendgrid.com", "mail.send", "capture", "never-deliver"],
  ["3", "hooks.slack.com", "webhook.post", "store", "preview"],
  ["4", "10.0.12.8:443", "tcp.connect", "deny", "unknown"],
  ["5", "s3.amazonaws.com", "PutObject", "clone-bucket", "write-local"],
];

export function FirewallSection() {
  const headRef = useRef<HTMLDivElement>(null);
  const ref = useRef<HTMLDivElement>(null);
  const { story: headStory, reduced: headReduced } = useInViewPlay(headRef, 0.3);
  const { story, reduced } = useInViewPlay(ref, 0.2);
  const [rows, setRows] = useState(reduced ? 5 : 0);
  const [cards, setCards] = useState(reduced);
  const [pulse, setPulse] = useState(false);
  const [intent, setIntent] = useState(reduced);

  useEffect(() => {
    if (!story) {
      if (reduced) return;
      setRows(0);
      setCards(false);
      setPulse(false);
      setIntent(false);
      return;
    }
    if (reduced) {
      setRows(5);
      setCards(true);
      return;
    }
    setRows(0);
    setCards(false);
    const timers = [160, 520, 900, 1280, 1660].map((ms, i) =>
      window.setTimeout(() => setRows(i + 1), ms),
    );
    const c = window.setTimeout(() => setCards(true), 2100);
    return () => {
      timers.forEach(clearTimeout);
      window.clearTimeout(c);
    };
  }, [story, reduced]);

  return (
    <section id="firewall" className="relative overflow-hidden bg-[#f7f7f5] pb-40 pt-12">
      <div ref={headRef} className="px-8 lg:px-16 lg:pl-[260px]">
        <div className="min-w-0 text-center">
          <RingIcon play={headStory || headReduced} />
          <h2 className="mx-auto max-w-[920px] text-[36px] font-semibold leading-[1.2] tracking-[-0.03em] md:text-[44px]">
            <span className="text-black">Side-Effect Firewall included. </span>
            <span className="text-black/45">
              Keep cloned applications from charging cards, emailing users, or touching production.
            </span>
          </h2>
        </div>
      </div>

      <div ref={ref} className="relative mx-auto mt-16 min-h-0 max-w-[1100px] px-8 lg:min-h-[520px]">
        <div className="overflow-hidden rounded-md border border-black/10">
          <table className="w-full text-left text-[12px] text-black/70">
            <thead className="bg-black/5 text-black/50">
              <tr>
                {["id", "destination", "operation", "decision", "ledger"].map((h) => (
                  <th key={h} className="px-4 py-2 font-normal">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {BASE.slice(0, rows).map((row, i) => {
                const deny = row[3] === "deny";
                return (
                  <tr
                    key={row[0]}
                    className="border-t border-black/6"
                    style={{
                      boxShadow:
                        deny && pulse
                          ? "inset 3px 0 0 #f87171, inset 0 0 24px rgba(248,113,113,0.12)"
                          : deny
                            ? "inset 3px 0 0 #f87171"
                            : i === 0
                              ? "inset 3px 0 0 #33bf00"
                              : "inset 3px 0 0 rgba(0,0,0,0.16)",
                      transition: "box-shadow 0.4s",
                    }}
                  >
                    {row.map((cell) => (
                      <td
                        key={cell}
                        className={`px-4 py-2.5 ${cell === "deny" || cell === "block" ? "text-red-400" : ""}`}
                      >
                        {cell}
                      </td>
                    ))}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        <div className="mt-6 flex flex-col gap-4 lg:mt-0">
          <motion.div
            className="rounded-none border border-black bg-white p-4 text-black shadow-xl lg:pointer-events-none lg:absolute lg:left-[8%] lg:top-[58%] lg:w-[260px]"
            initial={{ rotate: -8, y: 28, opacity: 0 }}
            animate={
              cards
                ? { rotate: 0, y: 0, opacity: 1 }
                : { rotate: -8, y: 28, opacity: 0 }
            }
            transition={{ type: "spring", stiffness: 260, damping: 22, delay: cards ? 0.18 : 0 }}
          >
            <span className="absolute -top-3 left-4 bg-[#3b82f6] px-1.5 py-0.5 text-[10px] text-white">
              email
            </span>
            <div className="text-[15px] font-medium">Email sink</div>
            <p className="mt-1 text-[12px] text-black/55">Render and capture. Never deliver.</p>
            <div className="mt-3 h-8 rounded border border-black/15 px-2 text-[12px] leading-8">
              billing@mask.local
            </div>
          </motion.div>

          <motion.div
            className="relative rounded-none border border-black bg-white p-5 text-black shadow-2xl lg:absolute lg:left-1/2 lg:top-[18%] lg:w-[320px] lg:-translate-x-1/2"
            initial={{ y: 36, scale: 0.94, opacity: 0 }}
            animate={cards ? { y: 0, scale: 1, opacity: 1 } : { y: 36, scale: 0.94, opacity: 0 }}
            transition={{ type: "spring", stiffness: 280, damping: 24 }}
          >
            <span className="absolute -top-3 left-4 bg-[#3b82f6] px-1.5 py-0.5 text-[10px] text-white">
              containment
            </span>
            <div className="text-[18px] font-medium">Stripe simulator</div>
            <p className="mt-1 text-[12px] text-black/55">Store the intent in the clone-local ledger.</p>
            <label className="mt-4 block text-[11px] text-black/45">Payment</label>
            <div className="mt-1 h-9 rounded border border-black/15 px-2 text-[13px] leading-9">
              pi_clone_184 · $49.00
            </div>
            <label className="mt-3 block text-[11px] text-black/45">Decision</label>
            <div className="mt-1 h-9 rounded border border-black/15 px-2 text-[13px] leading-9">
              simulate · no charge
            </div>
            <button
              type="button"
              className="mt-4 h-10 w-full bg-black text-[13px] text-white disabled:opacity-80"
              onClick={() => setIntent(true)}
              disabled={intent}
            >
              {intent ? "Simulated · no charge" : "Create an intent"}
            </button>
            {intent ? (
              <div className="mt-2 text-[11px] text-black/50">Ledger: pi_clone_184 stored locally</div>
            ) : null}
          </motion.div>

          <motion.div
            className="relative rounded-none border border-black bg-white p-4 text-black shadow-xl lg:pointer-events-none lg:absolute lg:right-[6%] lg:top-[62%] lg:w-[280px]"
            initial={{ scale: 1.08, opacity: 0 }}
            animate={
              cards
                ? { scale: 1, opacity: 1, boxShadow: pulse ? "0 0 0 2px #f87171" : "0 25px 50px rgba(0,0,0,0.25)" }
                : { scale: 1.08, opacity: 0 }
            }
            transition={{ type: "spring", stiffness: 420, damping: 18, delay: cards ? 0.28 : 0 }}
          >
            <span className="absolute -top-3 left-4 bg-[#3b82f6] px-1.5 py-0.5 text-[10px] text-white">
              fail-closed
            </span>
            <div className="text-[15px] font-medium">Unknown destinations</div>
            <div className="mt-3 space-y-2 text-[12px]">
              <div className="flex justify-between">
                <span>10.0.12.8:443</span>
                <span className="text-red-600">deny</span>
              </div>
              <div className="flex justify-between">
                <span>prod-api.internal</span>
                <span className="text-red-600">block</span>
              </div>
            </div>
          </motion.div>
        </div>
      </div>
    </section>
  );
}
