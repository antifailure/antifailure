"use client";

import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { motion } from "motion/react";
import { EASE } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import { LogoMark } from "@/components/icons";

const u = (n: number) => `calc(${n} * var(--s))`;

const PAGE = "#FFFFFF";
const SIDE = "#FAFAFA";
const CARD = "#FFFFFF";
const INK = "#161616";
const MUTED = "#8A8A83";
const DIM = "#A3A39C";
const RULE = "rgba(16,16,16,0.10)";
const GOLD = "#D4A017";
const MENTION = "#5B5FEF";
const DEL = "#D94841";
// Dark enough on white to clear 4.5:1, which the brand green at #33bf00 does
// not: this is text, not a logo.
const OK = "#1E7A3A";

/**
 * Which of the two plans the panel is showing.
 *
 * The point of the whole visual is that a BLOCK is not a dead end: the run
 * names the failure AND the change that passes. So the plan is a control the
 * reader can flip, and flipping it moves the findings, the verdict and the
 * issue's own activity feed together. One state, read by both halves, because
 * a verdict that said READY next to an activity row that still said BLOCK
 * would be the lie this product exists to prevent.
 */
type Plan = "as-written" | "safer";

export function MigrationBento() {
  const root = useRef<HTMLDivElement>(null);
  const play = useInViewPlay(root, 0.2);
  const live = play.idle || play.reduced;
  const [t, setT] = useState(0);
  const [plan, setPlan] = useState<Plan>("as-written");

  usePausedRaf(play.idle, (_now, elapsed) => {
    setT(Math.min(10, elapsed / 1000));
  });

  const worked = play.idle ? Math.min(10, Math.floor(t)) : 10;

  return (
    <div ref={root} className="@container relative w-full min-w-0 overflow-x-hidden">
      <div
        data-ref="panel"
        className="relative isolate overflow-hidden [--s:calc(100cqw/1024)] max-md:[--s:max(0.8px,calc(100cqw/1024))]"
        style={{
          height: u(585),
          background: PAGE,
          borderRadius: u(5),
          boxShadow: `inset 0 0 0 1px ${RULE}`,
        }}
      >
        <div className="noise pointer-events-none absolute inset-0 opacity-[0.08] mix-blend-multiply" aria-hidden />

        <div className="relative z-10 flex h-full">
          <Sidebar live={live} />

          <main className="relative min-w-0 flex-1" style={{ padding: `${u(14)} ${u(28)} 0 ${u(24)}` }}>
            <IssueTop />
            <h2
              className="truncate"
              style={{
                marginTop: u(22),
                fontSize: u(26),
                fontWeight: 600,
                letterSpacing: "-0.035em",
                color: INK,
                lineHeight: 1.15,
              }}
            >
              add_billing_status
            </h2>
            <p
              style={{
                marginTop: u(10),
                maxWidth: u(520),
                fontSize: u(13.5),
                lineHeight: 1.55,
                letterSpacing: "-0.018em",
                color: "#3F3F3A",
              }}
            >
              Add a nullable column on <Code>subscriptions</Code> and measure exclusive lock, checkout p99, and pool pressure before the change ships.
            </p>

            <p
              style={{
                marginTop: u(22),
                fontSize: u(13),
                fontWeight: 600,
                color: INK,
                letterSpacing: "-0.02em",
              }}
            >
              Activity
            </p>
            <ul className="relative" style={{ marginTop: u(10) }}>
              {activityFor(plan).map((item, i) => (
                <ActivityRow key={i} item={item} index={i} play={live} />
              ))}
            </ul>

            <div
              className="pointer-events-none absolute inset-x-0 bottom-0"
              style={{
                height: u(72),
                background: `linear-gradient(180deg, rgba(255,255,255,0) 0%, ${PAGE} 100%)`,
              }}
              aria-hidden
            />
          </main>
        </div>

        <AgentWindow seconds={worked} live={live} plan={plan} onPlan={setPlan} />
      </div>
    </div>
  );
}

