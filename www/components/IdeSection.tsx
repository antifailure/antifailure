"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { motion } from "motion/react";
import { EASE } from "@/lib/easing";
import { CopyCli } from "./Pills";
import { Caret } from "./motion/Caret";
import { useInViewPlay } from "@/lib/useInViewPlay";

type Token = { t: string; cls: string };
type BottomTab = "problems" | "output" | "debug" | "terminal" | "ports";
type TreeItem = {
  id: string;
  name: string;
  indent: number;
  folder?: boolean;
  parent: string | null;
  path: string;
};

const TREE: TreeItem[] = [
  { id: "root", name: "CHECKOUT-APP", indent: 0, folder: true, parent: null, path: "CHECKOUT-APP" },
  { id: "src", name: "src", indent: 1, folder: true, parent: "root", path: "src" },
  { id: "lib", name: "lib", indent: 2, folder: true, parent: "src", path: "src/lib" },
  { id: "routes", name: "routes", indent: 2, folder: true, parent: "src", path: "src/routes" },
  { id: "api", name: "api", indent: 3, folder: true, parent: "routes", path: "src/routes/api" },
  { id: "index.tsx", name: "index.tsx", indent: 4, parent: "api", path: "src/routes/api/index.tsx" },
  { id: "app.tsx", name: "app.tsx", indent: 2, parent: "src", path: "src/app.tsx" },
  { id: "wind-tunnel.yml", name: "wind-tunnel.yml", indent: 1, parent: "root", path: "wind-tunnel.yml" },
  { id: "seed.sql", name: "seed.sql", indent: 1, parent: "root", path: "seed.sql" },
  { id: "package.json", name: "package.json", indent: 1, parent: "root", path: "package.json" },
  { id: "README.md", name: "README.md", indent: 1, parent: "root", path: "README.md" },
];

const KW = "text-[#569cd6]";
const FN = "text-[#dcdcaa]";
const VAR = "text-[#9cdcfe]";
const STR = "text-[#ce9178]";
const CM = "text-[#6a9955]";
const DIM = "text-white/50";
const TEAL = "text-[#4ec9b0]";

const YML: Token[] = [
  { t: "# Generated — do not hand-author\n", cls: CM },
  { t: "repository", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "checkout-app\n", cls: STR },
  { t: "cloud", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "customer-hosted\n", cls: STR },
  { t: "source", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "postgres\n", cls: STR },
  { t: "contain", cls: VAR },
  { t: ":\n  - ", cls: DIM },
  { t: "stripe\n", cls: STR },
  { t: "  - ", cls: DIM },
  { t: "email\n", cls: STR },
  { t: "compare", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "baseline_vs_candidate\n", cls: STR },
  { t: "on_pr", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "create_wind_tunnel", cls: STR },
];

const FILE_TOKENS: Record<string, Token[]> = {
  "wind-tunnel.yml": YML,
  "index.tsx": [
    { t: "import", cls: KW },
    { t: " { ", cls: VAR },
    { t: "getSession", cls: TEAL },
    { t: ", ", cls: VAR },
    { t: "upgrade", cls: TEAL },
    { t: " } ", cls: VAR },
    { t: "from", cls: KW },
    { t: " ", cls: VAR },
    { t: '"@/lib/billing"', cls: STR },
    { t: "\n\n", cls: VAR },
    { t: "export default async function", cls: KW },
    { t: " handler", cls: FN },
    { t: "(req, res) {\n  ", cls: VAR },
    { t: "const", cls: KW },
    { t: " { ", cls: VAR },
    { t: "session", cls: TEAL },
    { t: " } = ", cls: VAR },
    { t: "await", cls: KW },
    { t: " getSession", cls: FN },
    { t: "(req)\n\n  ", cls: VAR },
    { t: "res", cls: VAR },
    { t: ".status", cls: FN },
    { t: "(", cls: VAR },
    { t: "200", cls: "text-[#b5cea8]" },
    { t: ").", cls: VAR },
    { t: "json", cls: FN },
    { t: "({ user: session })\n}", cls: VAR },
  ],
  "app.tsx": [
    { t: "export default function", cls: KW },
    { t: " App", cls: FN },
    { t: "({ children }: { children: React.ReactNode }) {\n  ", cls: VAR },
    { t: "return", cls: KW },
    { t: " <main className=", cls: VAR },
    { t: '"checkout"', cls: STR },
    { t: ">{children}</main>\n}", cls: VAR },
  ],
  "seed.sql": [
    { t: "-- Sanitized snapshot. Tokens deleted, emails masked.\n", cls: CM },
    { t: "insert into", cls: KW },
    { t: " accounts (email) ", cls: VAR },
    { t: "values", cls: KW },
    { t: " (", cls: VAR },
    { t: "'user_00418@mask.local'", cls: STR },
    { t: ");", cls: VAR },
  ],
  "package.json": [
    { t: "{\n  ", cls: DIM },
    { t: '"name"', cls: STR },
    { t: ": ", cls: DIM },
    { t: '"checkout-app"', cls: STR },
    { t: ",\n  ", cls: DIM },
    { t: '"private"', cls: STR },
    { t: ": ", cls: DIM },
    { t: "true", cls: KW },
    { t: "\n}", cls: DIM },
  ],
  "README.md": [
    { t: "# checkout-app\n", cls: "text-white" },
    { t: "Create Wind Tunnel from the pull request.\nThe safety report attaches to the check.", cls: "text-white/70" },
  ],
};

