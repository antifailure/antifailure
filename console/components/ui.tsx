"use client";

import type { ReactNode } from "react";
import { ApiError } from "@/lib/api";

/* -------------------------------------------------------------------------
 * Surfaces
 * ---------------------------------------------------------------------- */

export function Page({
  title,
  lede,
  actions,
  children,
}: {
  title: string;
  lede?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="mx-auto w-full max-w-[1120px] px-5 py-8 sm:px-8 lg:px-10">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-[26px] font-semibold leading-dense tracking-tighter text-ink">
            {title}
          </h1>
          {lede ? (
            <p className="mt-2 max-w-[62ch] text-[13.5px] leading-6 text-muted">{lede}</p>
          ) : null}
        </div>
        {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
      </div>
      <div className="mt-7">{children}</div>
    </div>
  );
}

export function Card({
  title,
  note,
  actions,
  children,
  className = "",
}: {
  title?: string;
  note?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section
      className={`overflow-hidden rounded-[6px] border border-rule bg-card ${className}`}
    >
      {title ? (
        <header className="flex flex-wrap items-center justify-between gap-3 border-b border-rule px-4 py-3">
          <div className="min-w-0">
            <h2 className="text-[13px] font-semibold tracking-extra-tight text-ink">{title}</h2>
            {note ? <p className="mt-0.5 text-[12px] leading-5 text-dim">{note}</p> : null}
          </div>
          {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
        </header>
      ) : null}
      {children}
    </section>
  );
}

/* -------------------------------------------------------------------------
 * Tables
 *
 * A wrapper that scrolls on its own rather than letting a wide table push the
 * page sideways, which is the single most common way a console breaks on a
 * phone.
 * ---------------------------------------------------------------------- */

export function TableWrap({ children }: { children: ReactNode }) {
  return <div className="scroll-x w-full">{children}</div>;
}

export function Table({ children }: { children: ReactNode }) {
  return <table className="w-full min-w-[520px] border-collapse text-left text-[13px]">{children}</table>;
}

export function Th({
  children,
  numeric = false,
}: {
  children: ReactNode;
  numeric?: boolean;
}) {
  return (
    <th
      scope="col"
      className={`whitespace-nowrap border-b border-rule px-4 py-2.5 text-[11px] font-medium uppercase tracking-[0.08em] text-dim ${
        numeric ? "text-right" : ""
      }`}
    >
      {children}
    </th>
  );
}

export function Td({
  children,
  numeric = false,
  mono = false,
  className = "",
}: {
  children: ReactNode;
  numeric?: boolean;
  mono?: boolean;
  className?: string;
}) {
  return (
    <td
      className={`border-b border-rule px-4 py-2.5 align-middle text-ink ${
        numeric ? "tnum text-right" : ""
      } ${mono ? "font-mono text-[12px]" : ""} ${className}`}
    >
      {children}
    </td>
  );
}

export function Row({ children, onClick }: { children: ReactNode; onClick?: () => void }) {
  if (!onClick) return <tr>{children}</tr>;
  return (
    <tr
      onClick={onClick}
      className="cursor-pointer transition-colors hover:bg-[rgba(16,16,16,0.035)]"
    >
      {children}
    </tr>
  );
}

/* -------------------------------------------------------------------------
 * Signals
 * ---------------------------------------------------------------------- */

export type Tone = "pass" | "fail" | "warn" | "neutral";

/**
 * A verdict, a status, a decision. Colour is never the only signal: the word
 * is the signal and the colour agrees with it, so this reads the same to
 * somebody who cannot tell the red from the green.
 */
export function Badge({ tone = "neutral", children }: { tone?: Tone; children: ReactNode }) {
  const styles: Record<Tone, string> = {
    pass: "text-pass bg-[rgba(30,122,58,0.1)]",
    fail: "text-fail bg-[rgba(179,38,30,0.1)]",
    warn: "text-warn bg-[rgba(138,90,0,0.12)]",
    neutral: "text-muted bg-[rgba(16,16,16,0.06)]",
  };
  return (
    <span
      className={`inline-flex items-center rounded-[4px] px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-[0.05em] ${styles[tone]}`}
    >
      {children}
    </span>
  );
}

export function toneFor(value: string | null | undefined): Tone {
  const v = (value ?? "").toLowerCase();
  if (["pass", "ok", "passed", "ready", "active", "allow", "verified", "approved"].includes(v)) {
    return "pass";
  }
  if (["fail", "failed", "block", "blocked", "denied", "deny", "error", "revoked"].includes(v)) {
    return "fail";
  }
  if (["pending", "running", "waiting", "proposed", "expiring", "warn"].includes(v)) return "warn";
  return "neutral";
}

/* -------------------------------------------------------------------------
 * Controls
 * ---------------------------------------------------------------------- */

export function Button({
  children,
  onClick,
  type = "button",
  variant = "secondary",
  disabled = false,
  busy = false,
}: {
  children: ReactNode;
  onClick?: () => void;
  type?: "button" | "submit";
  variant?: "primary" | "secondary" | "danger";
  disabled?: boolean;
  busy?: boolean;
}) {
  const base =
    "inline-flex h-9 items-center justify-center gap-2 rounded-[5px] px-3.5 text-[13px] font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-55";
  const variants = {
    primary: "bg-ink text-white hover:bg-[#2b2b2b]",
    secondary: "border border-rule bg-card text-ink hover:border-rule-strong",
    danger: "border border-[rgba(179,38,30,0.35)] bg-card text-fail hover:bg-[rgba(179,38,30,0.06)]",
  };
  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      className={`${base} ${variants[variant]}`}
    >
      {children}
    </button>
  );
}

export function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string;
  hint?: ReactNode;
  error?: string | null;
  children: ReactNode;
}) {
  return (
    <label className="block">
      <span className="block text-[12px] font-medium text-muted">{label}</span>
      {children}
      {error ? (
        <span role="alert" className="mt-1.5 block text-[12px] text-fail">
          {error}
        </span>
      ) : hint ? (
        <span className="mt-1.5 block text-[12px] leading-5 text-dim">{hint}</span>
      ) : null}
    </label>
  );
}