function Sidebar({ live }: { live: boolean }) {
  return (
    <aside
      className="flex h-full shrink-0 flex-col max-md:hidden"
      style={{
        width: u(208),
        background: SIDE,
        padding: `${u(14)} ${u(10)} ${u(16)}`,
        borderRight: `1px solid ${RULE}`,
      }}
    >
      <div className="flex items-center" style={{ gap: u(8), paddingInline: u(6) }}>
        <span className="inline-flex shrink-0 items-center justify-center" style={{ width: u(16), height: u(16) }}>
          <LogoMark className="size-full" />
        </span>
        <span style={{ fontSize: u(12.5), fontWeight: 600, color: INK, letterSpacing: "-0.02em" }}>Antifailure</span>
        <span style={{ color: DIM, width: u(10), height: u(10) }}>
          <IconChevron />
        </span>
        <span className="ml-auto flex items-center" style={{ gap: u(8), color: MUTED }}>
          <span style={{ width: u(14), height: u(14) }}>
            <IconSearch />
          </span>
          <span style={{ width: u(14), height: u(14) }}>
            <IconNew />
          </span>
        </span>
      </div>

      <ul style={{ marginTop: u(16) }}>
        {NAV.map((item, i) => (
          <NavRow key={item.label} icon={item.icon} delay={0.04 + i * 0.03} live={live}>
            {item.label}
          </NavRow>
        ))}
      </ul>

      <p className="uppercase" style={{ marginTop: u(18), paddingInline: u(8), fontSize: u(10.5), letterSpacing: "0.12em", color: DIM }}>
        Workspace
      </p>
      <ul style={{ marginTop: u(6) }}>
        {WORKSPACE.map((item) => (
          <NavRow key={item.label} icon={item.icon} live={live}>
            {item.label}
          </NavRow>
        ))}
      </ul>

      <p className="uppercase" style={{ marginTop: u(18), paddingInline: u(8), fontSize: u(10.5), letterSpacing: "0.12em", color: DIM }}>
        Favorites
      </p>
      <ul style={{ marginTop: u(6) }}>
        {FAVORITES.map((item) => (
          <li key={item.label}>
            <div
              className="flex items-center"
              style={{
                height: u(28),
                gap: u(8),
                paddingInline: u(8),
                borderRadius: u(4),
                background: item.active ? "rgba(16,16,16,0.06)" : "transparent",
                color: INK,
                fontSize: u(12.5),
                letterSpacing: "-0.018em",
              }}
            >
              <span className="shrink-0" style={{ width: u(14), height: u(14), color: item.tone }}>
                {item.icon}
              </span>
              <span className="truncate">{item.label}</span>
            </div>
          </li>
        ))}
      </ul>
    </aside>
  );
}

function NavRow({
  children,
  icon,
  live,
  delay = 0,
}: {
  children: ReactNode;
  icon: ReactNode;
  live: boolean;
  delay?: number;
}) {
  return (
    <motion.li
      initial={live ? { opacity: 0, x: -6 } : false}
      animate={{ opacity: 1, x: 0 }}
      transition={{ duration: 0.4, delay, ease: EASE }}
    >
      <div
        className="flex items-center"
        style={{
          height: u(28),
          gap: u(8),
          paddingInline: u(8),
          color: "#3D3D38",
          fontSize: u(12.5),
          letterSpacing: "-0.018em",
        }}
      >
        <span className="shrink-0" style={{ width: u(14), height: u(14), color: MUTED }}>
          {icon}
        </span>
        {children}
      </div>
    </motion.li>
  );
}

function IssueTop() {
  return (
    <div className="flex min-w-0 items-center" style={{ height: u(28), gap: u(8) }}>
      <GoldMark />
      <span className="shrink-0" style={{ fontSize: u(12.5), color: MUTED, letterSpacing: "-0.015em" }}>MIG-1204</span>
      <span className="min-w-0 truncate" style={{ fontSize: u(12.5), color: INK, fontWeight: 500, letterSpacing: "-0.02em" }}>add_billing_status</span>
      <span className="ml-auto flex shrink-0 items-center" style={{ gap: u(10), color: MUTED }}>
        <span style={{ width: u(13), height: u(13) }}>
          <IconLink />
        </span>
        <span style={{ fontSize: u(12), letterSpacing: "-0.01em" }}>1 / 84</span>
        <span className="flex" style={{ gap: u(4) }}>
          <span style={{ width: u(12), height: u(12) }}>
            <IconUp />
          </span>
          <span style={{ width: u(12), height: u(12) }}>
            <IconDown />
          </span>
        </span>
      </span>
    </div>
  );
}

