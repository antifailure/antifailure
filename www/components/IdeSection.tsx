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
  { id: "scenarios", name: "scenarios", indent: 1, folder: true, parent: "root", path: "scenarios" },
  { id: "impatient.ts", name: "impatient.ts", indent: 2, parent: "scenarios", path: "scenarios/impatient.ts" },
  { id: "wind-tunnel.yml", name: "wind-tunnel.yml", indent: 1, parent: "root", path: "wind-tunnel.yml" },
  { id: "seed.sql", name: "seed.sql", indent: 1, parent: "root", path: "seed.sql" },
  { id: "tsconfig.json", name: "tsconfig.json", indent: 1, parent: "root", path: "tsconfig.json" },
  { id: "package.json", name: "package.json", indent: 1, parent: "root", path: "package.json" },
  { id: "README.md", name: "README.md", indent: 1, parent: "root", path: "README.md" },
];

const KW = "text-[#0550ae]";
const FN = "text-[#6f42c1]";
const VAR = "text-[#0550ae]";
const STR = "text-[#a31515]";
const CM = "text-[#22863a]";
const DIM = "text-black/50";
const TEAL = "text-[#116329]";

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
    { t: "Workload", cls: TEAL },
    { t: " } ", cls: VAR },
    { t: "from", cls: KW },
    { t: " ", cls: VAR },
    { t: "'@antifailure/studio'", cls: STR },
    { t: ";\n\n", cls: VAR },
    { t: "export default async function", cls: KW },
    { t: " handler", cls: FN },
    { t: "(req, res) {\n  ", cls: VAR },
    { t: "const", cls: KW },
    { t: " result = ", cls: VAR },
    { t: "await", cls: KW },
    { t: " Workload", cls: TEAL },
    { t: ".run", cls: FN },
    { t: "(", cls: VAR },
    { t: "'impatient'", cls: STR },
    { t: ");\n\n  ", cls: VAR },
    { t: "res", cls: VAR },
    { t: ".status", cls: FN },
    { t: "(", cls: VAR },
    { t: "200", cls: "text-[#116329]" },
    { t: ").", cls: VAR },
    { t: "json", cls: FN },
    { t: "({ verdict: result });\n}", cls: VAR },
  ],
  "impatient.ts": [
    { t: "import", cls: KW },
    { t: " { ", cls: VAR },
    { t: "Workload", cls: TEAL },
    { t: " } ", cls: VAR },
    { t: "from", cls: KW },
    { t: " ", cls: VAR },
    { t: '"@antifailure/studio"', cls: STR },
    { t: "\n\n", cls: VAR },
    { t: "export const", cls: KW },
    { t: " impatientUpgrade", cls: FN },
    { t: " = ", cls: VAR },
    { t: "Workload", cls: TEAL },
    { t: ".compile", cls: FN },
    { t: "(\n  ", cls: VAR },
    { t: '"explore:double_click_upgrade"', cls: STR },
    { t: "\n)", cls: VAR },
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
  "tsconfig.json": [
    { t: "{\n  ", cls: DIM },
    { t: '"compilerOptions"', cls: STR },
    { t: ": { ", cls: DIM },
    { t: '"strict"', cls: STR },
    { t: ": ", cls: DIM },
    { t: "true", cls: KW },
    { t: " }\n}", cls: DIM },
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
    { t: "# checkout-app\n", cls: "text-black" },
    { t: "Create Wind Tunnel from the pull request.\nThe safety report attaches to the check.", cls: "text-black/70" },
  ],
};

const CMD = "curl -fsSL https://antifailure.dev/install.sh | sh";
const HOME_FILE = "index.tsx";

const CHECKS = [
  "Isolated twin provisioned: 'Checkout App'",
  "Imported observed production patterns",
  "Compiled scenarios/impatient.ts",
  "Contained Stripe + email in the twin",
];

