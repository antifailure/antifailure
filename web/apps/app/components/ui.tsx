// The component vocabulary, kept small on purpose.
//
// Nine things. Every page in this application is built from them, so a change
// to how a verdict reads or how a table breaks on a phone happens once. The
// alternative is what a control plane usually becomes: six tables that agree
// about nothing, generated a page at a time.

import type { ReactNode } from "react";

export function cn(...classes: Array<unknown>): string {
  return classes.filter((c): c is string => typeof c === "string" && c.length > 0).join(" ");
}

// ---------------------------------------------------------------------------
// Page frame
// ---------------------------------------------------------------------------

export function Page({ children }: { children: ReactNode }) {
  return <div className="mx-auto w-full max-w-[1180px] px-4 pb-24 pt-7 sm:px-6 lg:px-8">{children}</div>;
}

export function PageHead({
  title,
  lede,
  actions,
}: {
  title: string;
  /** One sentence saying what this page answers. Not a restatement of the
   *  title: a heading that says "Audit log" over a line that says "The audit
   *  log" has spent a line saying nothing. */
  lede: string;
  actions?: ReactNode;
}) {
  return (
    <header className="mb-6 flex flex-col gap-3 sm:mb-8 sm:flex-row sm:items-end sm:justify-between">
      <div className="max-w-[62ch]">
        <h1 className="text-[26px] font-semibold leading-[1.1] tracking-tighter text-ink sm:text-[30px]">
          {title}
        </h1>
        <p className="mt-1.5 text-[13.5px] leading-[1.5] text-muted">{lede}</p>
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </header>
  );
}

/** A bordered surface. One radius, one border, one background, everywhere. */
export function Panel({
  children,
  className,
  title,
  note,
}: {
  children: ReactNode;
  className?: string;
  title?: string;
  note?: ReactNode;
}) {
  return (
    <section className={cn("rounded-xl border border-hair bg-surface", className)}>
      {title ? (
        <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-hair px-4 py-3 sm:px-5">
          <h2 className="text-[13px] font-semibold tracking-snug text-ink">{title}</h2>
          {note ? <p className="text-[12.5px] text-faint">{note}</p> : null}
        </div>
      ) : null}
      {children}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Verdicts and states
// ---------------------------------------------------------------------------

type Tone = "pass" | "fail" | "flaky" | "neutral" | "quiet";

const TONE: Record<Tone, string> = {
  pass: "bg-pass-tint text-pass",
  fail: "bg-fail-tint text-fail",
  flaky: "bg-flaky-tint text-flaky",
  neutral: "bg-neutral-tint text-neutral",
  // Outlined rather than filled, so "we did not find out" is visibly not the
  // same kind of thing as "we found out and it was fine".
  quiet: "border border-dashed border-edge text-neutral",
};

export function Chip({
  children,
  tone = "neutral",
  title,
}: {
  children: ReactNode;
  tone?: Tone;
  title?: string;
}) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex items-center rounded-md px-1.5 py-0.5 text-[11.5px] font-medium leading-[1.4] tracking-snug whitespace-nowrap",
        TONE[tone],
      )}
    >
      {children}
    </span>
  );
}

/** How each verdict reads, and what it means, in one place.
 *
 * The two greys are the point of this table. A blocked run and a failing run
 * must never look alike: one is a fact about the change and the other is a
 * fact about us, and a report that renders them the same is a report people
 * learn to skim. */
export const VERDICTS: Record<string, { tone: Tone; means: string }> = {
  pass: { tone: "pass", means: "The agent did the workflow and every expectation held." },
  fail: { tone: "fail", means: "The agent did the workflow and an expectation did not hold." },
  flaky: { tone: "flaky", means: "It passed on one attempt and failed on another." },
  blocked: {
    tone: "neutral",
    means: "The environment was incomplete, so this is not evidence about the change.",
  },
  unverified: {
    tone: "quiet",
    means: "The page neither confirmed nor contradicted the expectation, so nothing was concluded.",
  },
};

export function VerdictChip({ value }: { value: string }) {
  const known = VERDICTS[value];
  return (
    <Chip tone={known?.tone ?? "neutral"} title={known?.means}>
      {value}
    </Chip>
  );
}

export const ENVIRONMENT_STATES: Record<string, { tone: Tone; means: string }> = {
  running: { tone: "pass", means: "Serving, and reachable at its preview address." },
  creating: { tone: "flaky", means: "Being built and branched. Nothing serves yet." },
  queued: { tone: "neutral", means: "Waiting for capacity." },
  sleeping: { tone: "neutral", means: "Idle and stopped. It wakes on the next request." },
  failed: { tone: "fail", means: "It did not come up. The run says how far it got." },
  torn_down: { tone: "quiet", means: "Gone. Every resource it created was removed." },
};

