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
  { id: "migrations", name: "migrations", indent: 1, folder: true, parent: "root", path: "migrations" },
  { id: "0002_access_tier.sql", name: "0002_access_tier.sql", indent: 2, parent: "migrations", path: "migrations/0002_access_tier.sql" },
  { id: "antifailure.yaml", name: "antifailure.yaml", indent: 1, parent: "root", path: "antifailure.yaml" },
  { id: "masking.yaml", name: "masking.yaml", indent: 1, parent: "root", path: "masking.yaml" },
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

/**
 * The manifest, in keys the schema actually has.
 *
 * This block used to be a file called wind-tunnel.yml holding repository,
 * cloud, source, contain, compare and on_pr, none of which exist in
 * schemas/manifest.v1.json. The file is antifailure.yaml and the schema closes
 * itself, so a developer who copied what was on the homepage got AF-MAN-002
 * and a manifest the CLI refused.
 */
const YML: Token[] = [
  { t: "# af init wrote this. Edit it.\n", cls: CM },
  { t: "version", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "1\n", cls: TEAL },
  { t: "name", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "checkout-app\n", cls: STR },
  { t: "services", cls: VAR },
  { t: ":\n  - ", cls: DIM },
  { t: "name", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "web\n    ", cls: STR },
  { t: "kind", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "web\n    ", cls: STR },
  { t: "port", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "3000\n", cls: TEAL },
  { t: "database", cls: VAR },
  { t: ":\n  ", cls: DIM },
  { t: "provider", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "docker\n  ", cls: STR },
  { t: "masking_rules", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "masking.yaml\n", cls: STR },
  { t: "egress", cls: VAR },
  { t: ":\n  ", cls: DIM },
  { t: "default", cls: VAR },
  { t: ": ", cls: DIM },
  { t: "block", cls: STR },
];

const FILE_TOKENS: Record<string, Token[]> = {
  "antifailure.yaml": YML,
  "index.tsx": [
    { t: "// The application. Antifailure needs no import in it.\n", cls: CM },
    { t: "export default async function", cls: KW },
    { t: " handler", cls: FN },
    { t: "(req, res) {\n  ", cls: VAR },
    { t: "const", cls: KW },
    { t: " subs = ", cls: VAR },
    { t: "await", cls: KW },
    { t: " db", cls: VAR },
    { t: ".query", cls: FN },
    { t: "(\n    ", cls: VAR },
    { t: '"select * from subscriptions where account_id = $1"', cls: STR },
    { t: ",\n    [req.accountId],\n  );\n\n  ", cls: VAR },
    { t: "res", cls: VAR },
    { t: ".status", cls: FN },
    { t: "(", cls: VAR },
    { t: "200", cls: "text-[#116329]" },
    { t: ").", cls: VAR },
    { t: "json", cls: FN },
    { t: "(subs);\n}", cls: VAR },
  ],
  "0002_access_tier.sql": [
    { t: "-- Rehearsed on a branch before it reaches production.\n", cls: CM },
    { t: "alter table", cls: KW },
    { t: " subscriptions\n  ", cls: VAR },
    { t: "add column", cls: KW },
    { t: " access_tier text;\n\n", cls: VAR },
    { t: "-- No default here on purpose: a default rewrites the table.\n", cls: CM },
  ],
  "masking.yaml": [
    { t: "# Compiled to SQL, then read back by the scanner.\n", cls: CM },
    { t: "rules", cls: VAR },
    { t: ":\n  - ", cls: DIM },
    { t: "table", cls: VAR },
    { t: ": ", cls: DIM },
    { t: "accounts\n    ", cls: STR },
    { t: "column", cls: VAR },
    { t: ": ", cls: DIM },
    { t: "email\n    ", cls: STR },
    { t: "transform", cls: VAR },
    { t: ": ", cls: DIM },
    { t: "email", cls: STR },
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
    { t: "af ci runs on the pull request.\nThe safety report attaches to the check.", cls: "text-black/70" },
  ],
};

const CMD = "curl -fsSL https://antifailure.dev/install.sh | sh";
const HOME_FILE = "index.tsx";

const CHECKS = [
  "Read the repository, wrote antifailure.yaml",
  "Branched a verified golden for this change",
  "Contained Stripe and email inside the twin",
  "Ran the declared workflows, attached the report",
];