const CHECK_DETAIL = [
  "Isolated twin of this PR. Destroyed when the TTL expires.",
  "Redacted production-shaped traffic. Never diverted from live users.",
  "Exploratory discoveries compiled to versioned IR. No LLM at scale.",
  "Stripe simulated in a clone-local ledger. Email captured, never delivered.",
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
        {echo ? <span className="text-black/45">{echo}</span> : null}
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
        fill={on ? "#22c55e" : "rgba(0,0,0,0.12)"}
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

export function IdePlay() {
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
      setChecks(CHECKS.length);
      cmdRef.current = CMD.length;
      termRef.current = 5;
      checksRef.current = CHECKS.length;
    }
  }, [story, reduced]);

  useEffect(() => {
    if (!story || reduced || !inView) return;
    if (termRef.current >= 5 && checksRef.current >= CHECKS.length) return;

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
    for (let i = 1; i <= CHECKS.length; i += 1) {
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
      lines.push({ text: "Antifailure Initialization" });
      lines.push({ text: "Step 1/3: Connecting isolated twin..." });
    }
    if (term >= 3) lines.push({ text: "Step 2/3: Importing observed patterns..." });
    if (term >= 4) lines.push({ text: "Step 3/3: Compiling deterministic journeys..." });
    if (term >= 5) {
      lines.push({ text: "Success! Workload Studio initialized.", cls: "text-[#16a34a]", success: true });
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
        <div className="text-black/45">No problems have been detected.</div>
      );
  } else if (bottomTab === "output") {
    bottomBody =
      inspectCheck != null ? (
        <div>{CHECK_DETAIL[inspectCheck]}</div>
      ) : (
        <div className="space-y-1">
          <div>Connecting isolated twin</div>
          <div>Importing observed patterns</div>
          <div>Compiling deterministic journeys</div>
          {term >= 5 ? <div className="text-[#16a34a]">Workload Studio initialized.</div> : null}
        </div>
      );
  } else if (bottomTab === "debug") {
    bottomBody = (
      <div>
        <span className="text-black/40">{"> "}</span>
        <Caret />
      </div>
    );
  } else if (bottomTab === "ports") {
    bottomBody = (
      <div>
        <div className="text-black/45">Private</div>
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
                <span className="text-black/40">$ </span>
                {l.text}
                {term < 1 && cmdChars < CMD.length && inView ? <Caret /> : null}
              </>
            ) : l.success ? (
              <span className="inline-flex items-center gap-1.5">
                <span className="inline-block h-[7px] w-[7px] rounded-[1px] bg-[#16a34a]" />
                {l.text}
              </span>
            ) : (
              l.text
            )}
          </div>
        ))}
        {spin ? (
          <span className="mt-1 inline-flex items-center gap-1.5 text-black/40">
            <span
              className="wt-spin inline-block h-2.5 w-2.5 rounded-full border border-black/30 border-t-black/80"
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
      className="relative overflow-hidden rounded-2xl border border-black/10 bg-white shadow-[0_40px_90px_rgba(0,0,0,0.12)]"
    >
      <div className="flex h-[42px] items-center border-b border-black/[0.07] px-3.5 text-[12px] text-black/40">
        <div className="flex gap-[6px]">
          <span className="h-3 w-3 rounded-full bg-[#ff5f57]" />
          <span className="h-3 w-3 rounded-full bg-[#febc2e]" />
          <span className="h-3 w-3 rounded-full bg-[#28c840]" />
        </div>
        <div className="flex-1 text-center tracking-[-0.01em]">Your Code Editor</div>
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-[200px_1fr] lg:grid-cols-[214px_1fr_minmax(270px,312px)]">
        <div className="relative hidden border-r border-black/[0.07] bg-[#f4f4f2] py-2.5 text-[12.5px] sm:block">
          {TREE.map((f) => {
            if (!isVisible(f, collapsed)) return null;
            const isActive = !f.folder && f.id === activeFile;
            const open = f.folder && !collapsed.has(f.id);
            return (
              <button
                key={f.id}
                type="button"
                title={f.path}
                className={`flex w-full items-center gap-1.5 py-[3px] pr-2 text-left hover:bg-black/[0.04] ${
                  isActive ? "bg-black/[0.08] text-black" : "text-black/65"
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
                  <span className="w-2.5 text-[9px] text-black/35">{open ? "▾" : "▸"}</span>
                ) : (
                  <span className="w-2.5" />
                )}
                {f.folder ? <FolderGlyph open={!!open} /> : <FileGlyph name={f.name} />}
                <span className="truncate">{f.name}</span>
              </button>
            );
          })}
          {tip ? (
            <div className="pointer-events-none absolute bottom-2 left-2 right-2 truncate rounded bg-white px-2 py-1 font-mono text-[10px] text-black/70 shadow">
              {tip}
            </div>
          ) : null}
        </div>
        <div className="min-w-0 bg-white">
          <div className="flex overflow-x-auto border-b border-black/[0.07] px-1 text-[12.5px]">
            {openTabs.map((id) => {
              const on = id === activeFile;
              return (
                <span
                  key={id}
                  className="flex shrink-0 items-center px-1"
                  style={{
                    borderBottom: on ? "1px solid rgba(0,0,0,0.78)" : "1px solid transparent",
                  }}
                >
                  <button
                    type="button"
                    className={`flex items-center gap-1.5 px-2 py-2 ${on ? "text-black" : "text-black/40 hover:text-black/70"}`}
                    onClick={() => openFile(id)}
                  >
                    <FileGlyph name={id} />
                    {id}
                  </button>
                  {id !== HOME_FILE ? (
                    <button
                      type="button"
                      className="pr-1.5 text-[11px] text-black/30 hover:text-black/70"
                      aria-label={`Close ${id}`}
                      onClick={(e) => {
                        e.stopPropagation();
                        closeTab(id);
                      }}
                    >
                      ×
                    </button>
                  ) : (
                    <span className="pr-1.5 text-[11px] text-black/25">×</span>
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
          <div className="relative border-t border-black/[0.07]">
            {wash && bottomTab === "terminal" ? (
              <div
                className="pointer-events-none absolute inset-0 bg-[#16a34a]/8"
                style={{ animation: "wt-sheen 0.9s cubic-bezier(0.16,1,0.3,1) 1" }}
              />
            ) : null}
            <div className="flex gap-5 px-3 text-[11.5px] text-black/35">
              {BOTTOM_TABS.map((tab) => (
                <button
                  key={tab.id}
                  type="button"
                  className={`border-b py-1.5 ${
                    bottomTab === tab.id
                      ? "border-black text-black"
                      : "border-transparent hover:text-black/70"
                  }`}
                  onClick={() => setBottomTab(tab.id)}
                >
                  {tab.label}
                </button>
              ))}
            </div>
            <pre className="min-h-[128px] px-4 pb-4 font-mono text-[12px] leading-[20px] text-black/72">{bottomBody}</pre>
          </div>
        </div>
        <div className="hidden bg-[#f4f4f2] p-2 lg:block">
          <div className="flex h-full flex-col rounded-[12px] border border-black/[0.08] bg-white shadow-[0_16px_48px_rgba(0,0,0,0.08)]">
            <div className="flex items-center justify-between px-3.5 pt-3.5 text-[13px] font-medium text-black">
              Getting started with Antifailure
              <span className="text-[15px] tracking-[0.2em] text-black/35">···</span>
            </div>
            <p className="mt-3 px-3.5 text-[12px] leading-[18px] text-black/55">
              Workload Studio compiles observed traffic, deterministic journeys, and exploratory
              users into a scenario that runs against an isolated twin.
            </p>
            <pre className="mx-3.5 mt-3 overflow-hidden rounded-lg bg-[#f4f4f2] p-2.5 font-mono text-[10.5px] leading-[17px]">
              <span className="mb-1 block text-[10px] text-black/35">workload.ts</span>
              <span className={KW}>import</span>
              <span className={VAR}>{" { "}</span>
              <span className={TEAL}>Workload</span>
              <span className={VAR}>{" } "}</span>
              <span className={KW}>from</span>
              <span className={STR}>{` "@antifailure/studio"`}</span>
              <span className={VAR}>{";\n"}</span>
              <span className={KW}>const</span>
              <span className={VAR}> studio = </span>
              <span className={TEAL}>Workload</span>
              <span className={FN}>.connect</span>
              <span className={VAR}>{"();\n"}</span>
              <span className={CM}>{"// Observed, deterministic, exploratory"}</span>
            </pre>
            <ul className="mt-3 space-y-2 px-3.5 text-[12.5px]">
              {CHECKS.map((item, i) => (
                <li key={item}>
                  <button
                    type="button"
                    className="flex w-full items-start gap-2 text-left hover:text-black"
                    style={{
                      color:
                        inspectCheck === i
                          ? "rgba(0,0,0,0.95)"
                          : checks > i
                            ? "rgba(0,0,0,0.82)"
                            : "rgba(0,0,0,0.28)",
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
                className="flex w-full flex-col rounded-[12px] border border-black/10 bg-[#f4f4f2] px-3 py-2.5 text-left hover:border-black/18"
                onClick={replayRun}
              >
                <span className="text-[12.5px] text-black/40">Plan, search, build anything...</span>
                <span className="mt-2.5 flex items-center gap-2">
                  <span className="inline-flex items-center gap-1 rounded-full border border-black/10 bg-white px-2 py-[3px] text-[11px] text-black/80">
                    <Sparkle className="h-2.5 w-2.5 text-black/70" />
                    Agent
                  </span>
                  <span className="inline-flex items-center gap-1 rounded-full border border-black/10 bg-white px-2 py-[3px] text-[11px] text-black/55">
                    GPT-5
                    <span className="text-[8px]">▾</span>
                  </span>
                  <span className="ml-auto flex h-6 w-6 items-center justify-center rounded-full bg-black text-white">
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
    <section className="relative bg-[#f7f7f5] px-6 pb-24 pt-16 lg:px-12 lg:pt-20" id="ide">
      <div className="flex gap-6">
        <div className="hidden w-[200px] shrink-0 lg:block" />
        <div className="relative mx-auto min-w-0 max-w-[1240px] flex-1">
          <h2
            id="from-pr"
            className="max-w-[920px] scroll-mt-[88px] text-[36px] font-semibold leading-[1.12] tracking-[-0.035em] text-black md:text-[44px] lg:text-[48px]"
          >
            A disposable production twin for every risky change.{" "}
            <span className="text-black/40">
              Connect a repository and cloud environment. The platform proves whether it is safe to ship.
            </span>
          </h2>

          <div className="relative mt-14 overflow-hidden border border-black/12 bg-[#f7f7f5]">
            <div className="pointer-events-none absolute inset-0" aria-hidden>
              <div
                className="absolute inset-0"
                style={{
                  background:
                    "radial-gradient(ellipse 58% 52% at 6% 100%, rgba(51,191,0,0.58), transparent 62%), radial-gradient(ellipse 58% 52% at 94% 100%, rgba(0,229,153,0.55), transparent 62%)",
                  opacity: story ? 1 : 0.7,
                  transition: "opacity 0.8s ease",
                }}
              />
              <div
                ref={glow}
                className="ide-dots absolute inset-0"
                style={{
                  WebkitMaskImage:
                    "radial-gradient(ellipse 60% 55% at 8% 100%, black 0%, transparent 70%), radial-gradient(ellipse 60% 55% at 92% 100%, black 0%, transparent 70%)",
                  maskImage:
                    "radial-gradient(ellipse 60% 55% at 8% 100%, black 0%, transparent 70%), radial-gradient(ellipse 60% 55% at 92% 100%, black 0%, transparent 70%)",
                  opacity: story ? 0.85 : 0.5,
                  transition: "opacity 0.8s ease",
                }}
              />
              <div
                className="auth-honeycomb absolute inset-0"
                style={{
                  WebkitMaskImage:
                    "linear-gradient(to top, rgba(0,0,0,0.7) 0%, transparent 55%), linear-gradient(90deg, rgba(0,0,0,0.55), transparent 40%, transparent 60%, rgba(0,0,0,0.55))",
                  maskImage:
                    "linear-gradient(to top, rgba(0,0,0,0.7) 0%, transparent 55%), linear-gradient(90deg, rgba(0,0,0,0.55), transparent 40%, transparent 60%, rgba(0,0,0,0.55))",
                  opacity: 0.4,
                }}
              />
            </div>

            <div className="relative px-6 pb-8 pt-[72px] sm:px-10 lg:px-14 lg:pb-10 lg:pt-[88px]">
              <div
                className="pointer-events-none absolute left-[16%] top-5 hidden items-start gap-2 lg:flex"
                aria-hidden
              >
                <span className="h-[56px] w-px bg-black/25" />
                <span className="pt-0.5 text-[12px] leading-4 text-black/45">
                  Attaches the safety report to the PR
                </span>
              </div>
              <div
                className="pointer-events-none absolute right-[14%] top-5 hidden items-start gap-2 lg:flex"
                aria-hidden
              >
                <span className="h-[56px] w-px bg-black/25" />
                <span className="pt-0.5 text-[12px] leading-4 text-black/45">
                  Provisions an isolated twin with contained integrations
                </span>
              </div>
              <IdePlay />
            </div>

            <div className="relative flex flex-wrap items-center justify-between gap-4 border-t border-black/12 px-6 py-5 sm:px-10 lg:px-14">
              <p className="text-[15px] tracking-[-0.01em] text-black">
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