export function StateChip({ value }: { value: string }) {
  const known = ENVIRONMENT_STATES[value];
  return (
    <Chip tone={known?.tone ?? "neutral"} title={known?.means}>
      {value.replace("_", " ")}
    </Chip>
  );
}

/** The six egress modes, which are the vocabulary of the network page. */
export const MODES: Record<string, { tone: Tone; means: string }> = {
  block: { tone: "fail", means: "Refused, with a decision that says which rule refused it." },
  allow: { tone: "pass", means: "It leaves, subject to the rate limit on the rule." },
  capture: { tone: "flaky", means: "Recorded into the inbox instead of being sent." },
  mock: { tone: "flaky", means: "Answered from an offline pack. Nothing leaves." },
  sandbox: { tone: "flaky", means: "It leaves, with a test credential swapped in on the way out." },
  synth: { tone: "flaky", means: "Answered with a generated response shaped like the real one." },
};

export function ModeChip({ value }: { value: string }) {
  const known = MODES[value];
  return (
    <Chip tone={known?.tone ?? "neutral"} title={known?.means}>
      {value}
    </Chip>
  );
}

// ---------------------------------------------------------------------------
// Type
// ---------------------------------------------------------------------------

/** An identifier, a hash, a host. Anything somebody might copy. */
export function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span
      className={cn(
        // Never wrapped. An identifier broken across two lines cannot be
        // compared down a column or copied out of one, which are the only two
        // things anybody does with one.
        "font-mono text-[12.5px] tracking-snug whitespace-nowrap numeric",
        className,
      )}
    >
      {children}
    </span>
  );
}

/**
 * A moment, written out and hover-readable.
 *
 * Absolute rather than "3 hours ago", with the exact instant in the title. A
 * relative time renders on the server, is wrong by the time it is read, and
 * cannot be compared against a log line, which is the thing somebody is
 * usually holding when they open this page.
 */
export function When({ at, className }: { at: string | null | undefined; className?: string }) {
  if (!at) return <span className={cn("text-faint", className)}>&mdash;</span>;
  const date = new Date(at);
  if (Number.isNaN(date.getTime())) return <span className={cn("text-faint", className)}>&mdash;</span>;
  const iso = date.toISOString();
  return (
    <time dateTime={iso} title={iso} className={cn("numeric whitespace-nowrap", className)}>
      {iso.slice(0, 10)} <span className="text-faint">{iso.slice(11, 19)}</span>
    </time>
  );
}

/** A duration in milliseconds, at a readable magnitude. */
export function Duration({ ms }: { ms: number | null | undefined }) {
  if (ms === null || ms === undefined) return <span className="text-faint">&mdash;</span>;
  if (ms < 1000) return <span className="numeric">{Math.round(ms)}ms</span>;
  if (ms < 60_000) return <span className="numeric">{(ms / 1000).toFixed(1)}s</span>;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return (
    <span className="numeric">
      {minutes}m {String(seconds).padStart(2, "0")}s
    </span>
  );
}

// ---------------------------------------------------------------------------
// Controls
// ---------------------------------------------------------------------------

export function Button({
  children,
  variant = "secondary",
  type = "button",
  ...rest
}: {
  children: ReactNode;
  variant?: "primary" | "secondary" | "danger";
} & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const base =
    "inline-flex h-8 items-center justify-center gap-1.5 rounded-lg px-3 text-[13px] font-medium " +
    "tracking-snug transition-colors active:translate-y-px disabled:cursor-not-allowed disabled:opacity-55";
  const look = {
    primary: "bg-ink text-white hover:bg-[#1c1c1c]",
    secondary: "border border-edge bg-surface text-ink hover:bg-sunken",
    danger: "border border-fail/35 bg-fail-tint text-fail hover:bg-[#f7dede]",
  }[variant];
  return (
    <button type={type} className={cn(base, look)} {...rest}>
      {children}
    </button>
  );
}

export function LinkButton({
  children,
  href,
  variant = "secondary",
  ...rest
}: {
  children: ReactNode;
  href: string;
  variant?: "primary" | "secondary";
} & React.AnchorHTMLAttributes<HTMLAnchorElement>) {
  const base =
    "inline-flex h-8 items-center justify-center gap-1.5 rounded-lg px-3 text-[13px] font-medium tracking-snug transition-colors";
  const look = {
    primary: "bg-ink text-white hover:bg-[#1c1c1c]",
    secondary: "border border-edge bg-surface text-ink hover:bg-sunken",
  }[variant];
  return (
    <a href={href} className={cn(base, look)} {...rest}>
      {children}
    </a>
  );
}