function Code({ children }: { children: ReactNode }) {
  return (
    <span
      className="font-mono"
      style={{
        padding: `${u(1)} ${u(5)}`,
        borderRadius: u(4),
        background: "rgba(16,16,16,0.06)",
        fontSize: u(12),
        color: INK,
      }}
    >
      {children}
    </span>
  );
}

type Activity = {
  kind: "system" | "comment" | "status";
  who?: string;
  avatar?: string;
  time: string;
  body: ReactNode;
};

function activityFor(plan: Plan): Activity[] {
  const safer = plan === "safer";
  return [
  {
    kind: "system",
    time: "2min ago",
    body: (
      <>
        Antifailure opened the run from checkout traffic · <span style={{ color: DIM }}>2min ago</span>
      </>
    ),
  },
  safer
    ? {
        kind: "system",
        time: "just now",
        body: (
          <>
            Policy cleared <Pill tone="block">BLOCK</Pill> · labels now <Pill tone="ok">READY</Pill> and{" "}
            <Pill>Locks</Pill> · <span style={{ color: DIM }}>just now</span>
          </>
        ),
      }
    : {
    kind: "system",
    time: "2min ago",
    body: (
      <>
        Policy added the labels <Pill tone="block">BLOCK</Pill> and <Pill>Locks</Pill> ·{" "}
        <span style={{ color: DIM }}>2min ago</span>
      </>
    ),
  },
  {
    kind: "comment",
    who: "maya",
    avatar: "/home/avatar-maya.jpg",
    time: "4min ago",
    body: "ACCESS EXCLUSIVE held 27.4s — POST /v1/checkout is stalled behind the rewrite.",
  },
  {
    kind: "comment",
    who: "jordan",
    avatar: "/home/avatar-jordan.jpg",
    time: "just now",
    body: (
      <>
        <span style={{ color: MENTION }}>@antifailure</span> can you take a stab at this?
      </>
    ),
  },
  {
    kind: "system",
    time: "just now",
    body: (
      <>
        Antifailure connected · <span style={{ color: DIM }}>just now</span>
      </>
    ),
  },
  {
    kind: "system",
    time: "just now",
    body: safer ? (
      <>
        Changed 3 files · nullable add, batched backfill, NOT NULL after ·{" "}
        <span style={{ color: DIM }}>just now</span>
      </>
    ) : (
      <>
        Changed 2 files · nullable add, backfill later · <span style={{ color: DIM }}>just now</span>
      </>
    ),
  },
  {
    kind: "status",
    time: "just now",
    body: safer ? (
      <>
        Antifailure moved from Block to Ready · <span style={{ color: DIM }}>just now</span>
      </>
    ) : (
      <>
        Antifailure moved from Todo to Block · <span style={{ color: DIM }}>just now</span>
      </>
    ),
  },
  ];
}

function ActivityRow({ item, index, play }: { item: Activity; index: number; play: boolean }) {
  return (
    <motion.li
      className="flex"
      style={{ gap: u(10), paddingBottom: u(12) }}
      initial={play ? { opacity: 0, y: 8 } : false}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.42, delay: 0.12 + index * 0.055, ease: EASE }}
    >
      <span
        className="flex shrink-0 items-center justify-center"
        style={{
          // A system line is 12.5 * 1.45. A comment card is 8 of padding plus a
          // 12 * 1.4 first line, so its marker centres on 16.4 -> a 32.8 box.
          width: item.kind === "comment" ? u(22) : u(14),
          height: item.kind === "comment" ? u(32.8) : u(18.125),
        }}
      >
        {item.kind === "status" ? <GoldMark /> : item.kind === "comment" ? <Avatar src={item.avatar} name={item.who ?? ""} /> : <Dot />}
      </span>
      {item.kind === "comment" ? (
        <div
          className="min-w-0"
          style={{
            flex: "0 1 auto",
            maxWidth: u(420),
            background: CARD,
            borderRadius: u(4),
            padding: `${u(8)} ${u(10)}`,
            boxShadow: `inset 0 0 0 1px ${RULE}`,
          }}
        >
          <p style={{ fontSize: u(12), color: MUTED }}>
            <span style={{ color: INK, fontWeight: 500 }}>{item.who}</span> · {item.time}
          </p>
          <p style={{ marginTop: u(4), fontSize: u(12.5), lineHeight: 1.45, color: "#2C2C28", letterSpacing: "-0.015em" }}>
            {item.body}
          </p>
        </div>
      ) : (
        <p
          className="min-w-0"
          style={{
            fontSize: u(12.5),
            lineHeight: 1.45,
            color: "#3F3F3A",
            letterSpacing: "-0.015em",
            // Matches the marker box, so a one-line row is centred against its
            // dot and a wrapped row still starts on the first line.
            minHeight: u(18.125),
          }}
        >
          {item.body}
        </p>
      )}
    </motion.li>
  );
}