export const inputClass =
  "mt-1.5 h-9 w-full rounded-[5px] border border-rule bg-card px-2.5 text-[13px] text-ink outline-none placeholder:text-dim focus:border-rule-strong";

/* -------------------------------------------------------------------------
 * The three states a screen is usually missing
 * ---------------------------------------------------------------------- */

/** A skeleton shaped like the table it stands in for, so the layout does not
 *  jump when the data lands. */
export function TableSkeleton({ rows = 5, cols = 4 }: { rows?: number; cols?: number }) {
  return (
    <TableWrap>
      <Table>
        <tbody>
          {Array.from({ length: rows }).map((_, r) => (
            <tr key={r}>
              {Array.from({ length: cols }).map((_, c) => (
                <Td key={c}>
                  <span
                    className="block h-3 rounded-[3px] bg-[rgba(16,16,16,0.07)]"
                    style={{ width: c === 0 ? "58%" : c === cols - 1 ? "34%" : "46%" }}
                  />
                </Td>
              ))}
            </tr>
          ))}
        </tbody>
      </Table>
      <span className="sr-only" role="status">
        Loading
      </span>
    </TableWrap>
  );
}

export function Empty({
  title,
  children,
  action,
}: {
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="px-6 py-12 text-center">
      <p className="text-[14px] font-medium text-ink">{title}</p>
      {children ? (
        <p className="mx-auto mt-2 max-w-[46ch] text-[13px] leading-6 text-muted">{children}</p>
      ) : null}
      {action ? <div className="mt-5 flex justify-center">{action}</div> : null}
    </div>
  );
}

/**
 * What went wrong, in words, and the one thing to do about it. Never a raw
 * status code on its own and never a silent blank.
 */
export function ErrorState({ error, retry }: { error: ApiError; retry?: () => void }) {
  const forbidden = error.status === 403 || error.code === "FORBIDDEN";
  return (
    <div className="px-6 py-12 text-center" role="alert">
      <p className="text-[14px] font-medium text-ink">
        {forbidden ? "Your role cannot see this" : "That did not load"}
      </p>
      <p className="mx-auto mt-2 max-w-[52ch] text-[13px] leading-6 text-muted">{error.message}</p>
      {retry && !forbidden ? (
        <div className="mt-5 flex justify-center">
          <Button onClick={retry}>Try again</Button>
        </div>
      ) : null}
    </div>
  );
}

/**
 * The three branches in one place, so no screen renders a blank on an error
 * it forgot to handle.
 */
export function Loaded<T>({
  state,
  skeleton,
  children,
}: {
  state: {
    status: "loading" | "ready" | "error";
    data: T | null;
    error: ApiError | null;
    reload: () => void;
  };
  skeleton?: ReactNode;
  children: (data: T) => ReactNode;
}) {
  if (state.status === "error" && state.error) {
    return <ErrorState error={state.error} retry={state.reload} />;
  }
  // "ready with no data" is not a state a page should have to handle, and it
  // is reachable: a hook whose deps have changed still reports the previous
  // result for one render. Handing null to children here crashed the network
  // page to a white screen the first time anybody used it, so this treats it
  // as still loading rather than passing it on.
  if (state.status === "loading" || state.data === null || state.data === undefined) {
    return <>{skeleton ?? <TableSkeleton />}</>;
  }
  return <>{children(state.data)}</>;
}