const CHECK_DETAIL = [
  "Twelve analyzers read the repository and say what they assumed. Edit what they got wrong.",
  "A masked, referentially consistent branch. An unverified golden cannot be branched at all.",
  "Stripe answered from a stateful pack with the network unplugged. Mail rendered and captured.",
  "Agents drive the workflows through the accessibility tree and return a verdict with a trace.",
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
    <div className="flex min-h-[228px] font-mono text-[13px] leading-[22px] max-sm:min-h-[200px] max-sm:text-[11px] max-sm:leading-[19px]">
      {/* The gutter is dropped on a phone rather than shrunk. The code has to
          wrap at that width, and a wrapped line makes every number below it
          point at the wrong row — a broken gutter is worse than none. */}
      <div className="select-none py-3.5 pl-3 pr-3 text-right text-[#565656] max-sm:hidden">
        {Array.from({ length: lines }, (_, i) => (
          <div key={i}>{i + 1}</div>
        ))}
      </div>
      <pre className="min-w-0 flex-1 overflow-auto py-3.5 pr-4 outline-none max-sm:overflow-visible max-sm:px-3.5 max-sm:whitespace-pre-wrap max-sm:break-words">
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
      lines.push({ text: "$ af init" });
      lines.push({ text: "Step 1/3: reading the repository..." });
    }
    if (term >= 3) lines.push({ text: "Step 2/3: 12 analyzers, 3 services, 1 database..." });
    if (term >= 4) lines.push({ text: "Step 3/3: writing antifailure.yaml..." });
    if (term >= 5) {
      lines.push({ text: "Wrote antifailure.yaml. Run af up next.", cls: "text-[#16a34a]", success: true });
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
          <span className="text-red-400">af insights</span>
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
          <div>af init: reading the repository</div>
          <div>af up: branching the golden, starting the services</div>
          <div>af ci: running the workflows, writing the report</div>
          {term >= 5 ? <div className="text-[#16a34a]">Wrote antifailure.yaml.</div> : null}
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
        <div className="text-black/45">Local</div>
        <div>http://127.0.0.1:46000</div>
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
      className="relative min-w-0 overflow-hidden rounded-2xl border border-black/10 bg-white shadow-[0_40px_90px_rgba(0,0,0,0.12)]"
    >
      <div className="flex h-[42px] items-center border-b border-black/[0.07] px-3.5 text-[12px] text-black/40">
        <div className="flex gap-[6px]">
          <span className="h-3 w-3 rounded-full bg-[#ff5f57]" />
          <span className="h-3 w-3 rounded-full bg-[#febc2e]" />
          <span className="h-3 w-3 rounded-full bg-[#28c840]" />
        </div>
        <div className="min-w-0 flex-1 truncate px-3 text-center tracking-[-0.01em]">
          Your Code Editor
        </div>
      </div>
      <div className="grid min-w-0 grid-cols-1 sm:grid-cols-[minmax(0,148px)_minmax(0,1fr)] lg:grid-cols-[minmax(0,180px)_minmax(0,1fr)] xl:grid-cols-[200px_minmax(0,1fr)_minmax(0,260px)]">
        <div className="relative hidden min-w-0 overflow-hidden border-r border-black/[0.07] bg-[#f4f4f2] py-2.5 text-[12.5px] sm:block">
          {TREE.map((f) => {
            if (!isVisible(f, collapsed)) return null;
            const isActive = !f.folder && f.id === activeFile;
            const open = f.folder && !collapsed.has(f.id);
            return (
              <button
                key={f.id}
                type="button"
                title={f.path}
                className={`flex min-w-0 w-full items-center gap-1.5 py-[3px] pr-2 text-left hover:bg-black/[0.04] ${
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
          <div className="fade-scroll-x no-scrollbars flex overflow-x-auto border-b border-black/[0.07] px-1 text-[12.5px]">
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
            <div className="fade-scroll-x no-scrollbars flex gap-5 overflow-x-auto px-3 text-[11.5px] text-black/35 max-sm:gap-4">
              {BOTTOM_TABS.map((tab) => (
                <button
                  key={tab.id}
                  type="button"
                  className={`shrink-0 whitespace-nowrap border-b py-1.5 ${
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
            <pre className="min-h-[128px] overflow-x-auto px-4 pb-4 font-mono text-[12px] leading-[20px] text-black/72 max-sm:min-h-[96px] max-sm:px-3.5 max-sm:text-[11px] max-sm:leading-[18px]">{bottomBody}</pre>
          </div>
        </div>
        <div className="hidden min-w-0 bg-[#f4f4f2] p-2 xl:block">
          <div className="flex h-full flex-col rounded-[12px] border border-black/[0.08] bg-white shadow-[0_16px_48px_rgba(0,0,0,0.08)]">
            <div className="flex min-w-0 items-center justify-between gap-2 px-3.5 pt-3.5 text-[13px] font-medium text-black">
              <span className="min-w-0 leading-snug">Getting started with Antifailure</span>
              <span className="shrink-0 text-[15px] tracking-[0.2em] text-black/35">···</span>
            </div>
            <p className="mt-3 px-3.5 text-[12px] leading-[18px] text-black/55">
              Three commands. af init reads the repository and writes the manifest, af up builds the
              twin around a masked branch, af ci runs it and attaches the report.
            </p>
            <pre className="mx-3.5 mt-3 overflow-hidden rounded-lg bg-[#f4f4f2] p-2.5 font-mono text-[10.5px] leading-[17px]">
              <span className="mb-1 block text-[10px] text-black/35">terminal</span>
              <span className={DIM}>{"$ "}</span>
              <span className={VAR}>{"af init\n"}</span>
              <span className={DIM}>{"$ "}</span>
              <span className={VAR}>{"af up\n"}</span>
              <span className={DIM}>{"$ "}</span>
              <span className={VAR}>{"af ci\n"}</span>
              <span className={CM}>{"# pass or fail, with evidence"}</span>
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
                    <span className="min-w-0">{item}</span>
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
                <span className="block min-w-0 text-[12.5px] text-black/40">Plan, search, build anything...</span>
                <span className="mt-2.5 flex min-w-0 flex-wrap items-center gap-2">
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