function AgentWindow({
  seconds,
  live,
  plan,
  onPlan,
}: {
  seconds: number;
  live: boolean;
  plan: Plan;
  onPlan: (plan: Plan) => void;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const [collapsed, setCollapsed] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [draft, setDraft] = useState("");
  const [skillsOpen, setSkillsOpen] = useState(false);
  const [active, setActive] = useState("verdict");
  const [notes, setNotes] = useState<string[]>([]);

  useEffect(() => {
    if (notes.length === 0) return;
    const el = scroller.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  }, [notes]);

  function jump(id: string) {
    setActive(id);
    setSkillsOpen(false);
    const node = scroller.current?.querySelector(`[data-finding="${id}"]`);
    node?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }

  function send(event?: FormEvent) {
    event?.preventDefault();
    const text = draft.trim();
    if (!text) return;
    setNotes((prev) => [...prev, text]);
    setDraft("");
  }

  const findings = FINDINGS[plan];
  const verdict = VERDICT[plan];
  const height = collapsed ? 40 : expanded ? 430 : 328;

  return (
    <motion.aside
      className="absolute z-20 flex flex-col overflow-hidden"
      style={{
        width: u(300),
        height: u(height),
        right: u(16),
        bottom: u(14),
        background: CARD,
        borderRadius: u(5),
        boxShadow: `0 12px 28px rgba(16,16,16,0.08), inset 0 0 0 1px ${RULE}`,
      }}
      initial={live ? { opacity: 0, y: 18, scale: 0.98 } : false}
      animate={{ opacity: 1, y: 0, scale: 1, height: u(height) }}
      transition={{ duration: 0.45, delay: live ? 0.28 : 0, ease: EASE }}
      onWheel={(e) => e.stopPropagation()}
    >
      <div
        className="flex shrink-0 items-center"
        style={{ height: u(40), paddingInline: u(12), gap: u(8), borderBottom: collapsed ? undefined : `1px solid ${RULE}`, cursor: collapsed ? "pointer" : undefined }}
        onClick={() => {
          if (collapsed) setCollapsed(false);
        }}
      >
        <span className="inline-flex shrink-0 items-center justify-center" style={{ width: u(14), height: u(14) }}>
          <LogoMark className="size-full" />
        </span>
        <span style={{ fontSize: u(12.5), fontWeight: 500, color: INK, letterSpacing: "-0.02em" }}>Antifailure</span>
        <span className="ml-auto flex items-center" style={{ gap: u(8), color: DIM }}>
          <button
            type="button"
            aria-label="Minimize"
            onClick={(e) => {
              e.stopPropagation();
              setCollapsed(true);
            }}
            style={{ width: u(11), height: u(11) }}
          >
            <IconMin />
          </button>
          <button
            type="button"
            aria-label={expanded ? "Restore" : "Expand"}
            onClick={(e) => {
              e.stopPropagation();
              setCollapsed(false);
              setExpanded((v) => !v);
            }}
            style={{ width: u(11), height: u(11) }}
          >
            <IconMax />
          </button>
          <button
            type="button"
            aria-label="Collapse"
            onClick={(e) => {
              e.stopPropagation();
              setCollapsed(true);
            }}
            style={{ width: u(11), height: u(11) }}
          >
            <IconX />
          </button>
        </span>
      </div>

      {!collapsed ? (
        <>
          <div
            ref={scroller}
            className="min-h-0 flex-1 overflow-y-auto overscroll-contain"
            style={{ padding: `${u(10)} ${u(12)}` }}
          >
            <div
              style={{
                padding: u(10),
                borderRadius: u(4),
                background: "#F7F7F7",
                boxShadow: `inset 0 0 0 1px ${RULE}`,
                fontSize: u(12.5),
                lineHeight: 1.45,
                color: INK,
                letterSpacing: "-0.018em",
              }}
            >
              Prove <span className="font-mono">add_billing_status</span> is safe on production-shaped Postgres. Fail closed.
            </div>

            <p className="flex items-center" style={{ marginTop: u(8), gap: u(6), fontSize: u(11.5), color: MUTED }}>
              <span className="inline-block shrink-0 rounded-full" style={{ width: u(6), height: u(6), background: GOLD }} />
              <span className="font-mono">subscriptions</span> schema in this run
            </p>
            <p className="flex items-center" style={{ marginTop: u(6), gap: u(6), fontSize: u(12), color: MUTED }}>
              <span className="flex shrink-0 items-center justify-center" style={{ width: u(10), height: u(17.4), color: DIM }}>
                <IconPlay />
              </span>
              Ran for {seconds}s · data stayed in-boundary
            </p>

            <div
              className="flex"
              role="group"
              aria-label="Which plan to show"
              style={{ marginTop: u(10), gap: u(2), padding: u(2), borderRadius: u(5), background: "rgba(16,16,16,0.05)" }}
            >
              {(["as-written", "safer"] as const).map((option) => {
                const on = plan === option;
                return (
                  <button
                    key={option}
                    type="button"
                    aria-pressed={on}
                    onClick={() => onPlan(option)}
                    className="flex-1 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-black/60"
                    style={{
                      height: u(24),
                      borderRadius: u(4),
                      fontSize: u(11.5),
                      letterSpacing: "-0.015em",
                      fontWeight: on ? 600 : 400,
                      color: on ? INK : MUTED,
                      background: on ? CARD : "transparent",
                      boxShadow: on ? `inset 0 0 0 1px ${RULE}` : undefined,
                      transition: "background 0.18s, color 0.18s",
                    }}
                  >
                    {option === "as-written" ? "As written" : "Safer path"}
                  </button>
                );
              })}
            </div>

            <ul style={{ marginTop: u(10) }}>
              {findings.map((item) => {
                const on = active === item.id;
                return (
                  <li key={item.id} data-finding={item.id} style={{ marginBottom: u(8) }}>
                    <button
                      type="button"
                      onClick={() => setActive(on ? "" : item.id)}
                      aria-expanded={on}
                      className="w-full text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-black/60"
                      style={{
                        padding: u(8),
                        borderRadius: u(4),
                        boxShadow: `inset 0 0 0 1px ${on ? "rgba(16,16,16,0.22)" : RULE}`,
                        background: on ? "#F7F7F7" : CARD,
                        transition: "background 0.18s, box-shadow 0.18s",
                      }}
                    >
                      <p className="flex items-center justify-between" style={{ fontSize: u(11), color: MUTED, letterSpacing: "0.04em" }}>
                        <span className="uppercase">{item.label}</span>
                        <span style={{ color: item.tone === "block" ? DEL : OK, fontWeight: 600 }}>{item.mark}</span>
                      </p>
                      <p style={{ marginTop: u(4), fontSize: u(12.5), lineHeight: 1.4, color: "#2C2C28", letterSpacing: "-0.018em" }}>
                        {item.detail}
                      </p>
                      {on ? (
                        <p
                          className="font-mono"
                          style={{ marginTop: u(6), fontSize: u(10.5), lineHeight: 1.45, color: MUTED }}
                        >
                          {item.evidence}
                        </p>
                      ) : null}
                    </button>
                  </li>
                );
              })}
            </ul>

            <button
              type="button"
              data-finding="verdict"
              onClick={() => setActive("verdict")}
              className="w-full text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-black/60"
              style={{
                padding: u(8),
                borderRadius: u(4),
                boxShadow: `inset 0 0 0 1px ${active === "verdict" ? "rgba(16,16,16,0.22)" : RULE}`,
                background: active === "verdict" ? "#F7F7F7" : CARD,
              }}
            >
              <p style={{ fontSize: u(11.5), color: MUTED }}>
                Verdict{" "}
                <span style={{ color: verdict.tone === "block" ? DEL : OK, fontWeight: 600 }}>{verdict.mark}</span>
              </p>
              <p style={{ marginTop: u(4), fontSize: u(12), fontWeight: 500, color: INK, letterSpacing: "-0.02em" }}>
                {verdict.title}
              </p>
              <p style={{ marginTop: u(4), fontSize: u(10.5), lineHeight: 1.45, color: DIM }}>{verdict.note}</p>
            </button>

            {notes.map((note, i) => (
              <p
                key={`${note}-${i}`}
                style={{
                  marginTop: u(8),
                  marginLeft: u(24),
                  padding: u(8),
                  borderRadius: u(4),
                  background: "#F0F0EA",
                  fontSize: u(12.5),
                  lineHeight: 1.4,
                  color: INK,
                }}
              >
                {note}
              </p>
            ))}
          </div>

          <form
            onSubmit={send}
            className="relative shrink-0"
            style={{
              margin: u(10),
              marginTop: 0,
              padding: `${u(6)} ${u(8)}`,
              borderRadius: u(4),
              boxShadow: `inset 0 0 0 1px ${RULE}`,
            }}
          >
            {skillsOpen ? (
              <div
                className="absolute left-0 right-0 overflow-hidden"
                style={{
                  bottom: "100%",
                  marginBottom: u(6),
                  background: CARD,
                  borderRadius: u(4),
                  boxShadow: `0 8px 24px rgba(16,16,16,0.1), inset 0 0 0 1px ${RULE}`,
                }}
              >
                {findings.map((item) => (
                  <button
                    key={item.id}
                    type="button"
                    onClick={() => jump(item.id)}
                    className="flex w-full items-center justify-between text-left"
                    style={{
                      height: u(28),
                      paddingInline: u(10),
                      fontSize: u(12),
                      color: INK,
                    }}
                  >
                    <span>{item.label}</span>
                    <span style={{ color: item.tone === "block" ? DEL : OK, fontSize: u(11) }}>{item.mark}</span>
                  </button>
                ))}
                <button
                  type="button"
                  onClick={() => jump("verdict")}
                  className="flex w-full items-center justify-between text-left"
                  style={{ height: u(28), paddingInline: u(10), fontSize: u(12), color: INK }}
                >
                  <span>Verdict</span>
                  <span style={{ color: verdict.tone === "block" ? DEL : OK, fontSize: u(11) }}>{verdict.mark}</span>
                </button>
              </div>
            ) : null}
            <div className="flex items-center" style={{ gap: u(8) }}>
              <input
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                placeholder="Reply…"
                className="min-w-0 flex-1 bg-transparent outline-none"
                style={{ height: u(26), fontSize: u(12), color: INK }}
              />
              <button
                type="button"
                onClick={() => setSkillsOpen((v) => !v)}
                style={{ fontSize: u(11), color: DIM, whiteSpace: "nowrap" }}
              >
                Skills
              </button>
              <button type="submit" aria-label="Send" style={{ width: u(12), height: u(12), color: DIM }}>
                <IconSend />
              </button>
            </div>
          </form>
        </>
      ) : null}
    </motion.aside>
  );
}

type Finding = {
  id: string;
  label: string;
  mark: string;
  tone: "block" | "ok";
  detail: string;
  evidence: string;
};

// The same five checks under both plans. Selecting one shows the line the run
// actually measured, because "BLOCK" on its own is an opinion and the number
// under it is the argument.
const FINDINGS: Record<Plan, Finding[]> = {
  "as-written": [
    {
      id: "locks",
      label: "Locks",
      mark: "BLOCK",
      tone: "block",
      detail: "ACCESS EXCLUSIVE on subscriptions for 27.4s. Policy is under 2s.",
      evidence: "lock_waits 41 · longest hold 27.4s · budget 2.0s",
    },
    {
      id: "plans",
      label: "Plans",
      mark: "BLOCK",
      tone: "block",
      detail: "Checkout reads fell back to Seq Scan. Planner dropped subscriptions_status_idx.",
      evidence: "checkout_by_status → Seq Scan on subscriptions · cost 84210",
    },
    {
      id: "pool",
      label: "Pool",
      mark: "BLOCK",
      tone: "block",
      detail: "20/20 connections busy, +14 waiting. Checkout p99 820ms → 6.9s.",
      evidence: "cl_waiting 14 · p99 6.9s against a 820ms baseline",
    },
    {
      id: "rollback",
      label: "Rollback",
      mark: "BLOCK",
      tone: "block",
      detail: "Old binary cannot read the new column. Rolling revert is not feasible.",
      evidence: "v412 replay → column billing_status does not exist",
    },
    {
      id: "cleanup",
      label: "Cleanup",
      mark: "OK",
      tone: "ok",
      detail: "Run torn down. Production data never left the customer boundary.",
      evidence: "clone destroyed · 0 rows crossed the boundary",
    },
  ],
  safer: [
    {
      id: "locks",
      label: "Locks",
      mark: "OK",
      tone: "ok",
      detail: "Nullable add takes no rewrite. Longest hold 0.04s.",
      evidence: "lock_waits 0 · longest hold 0.04s · budget 2.0s",
    },
    {
      id: "plans",
      label: "Plans",
      mark: "OK",
      tone: "ok",
      detail: "Checkout keeps subscriptions_status_idx. Plan is unchanged.",
      evidence: "checkout_by_status → Index Scan · cost 84 (was 84210)",
    },
    {
      id: "pool",
      label: "Pool",
      mark: "OK",
      tone: "ok",
      detail: "Backfill in 5k batches. 7/20 connections busy at peak.",
      evidence: "cl_waiting 0 · p99 840ms against a 820ms baseline",
    },
    {
      id: "rollback",
      label: "Rollback",
      mark: "OK",
      tone: "ok",
      detail: "Old binary ignores the nullable column. Revert replayed clean.",
      evidence: "v412 replay → 11s, no error",
    },
    {
      id: "cleanup",
      label: "Cleanup",
      mark: "OK",
      tone: "ok",
      detail: "Run torn down. Production data never left the customer boundary.",
      evidence: "clone destroyed · 0 rows crossed the boundary",
    },
  ],
};

const VERDICT: Record<Plan, { mark: string; tone: "block" | "ok"; title: string; note: string }> = {
  "as-written": {
    mark: "BLOCK",
    tone: "block",
    title: "Do not ship add_billing_status as written",
    note: "Safer path: nullable add, no table rewrite, backfill in small batches",
  },
  safer: {
    mark: "READY",
    tone: "ok",
    title: "Safe to ship add_billing_status in three steps",
    note: "Nullable add now · backfill 5k a batch · set NOT NULL once the backfill lands",
  },
};

function Pill({ children, tone }: { children: ReactNode; tone?: "block" | "ok" }) {
  const color = tone === "block" ? DEL : tone === "ok" ? OK : "#3D3D38";
  const background =
    tone === "block"
      ? "rgba(217,72,65,0.12)"
      : tone === "ok"
        ? "rgba(30,122,58,0.12)"
        : "rgba(16,16,16,0.06)";
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        height: u(18),
        paddingInline: u(6),
        borderRadius: u(4),
        fontSize: u(10.5),
        letterSpacing: "-0.01em",
        color,
        background,
      }}
    >
      {children}
    </span>
  );
}