const CMD = "npx antifailure init";
const HOME_FILE = "index.tsx";

const CHECKS = [
  "Isolated twin provisioned",
  "Sanitized Postgres restored",
  "Stripe + email contained",
  "Pass / warning / block on the PR",
  "Automatic destroy scheduled",
];

const CHECK_DETAIL = [
  "Isolated twin of this PR. Destroyed when the TTL expires.",
  "Sanitized, referentially consistent Postgres. Tokens deleted.",
  "Stripe simulated in a clone-local ledger. Email captured, never delivered.",
  "Baseline vs candidate. Verdict: pass, warning, or block.",
  "Cleanup is a safety property: independent destroy path, recorded.",
];

const BOTTOM_TABS: { id: BottomTab; label: string }[] = [
  { id: "problems", label: "Problems" },
  { id: "output", label: "Output" },
  { id: "debug", label: "Debug Console" },
  { id: "terminal", label: "Terminal" },
  { id: "ports", label: "Ports" },
];

function FolderGlyph({ open }: { open: boolean }) {
  return (
    <svg viewBox="0 0 16 16" className="h-[15px] w-[15px] shrink-0" aria-hidden>
      {open ? (
        <>
          <path d="M1.5 13.2V4.8c0-.5.4-.9.9-.9h3.2l1.2 1.3h6.8c.5 0 .9.4.9.9v7.1c0 .5-.4.9-.9.9H2.4c-.5 0-.9-.4-.9-.9Z" fill="#dcb67a" />
          <path d="M1.8 6.6h12.4v6.2c0 .3-.2.5-.5.5H2.3c-.3 0-.5-.2-.5-.5V6.6Z" fill="#c9a066" />
        </>
      ) : (
        <path d="M1.6 13V4.7c0-.5.4-.9.9-.9h3.1L7 5.2h6.5c.5 0 .9.4.9.9V13c0 .5-.4.9-.9.9H2.5c-.5 0-.9-.4-.9-.9Z" fill="#dcb67a" />
      )}
    </svg>
  );
}