export function Field({
  label,
  hint,
  children,
  htmlFor,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
  htmlFor: string;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={htmlFor} className="text-[12.5px] font-medium tracking-snug text-ink">
        {label}
      </label>
      {children}
      {hint ? <p className="text-[12px] leading-[1.45] text-faint">{hint}</p> : null}
    </div>
  );
}

/** Every text input in the application. Four states, all designed: resting,
 *  hover, focus, and disabled. */
export const inputClass =
  "h-9 w-full rounded-lg border border-edge bg-surface px-2.5 text-[13.5px] text-ink " +
  "placeholder:text-faint hover:border-[rgba(0,0,0,0.24)] focus:border-ink focus-visible:outline-none " +
  "focus:ring-2 focus:ring-ink/12 disabled:bg-sunken disabled:text-faint";

export const selectClass = cn(inputClass, "pr-8 appearance-none bg-no-repeat cursor-pointer");

// ---------------------------------------------------------------------------
// The states everything else forgets
// ---------------------------------------------------------------------------

/** Nothing here, and what to do about it. */
export function Empty({
  title,
  says,
  action,
}: {
  title: string;
  says: string;
  action?: ReactNode;
}) {
  return (
    <div className="mesh-grid flex flex-col items-center justify-center gap-2 px-6 py-14 text-center">
      <p className="text-[14px] font-medium tracking-snug text-ink">{title}</p>
      <p className="max-w-[46ch] text-[13px] leading-[1.55] text-muted">{says}</p>
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  );
}

/** It went wrong, in a sentence, with the way back. */
export function Failure({
  title,
  detail,
  action,
}: {
  title: string;
  detail: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-start gap-2 rounded-xl border border-fail/25 bg-fail-tint px-4 py-4 sm:px-5">
      <p className="text-[13.5px] font-semibold tracking-snug text-fail">{title}</p>
      <p className="max-w-[70ch] text-[13px] leading-[1.55] text-[#7a1414]">{detail}</p>
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}

/** A block the size of what is coming. */
export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden className={cn("skeleton h-4 w-full", className)} />;
}

/** Rows of them, for a table that has not arrived. */
export function TableSkeleton({ rows = 6, columns = 5 }: { rows?: number; columns?: number }) {
  return (
    <div className="divide-y divide-hair" aria-hidden>
      {Array.from({ length: rows }, (_, r) => (
        <div key={r} className="flex items-center gap-4 px-4 py-3.5 sm:px-5">
          {Array.from({ length: columns }, (_, c) => (
            <Skeleton key={c} className={c === 0 ? "w-[22%]" : "w-[14%]"} />
          ))}
        </div>
      ))}
      <span className="sr-only">Loading</span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Tables
// ---------------------------------------------------------------------------

/**
 * A table that scrolls inside itself rather than pushing the page sideways.
 *
 * The wrapper is the whole trick and it is not optional: a matrix of eleven
 * columns on a 320px screen either scrolls in its own box or makes the entire
 * document scroll horizontally, and the second one moves the navigation off
 * the screen along with it.
 */
export function TableFrame({ children }: { children: ReactNode }) {
  return (
    <div className="w-full overflow-x-auto">
      <table className="w-full min-w-[720px] border-collapse text-left">{children}</table>
    </div>
  );
}

export function Th({
  children,
  className,
  numeric,
}: {
  children: ReactNode;
  className?: string;
  numeric?: boolean;
}) {
  return (
    <th
      scope="col"
      className={cn(
        "border-b border-hair px-4 py-2.5 text-[11.5px] font-medium uppercase tracking-[0.06em] text-faint sm:px-5",
        numeric && "text-right",
        className,
      )}
    >
      {children}
    </th>
  );
}

export function Td({
  children,
  className,
  numeric,
}: {
  children: ReactNode;
  className?: string;
  numeric?: boolean;
}) {
  return (
    <td
      className={cn(
        "px-4 py-3 align-middle text-[13px] text-ink sm:px-5",
        // Right-aligned and tabular, because a column of numbers that is
        // left-aligned and proportional cannot be compared down its length.
        numeric && "text-right numeric",
        className,
      )}
    >
      {children}
    </td>
  );
}

export function Tr({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <tr className={cn("border-b border-hair last:border-b-0 hover:bg-sunken/60", className)}>
      {children}
    </tr>
  );
}