function GoldMark() {
  return (
    <span
      className="inline-flex shrink-0 items-center justify-center rounded-full"
      style={{ width: u(14), height: u(14), background: GOLD, color: "#fff" }}
    >
      <svg viewBox="0 0 14 14" className="size-full" fill="none" aria-hidden>
        <path d="M6 4.6 8.4 7 6 9.4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </span>
  );
}

function Dot() {
  return (
    <span
      className="inline-block rounded-full"
      style={{ width: u(14), height: u(14), boxShadow: `inset 0 0 0 1.4px ${RULE}`, background: CARD }}
    />
  );
}

function Avatar({ src, name }: { src?: string; name: string }) {
  return (
    <span
      className="inline-flex shrink-0 overflow-hidden rounded-full"
      style={{
        width: u(22),
        height: u(22),
        background: "rgba(16,16,16,0.08)",
        boxShadow: `inset 0 0 0 1px ${RULE}`,
      }}
    >
      {src ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img src={src} alt="" className="size-full object-cover" />
      ) : (
        <span className="flex size-full items-center justify-center uppercase" style={{ fontSize: u(8), fontWeight: 600, color: INK }}>
          {name[0]}
        </span>
      )}
    </span>
  );
}

const NAV = [
  { label: "Pulse", icon: <IconPulse /> },
  { label: "Inbox", icon: <IconInbox /> },
  { label: "My issues", icon: <IconIssues /> },
  { label: "Reviews", icon: <IconReviews /> },
];