function FileGlyph({ name }: { name: string }) {
  const ext = name.split(".").pop();
  if (ext === "tsx" || ext === "ts") {
    return (
      <svg viewBox="0 0 16 16" className="h-[15px] w-[15px] shrink-0" aria-hidden>
        <rect x="1.5" y="1.5" width="13" height="13" rx="2.2" fill="#3178c6" />
        <path d="M4.2 8.3h7.6M8 4.4v7.2" stroke="#fff" strokeWidth="1.5" />
      </svg>
    );
  }
  if (ext === "yml" || ext === "yaml") {
    return (
      <svg viewBox="0 0 16 16" className="h-[15px] w-[15px] shrink-0" aria-hidden>
        <rect x="1.5" y="1.5" width="13" height="13" rx="2.2" fill="#cb171e" />
        <path d="M4.2 4.4h7.6M4.2 8h5.2M4.2 11.6h6.4" stroke="#fff" strokeWidth="1.3" strokeLinecap="round" />
      </svg>
    );
  }
  if (ext === "sql") {
    return (
      <svg viewBox="0 0 16 16" className="h-[15px] w-[15px] shrink-0" aria-hidden>
        <ellipse cx="8" cy="5" rx="5.2" ry="2.3" fill="#e38c3e" />
        <path d="M2.8 5v6.2c0 1.3 2.3 2.3 5.2 2.3s5.2-1 5.2-2.3V5" fill="none" stroke="#e38c3e" strokeWidth="1.6" />
        <path d="M2.8 8.2c0 1.3 2.3 2.3 5.2 2.3s5.2-1 5.2-2.3" fill="none" stroke="#e38c3e" strokeWidth="1.6" />
      </svg>
    );
  }
  if (ext === "json") {
    return (
      <svg viewBox="0 0 16 16" className="h-[15px] w-[15px] shrink-0" aria-hidden>
        <rect x="1.5" y="1.5" width="13" height="13" rx="2.2" fill="#cbcb41" />
        <path
          d="M5.2 4.2c-1.4 0-1.8 1-1.8 2v.8c0 .5-.4.8-.8.8M5.2 11.8c-1.4 0-1.8-1-1.8-2v-.8c0-.5-.4-.8-.8-.8M10.8 4.2c1.4 0 1.8 1 1.8 2v.8c0 .5.4.8.8.8M10.8 11.8c1.4 0 1.8-1 1.8-2v-.8c0-.5.4-.8.8-.8"
          fill="none"
          stroke="#3a3a00"
          strokeWidth="1.3"
          strokeLinecap="round"
        />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 16 16" className="h-[15px] w-[15px] shrink-0" aria-hidden>
      <path d="M3.2 1.6h6.1L12.8 5v9.4c0 .5-.4.9-.9.9H3.2c-.5 0-.9-.4-.9-.9V2.5c0-.5.4-.9.9-.9Z" fill="#519aba" />
      <path d="M9.2 1.8V5h3.4" fill="#3d7a96" />
    </svg>
  );
}

function Sparkle({ className = "h-3 w-3" }: { className?: string }) {
  return (
    <svg viewBox="0 0 12 12" className={className} fill="currentColor" aria-hidden>
      <path d="M6 0.4 6.7 4.6 11.2 6 6.7 7.4 6 11.6 5.3 7.4 0.8 6 5.3 4.6 6 0.4Z" />
    </svg>
  );
}

function TokenView({
  tokens,
  caret,
  echo,
}: {
  tokens: Token[];
  caret?: boolean;
  echo?: string;
}) {
  const text = tokens.map((tok) => tok.t).join("") + (echo ?? "");
  const lines = Math.max(8, text.split("\n").length);
  return (
    <div className="flex min-h-[228px] font-mono text-[13px] leading-[22px]">
      <div className="select-none py-3.5 pl-3 pr-3 text-right text-[#565656]">
        {Array.from({ length: lines }, (_, i) => (
          <div key={i}>{i + 1}</div>
        ))}
      </div>
      <pre className="min-w-0 flex-1 overflow-auto py-3.5 pr-4 outline-none">
        {tokens.map((tok, i) => (
          <span key={i} className={tok.cls || VAR}>
            {tok.t}
          </span>
        ))}
        {echo ? <span className="text-white/45">{echo}</span> : null}
        {caret ? <Caret /> : null}
      </pre>
    </div>
  );
}

function CheckTick({ on }: { on: boolean }) {
  return (
    <svg viewBox="0 0 16 16" className="mt-px h-4 w-4 shrink-0" aria-hidden>
      <motion.circle
        cx="8"
        cy="8"
        r="8"
        fill={on ? "#22c55e" : "rgba(255,255,255,0.12)"}
        transition={{ duration: 0.25 }}
      />
      <motion.path
        d="M4.4 8.15 L6.9 10.5 L11.6 5.5"
        fill="none"
        stroke={on ? "#052e16" : "transparent"}
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
        initial={{ pathLength: 0 }}
        animate={{ pathLength: on ? 1 : 0 }}
        transition={{ duration: 0.35, ease: EASE, delay: on ? 0.05 : 0 }}
      />
    </svg>
  );
}

function isVisible(item: TreeItem, collapsed: Set<string>) {
  let p = item.parent;
  while (p) {
    if (collapsed.has(p)) return false;
    p = TREE.find((n) => n.id === p)?.parent ?? null;
  }
  return true;
}

function IdePlay() {
  const ref = useRef<HTMLDivElement>(null);
  const editorRef = useRef<HTMLDivElement>(null);
  const { story, reduced, inView } = useInViewPlay(ref, 0.2);
  const [cmdChars, setCmdChars] = useState(0);
  const [term, setTerm] = useState(0);
  const [checks, setChecks] = useState(0);
  const [spin, setSpin] = useState(false);
  const [wash, setWash] = useState(false);
  const [runId, setRunId] = useState(0);
  const [activeFile, setActiveFile] = useState(HOME_FILE);
  const [openTabs, setOpenTabs] = useState<string[]>([HOME_FILE]);
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set(["lib"]));
  const [bottomTab, setBottomTab] = useState<BottomTab>("terminal");
  const [caretOn, setCaretOn] = useState(false);
  const [echo, setEcho] = useState("");
  const [inspectCheck, setInspectCheck] = useState<number | null>(null);
  const [tip, setTip] = useState<string | null>(null);
  const cmdRef = useRef(0);
  const termRef = useRef(0);
  const checksRef = useRef(0);
  const echoTimer = useRef(0);

  useEffect(() => {
    if (!story) {
      setCmdChars(0);
      setTerm(0);
      setChecks(0);
      setSpin(false);
      setWash(false);
      setActiveFile(HOME_FILE);
      setOpenTabs([HOME_FILE]);
      setCollapsed(new Set(["lib"]));
      setBottomTab("terminal");
      setCaretOn(false);
      setEcho("");
      setInspectCheck(null);
      cmdRef.current = 0;
      termRef.current = 0;
      checksRef.current = 0;
      return;
    }
    if (reduced) {
      setCmdChars(CMD.length);
      setTerm(5);
      setChecks(5);
      cmdRef.current = CMD.length;
      termRef.current = 5;
      checksRef.current = 5;
    }
  }, [story, reduced]);

  useEffect(() => {
    if (!story || reduced || !inView) return;
    if (termRef.current >= 5 && checksRef.current >= 5) return;

    let cancelled = false;
    const timers: number[] = [];
    const later = (ms: number, fn: () => void) => {
      timers.push(window.setTimeout(fn, ms));
    };

    if (termRef.current < 1) {
      setSpin(true);
      later(180, () => {
        if (cancelled) return;
        const typeCmd = window.setInterval(() => {
          if (cancelled) return;
          cmdRef.current += 1;
          setCmdChars(cmdRef.current);
          if (cmdRef.current >= CMD.length) {
            window.clearInterval(typeCmd);
            termRef.current = 1;
            setTerm(1);
          }
        }, 28);
        timers.push(typeCmd);
      });
    }

    const scheduleFrom = termRef.current;
    const steps: [number, number][] = [
      [2, 1100],
      [3, 2000],
      [4, 2900],
      [5, 3600],
    ];
    for (const [step, ms] of steps) {
      if (scheduleFrom >= step) continue;
      later(ms, () => {
        if (cancelled) return;
        termRef.current = step;
        setTerm(step);
        if (step === 5) {
          setSpin(false);
          setWash(true);
        }
      });
    }
    for (let i = 1; i <= 5; i += 1) {
      if (checksRef.current >= i) continue;
      later(3900 + i * 400, () => {
        if (cancelled) return;
        checksRef.current = i;
        setChecks(i);
      });
    }

    return () => {
      cancelled = true;
      timers.forEach(clearTimeout);
    };
  }, [story, reduced, inView, runId]);

  const termLines = useMemo(() => {
    const lines: { text: string; cls?: string; success?: boolean }[] = [];
    if (term >= 1 || cmdChars > 0) {
      lines.push({ text: CMD.slice(0, Math.max(cmdChars, term >= 1 ? CMD.length : 0)) });
    }
    if (term >= 2) {
      lines.push({ text: "" });
      lines.push({ text: "Wind Tunnel Initialization" });
      lines.push({ text: "Step 1/3: GitHub + customer-hosted runner" });
    }
    if (term >= 3) lines.push({ text: "Step 2/3: Sanitized snapshot + Stripe/email sinks" });
    if (term >= 4) lines.push({ text: "Step 3/3: Baseline vs candidate migration" });
    if (term >= 5) {
      lines.push({ text: "Success! Safety report attached to the PR.", cls: "text-[#3ddc84]", success: true });
    }
    return lines;
  }, [term, cmdChars]);

  const openFile = useCallback((id: string) => {
    if (!FILE_TOKENS[id]) return;
    setActiveFile(id);
    setOpenTabs((tabs) => (tabs.includes(id) ? tabs : [...tabs, id]));
    setCaretOn(false);
    setEcho("");
  }, []);

  const closeTab = useCallback(
    (id: string) => {
      if (id === HOME_FILE) return;
      setOpenTabs((tabs) => {
        const next = tabs.filter((t) => t !== id);
        if (activeFile === id) setActiveFile(next[next.length - 1] ?? HOME_FILE);
        return next.length ? next : [HOME_FILE];
      });
    },
    [activeFile],
  );

  const toggleFolder = useCallback((id: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const replayRun = useCallback(() => {
    setBottomTab("terminal");
    setInspectCheck(null);
    setCmdChars(0);
    setTerm(0);
    setChecks(0);
    setSpin(true);
    setWash(false);
    cmdRef.current = 0;
    termRef.current = 0;
    checksRef.current = 0;
    setRunId((n) => n + 1);
  }, []);

  const inspect = useCallback((i: number) => {
    setInspectCheck(i);
    setBottomTab("output");
  }, []);

  const onEditorKey = useCallback(
    (e: KeyboardEvent<HTMLDivElement>) => {
      if (!caretOn) return;
      if (e.key === "Escape") {
        setCaretOn(false);
        setEcho("");
        return;
      }
      if (e.key === "Backspace") {
        e.preventDefault();
        setEcho((s) => s.slice(0, -1));
        return;
      }
      if (e.key.length === 1 && echo.length < 24) {
        e.preventDefault();
        setEcho((s) => s + e.key);
        window.clearTimeout(echoTimer.current);
        echoTimer.current = window.setTimeout(() => setEcho(""), 1600);
      }
    },
    [caretOn, echo.length],
  );

  const tokens = FILE_TOKENS[activeFile] ?? FILE_TOKENS[HOME_FILE];

  let bottomBody: ReactNode;
  if (bottomTab === "problems") {
    bottomBody =
      inspectCheck === 3 ? (
        <div>
          <span className="text-red-400">BLOCK / schema.lock</span>
          {"  ACCESS EXCLUSIVE on subscriptions 27.4s"}
        </div>
      ) : (
        <div className="text-white/45">No problems have been detected.</div>
      );
  } else if (bottomTab === "output") {
    bottomBody =
      inspectCheck != null ? (
        <div>{CHECK_DETAIL[inspectCheck]}</div>
      ) : (
        <div className="space-y-1">
          <div>GitHub + customer-hosted runner</div>
          <div>Sanitized snapshot + Stripe/email sinks</div>
          <div>Baseline vs candidate migration</div>
          {term >= 5 ? <div className="text-[#3ddc84]">Safety report attached to the PR.</div> : null}
        </div>
      );
  } else if (bottomTab === "debug") {
    bottomBody = (
      <div>
        <span className="text-white/40">{"> "}</span>
        <Caret />
      </div>
    );
  } else if (bottomTab === "ports") {
    bottomBody = (
      <div>
        <div className="text-white/45">Private</div>
        <div>fix-billing-184.preview.company.com</div>
      </div>
    );
  } else {
    bottomBody = (
      <>
        {termLines.map((l, i) => (
          <div key={i} className={l.cls}>
            {i === 0 ? (
              <>
                <span className="text-white/40">$ </span>
                {l.text}
                {term < 1 && cmdChars < CMD.length && inView ? <Caret /> : null}
              </>
            ) : l.success ? (
              <span className="inline-flex items-center gap-1.5">
                <span className="inline-block h-[7px] w-[7px] rounded-[1px] bg-[#3ddc84]" />
                {l.text}
              </span>
            ) : (
              l.text
            )}
          </div>
        ))}
        {spin ? (
          <span className="mt-1 inline-flex items-center gap-1.5 text-white/40">
            <span
              className="wt-spin inline-block h-2.5 w-2.5 rounded-full border border-white/30 border-t-white/80"
              style={{ animation: "wt-spin 0.7s linear infinite" }}
            />
            running
          </span>
        ) : null}
      </>
    );
  }

  return (
    <div
      ref={ref}
      className="relative overflow-hidden rounded-2xl border border-white/10 bg-[#0c0c0c] shadow-[-48px_0_90px_rgba(26,212,192,0.18),48px_0_90px_rgba(232,148,64,0.22),0_40px_90px_rgba(0,0,0,0.62)]"
    >
      <div className="flex h-[42px] items-center border-b border-white/[0.07] px-3.5 text-[12px] text-white/40">
        <div className="flex gap-[6px]">
          <span className="h-3 w-3 rounded-full bg-[#ff5f57]" />
          <span className="h-3 w-3 rounded-full bg-[#febc2e]" />
          <span className="h-3 w-3 rounded-full bg-[#28c840]" />
        </div>
        <div className="flex-1 text-center tracking-[-0.01em]">Your Code Editor</div>
      </div>
      <div className="grid grid-cols-[214px_1fr] lg:grid-cols-[214px_1fr_minmax(270px,312px)]">
        <div className="relative border-r border-white/[0.07] bg-[#0b0b0b] py-2.5 text-[12.5px]">
          {TREE.map((f) => {
            if (!isVisible(f, collapsed)) return null;
            const isActive = !f.folder && f.id === activeFile;
            const open = f.folder && !collapsed.has(f.id);
            return (
              <button
                key={f.id}
                type="button"
                title={f.path}
                className={`flex w-full items-center gap-1.5 py-[3px] pr-2 text-left hover:bg-white/[0.04] ${
                  isActive ? "bg-white/[0.08] text-white" : "text-white/65"
                }`}
                style={{ paddingLeft: 10 + f.indent * 12 }}
                onMouseEnter={() => setTip(f.path)}
                onMouseLeave={() => setTip(null)}
                onClick={() => {
                  if (f.folder) toggleFolder(f.id);
                  else openFile(f.id);
                }}
              >
                {f.folder ? (
                  <span className="w-2.5 text-[9px] text-white/35">{open ? "▾" : "▸"}</span>
                ) : (
                  <span className="w-2.5" />
                )}
                {f.folder ? <FolderGlyph open={!!open} /> : <FileGlyph name={f.name} />}
                <span className="truncate">{f.name}</span>
              </button>
            );
          })}
          {tip ? (
            <div className="pointer-events-none absolute bottom-2 left-2 right-2 truncate rounded bg-black/80 px-2 py-1 font-mono text-[10px] text-white/70">
              {tip}
            </div>
          ) : null}
        </div>
        <div className="min-w-0 bg-[#0a0a0a]">
          <div className="flex overflow-x-auto border-b border-white/[0.07] px-1 text-[12.5px]">
            {openTabs.map((id) => {
              const on = id === activeFile;
              return (
                <span
                  key={id}
                  className="flex shrink-0 items-center px-1"
                  style={{
                    borderBottom: on ? "1px solid rgba(255,255,255,0.78)" : "1px solid transparent",
                  }}
                >
                  <button
                    type="button"
                    className={`flex items-center gap-1.5 px-2 py-2 ${on ? "text-white" : "text-white/40 hover:text-white/70"}`}
                    onClick={() => openFile(id)}
                  >
                    <FileGlyph name={id} />
                    {id}
                  </button>
                  {id !== HOME_FILE ? (
                    <button
                      type="button"
                      className="pr-1.5 text-[11px] text-white/30 hover:text-white/70"
                      aria-label={`Close ${id}`}
                      onClick={(e) => {
                        e.stopPropagation();
                        closeTab(id);
                      }}
                    >
                      ×
                    </button>
                  ) : (
                    <span className="pr-1.5 text-[11px] text-white/25">×</span>
                  )}
                </span>
              );
            })}
          </div>
          <div
            ref={editorRef}
            tabIndex={0}
            className="cursor-text outline-none"
            onClick={() => {
              setCaretOn(true);
              editorRef.current?.focus();
            }}
            onKeyDown={onEditorKey}
            onBlur={() => {
              setCaretOn(false);
              setEcho("");
            }}
          >
            <TokenView tokens={tokens} caret={caretOn} echo={caretOn ? echo : ""} />
          </div>
          <div className="relative border-t border-white/[0.07]">
            {wash && bottomTab === "terminal" ? (
              <div
                className="pointer-events-none absolute inset-0 bg-[#3ddc84]/8"
                style={{ animation: "wt-sheen 0.9s cubic-bezier(0.16,1,0.3,1) 1" }}
              />
            ) : null}
            <div className="flex gap-5 px-3 text-[11.5px] text-white/35">
              {BOTTOM_TABS.map((tab) => (
                <button
                  key={tab.id}
                  type="button"
                  className={`border-b py-1.5 ${
                    bottomTab === tab.id
                      ? "border-white text-white"
                      : "border-transparent hover:text-white/70"
                  }`}
                  onClick={() => setBottomTab(tab.id)}
                >
                  {tab.label}
                </button>
              ))}
            </div>
            <pre className="min-h-[128px] px-4 pb-4 font-mono text-[12px] leading-[20px] text-white/72">{bottomBody}</pre>
          </div>
        </div>
        <div className="hidden bg-[#0c0c0c] p-2 lg:block">
          <div className="flex h-full flex-col rounded-[12px] border border-white/[0.08] bg-[#161616] shadow-[0_16px_48px_rgba(0,0,0,0.45)]">
            <div className="flex items-center justify-between px-3.5 pt-3.5 text-[13px] font-medium text-white">
              Create Wind Tunnel
              <span className="text-[15px] tracking-[0.2em] text-white/35">···</span>
            </div>
            <p className="mt-3 px-3.5 text-[12px] leading-[18px] text-white/55">
              One click turns this pull request into a private, production-shaped environment with its own
              safe database and integrations.
            </p>
            <pre className="mx-3.5 mt-3 overflow-hidden rounded-lg bg-[#0c0c0c] p-2.5 font-mono text-[10.5px] leading-[17px] text-[#9cdcfe]">
              {`# Generated — do not hand-author
on_pr: create_wind_tunnel
contain:
  - stripe
  - email
compare: baseline_vs_candidate`}
            </pre>
            <div className="mx-3.5 mt-3 rounded-lg bg-[#0c0c0c] px-2.5 py-2">
              <div className="flex items-center gap-2 text-[12px]">
                {term >= 5 ? (
                  <CheckTick on />
                ) : (
                  <span
                    className="wt-spin inline-block h-3.5 w-3.5 rounded-full border border-white/25 border-t-white/80"
                    style={{ animation: spin ? "wt-spin 0.7s linear infinite" : "none" }}
                  />
                )}
                <span className="text-white/45">Run</span>
                <span className="font-medium text-white">create_wind_tunnel</span>
              </div>
              <div className="mt-0.5 pl-6 text-[11px] text-white/40">
                {term >= 5 ? "Isolated twin provisioned." : spin ? "Provisioning…" : "Waiting for init"}
              </div>
            </div>
            <ul className="mt-3 space-y-2 px-3.5 text-[12.5px]">
              {CHECKS.map((item, i) => (
                <li key={item}>
                  <button
                    type="button"
                    className="flex w-full items-start gap-2 text-left hover:text-white"
                    style={{
                      color:
                        inspectCheck === i
                          ? "rgba(255,255,255,0.95)"
                          : checks > i
                            ? "rgba(255,255,255,0.82)"
                            : "rgba(255,255,255,0.28)",
                      transition: "color 0.35s cubic-bezier(0.16,1,0.3,1)",
                    }}
                    title={CHECK_DETAIL[i]}
                    onClick={() => inspect(i)}
                  >
                    <CheckTick on={checks > i} />
                    {item}
                  </button>
                </li>
              ))}
            </ul>
            <div className="mt-auto p-2.5">
              <button
                type="button"
                className="flex w-full flex-col rounded-[12px] border border-white/10 bg-[#101010] px-3 py-2.5 text-left hover:border-white/18"
                onClick={replayRun}
              >
                <span className="text-[12.5px] text-white/40">Plan, search, build anything...</span>
                <span className="mt-2.5 flex items-center gap-2">
                  <span className="inline-flex items-center gap-1 rounded-md border border-white/10 bg-white/5 px-1.5 py-[3px] text-[11px] text-white/80">
                    <Sparkle className="h-2.5 w-2.5 text-white/70" />
                    + Agent
                  </span>
                  <span className="inline-flex items-center gap-1 text-[11px] text-white/55">
                    GPT-5
                    <span className="text-[8px]">▾</span>
                  </span>
                  <span className="ml-auto flex h-6 w-6 items-center justify-center rounded-md bg-white/10 text-white">
                    <svg viewBox="0 0 12 12" className="h-3 w-3" aria-hidden>
                      <path d="M6 9.5V2.5M3 5l3-3 3 3" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
                    </svg>
                  </span>
                </span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

export function IdeSection() {
  const glow = useRef<HTMLDivElement>(null);
  const { story } = useInViewPlay(glow, 0.15);

  return (
    <section className="relative overflow-hidden bg-black px-6 pb-10 pt-6 lg:px-12" id="ide">
      <div className="flex gap-6">
        <div className="hidden w-[200px] shrink-0 lg:block" />
        <div className="relative mx-auto min-w-0 max-w-[1180px] flex-1 py-12 lg:py-16">
          <div className="pointer-events-none absolute -inset-x-10 -inset-y-4 lg:-inset-x-24" aria-hidden>
            <div className="ide-grid absolute inset-0 opacity-[0.28]" />
            <div
              className="absolute left-[-12%] top-[12%] h-[78%] w-[48%] rounded-full bg-[#1ad4c0] opacity-55 blur-[100px]"
              style={{ opacity: story ? 0.58 : 0.32 }}
            />
            <div
              className="absolute right-[-14%] top-[10%] h-[80%] w-[50%] rounded-full bg-[#e89440] opacity-60 blur-[100px]"
              style={{ opacity: story ? 0.62 : 0.34 }}
            />
            <div
              className="absolute inset-0"
              style={{
                background:
                  "radial-gradient(ellipse 50% 82% at 0% 50%, rgba(26,212,192,0.7), transparent 68%), radial-gradient(ellipse 52% 84% at 100% 50%, rgba(232,148,64,0.78), transparent 68%)",
                opacity: story ? 1 : 0.6,
                transition: "opacity 0.8s ease",
              }}
            />
            <div
              ref={glow}
              className="ide-dots absolute inset-0 mix-blend-soft-light"
              style={{
                WebkitMaskImage:
                  "radial-gradient(ellipse 52% 86% at 0% 50%, black 0%, transparent 70%), radial-gradient(ellipse 54% 88% at 100% 50%, black 0%, transparent 70%)",
                maskImage:
                  "radial-gradient(ellipse 52% 86% at 0% 50%, black 0%, transparent 70%), radial-gradient(ellipse 54% 88% at 100% 50%, black 0%, transparent 70%)",
                opacity: story ? 1 : 0.55,
                transition: "opacity 0.8s ease",
              }}
            />
            <div
              className="auth-honeycomb absolute inset-0 mix-blend-screen"
              style={{
                WebkitMaskImage:
                  "linear-gradient(90deg, rgba(0,0,0,0.85), transparent 36%, transparent 64%, rgba(0,0,0,0.85))",
                maskImage:
                  "linear-gradient(90deg, rgba(0,0,0,0.85), transparent 36%, transparent 64%, rgba(0,0,0,0.85))",
                opacity: 0.55,
              }}
            />
          </div>
          <div className="pointer-events-none absolute inset-x-0 top-[42px] h-px bg-white/10" aria-hidden />
          <div className="pointer-events-none absolute inset-y-12 left-0 w-px bg-white/10 lg:inset-y-16" aria-hidden />
          <div className="pointer-events-none absolute inset-y-12 right-0 w-px bg-white/10 lg:inset-y-16" aria-hidden />
          <div className="relative">
            <IdePlay />
            <div className="mt-5 flex flex-wrap items-center justify-between gap-4 px-1">
              <p className="text-[15px] tracking-[-0.01em] text-white">
                Try for yourself, prove the next deploy before it ships.
              </p>
              <CopyCli variant="mint" />
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