const WORKSPACE = [
  { label: "Initiatives", icon: <IconHex /> },
  { label: "Projects", icon: <IconBox /> },
  { label: "More", icon: <IconMore /> },
];

const FAVORITES = [
  { label: "add_billing_status", active: true, tone: GOLD, icon: <GoldMark /> },
  { label: "checkout replay", active: false, tone: MUTED, icon: <IconDot /> },
  { label: "pool pressure", active: false, tone: "#5B8DEF", icon: <IconBars /> },
  { label: "rollback", active: false, tone: DEL, icon: <IconX /> },
];

function strokeIcon(d: string) {
  return (
    <svg viewBox="0 0 16 16" className="size-full" fill="none" aria-hidden>
      <path d={d} stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function IconChevron() {
  return strokeIcon("M3 6l5 5 5-5");
}
function IconSearch() {
  return (
    <svg viewBox="0 0 16 16" className="size-full" fill="none" aria-hidden>
      <circle cx="7" cy="7" r="4.2" stroke="currentColor" strokeWidth="1.3" />
      <path d="M10.2 10.2 13.4 13.4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}
function IconNew() {
  return strokeIcon("M3.5 3.5h9v9h-9zM8 6v4M6 8h4");
}
function IconPulse() {
  return strokeIcon("M2 8h3l1.5-3 3 6L11 8h3");
}
function IconInbox() {
  return strokeIcon("M2.5 4.5h11v8h-11zM2.5 9.5h3.2c.4 1.2 1.3 1.8 2.3 1.8s1.9-.6 2.3-1.8h3.2");
}
function IconIssues() {
  return strokeIcon("M3 4.5h10M3 8h10M3 11.5h6");
}
function IconReviews() {
  return strokeIcon("M4 3.5h8v9H4zM6.5 7.2l1.3 1.3 2.2-2.5");
}
function IconHex() {
  return strokeIcon("M8 2.2 13 5.2v5.6L8 13.8 3 10.8V5.2L8 2.2Z");
}
function IconBox() {
  return strokeIcon("M3.5 5.5 8 3.2l4.5 2.3v6.3L8 14 3.5 11.8zM3.5 5.5 8 7.8l4.5-2.3M8 7.8V14");
}
function IconMore() {
  return (
    <svg viewBox="0 0 16 16" className="size-full" fill="currentColor" aria-hidden>
      <circle cx="4" cy="8" r="1.1" />
      <circle cx="8" cy="8" r="1.1" />
      <circle cx="12" cy="8" r="1.1" />
    </svg>
  );
}
function IconDot() {
  return (
    <svg viewBox="0 0 16 16" className="size-full" fill="currentColor" aria-hidden>
      <circle cx="8" cy="8" r="3" />
    </svg>
  );
}
function IconBars() {
  return strokeIcon("M4 11.5V7M8 11.5V4.5M12 11.5V8.5");
}
function IconLink() {
  return strokeIcon("M6.5 9.5 9.5 6.5M7 6.2l.8-1.2a2.2 2.2 0 1 1 3.2 3.2L10 9.2M9 9.8l-.8 1.2a2.2 2.2 0 1 1-3.2-3.2L6 6.8");
}
function IconUp() {
  return strokeIcon("M4 9.5 8 5.5 12 9.5");
}
function IconDown() {
  return strokeIcon("M4 6.5 8 10.5 12 6.5");
}
function IconMin() {
  return strokeIcon("M3 8h10");
}
function IconMax() {
  return strokeIcon("M4 4h8v8H4z");
}
function IconX() {
  return strokeIcon("M4.5 4.5 11.5 11.5M11.5 4.5 4.5 11.5");
}
function IconPlay() {
  return (
    <svg viewBox="0 0 16 16" className="size-full" fill="currentColor" aria-hidden>
      <path d="M6 4.5v7l6-3.5z" />
    </svg>
  );
}
function IconSend() {
  return strokeIcon("M3 8h10M9.5 4.5 13 8l-3.5 3.5");
}
