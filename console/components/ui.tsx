"use client";

import {
  cloneElement,
  isValidElement,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";
import Link from "next/link";
import { ApiError } from "@/lib/api";
import { ago, when } from "@/lib/format";
import { LogoMark } from "@/components/icons";

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
        {actions ? (
            // max-w-full, or an actions block wider than the card, such as a
            // long error message beside a button, is clipped by the
            // overflow-hidden on the section and simply disappears at a phone
            // width. shrink-0 still keeps a short one from being squeezed by
            // the title.
            <div className="flex min-w-0 max-w-full shrink-0 items-center gap-2">{actions}</div>
          ) : null}
      </div>
      <div className="mt-7">{children}</div>
    </div>
  );
}

/**
 * Every screen that is the whole window rather than a page inside the chrome:
 * sign in, no organization, the control plane did not answer, not found, and
 * the three states of device approval.
 *
 * There were six of these and they had four heading sizes (26, 28, 30) and
 * four different primary buttons (h-9/h-10/h-11, two radii, three type sizes)
 * between them, because each was written on its own. One component means the
 * sign-in screen and the 404 are demonstrably the same product.
 */
export function Standalone({
  title,
  width = 420,
  alert = false,
  children,
}: {
  title: string;
  width?: number;
  alert?: boolean;
  children: ReactNode;
}) {
  return (
    <main className="grid min-h-dvh place-items-center px-5 py-10">
      <div
        className="w-full"
        style={{ maxWidth: `${width}px` }}
        role={alert ? "alert" : undefined}
      >
        <LogoMark className="h-9 w-9" />
        <h1 className="mt-7 text-[28px] font-semibold leading-dense tracking-tighter text-ink">
          {title}
        </h1>
        {children}
      </div>
    </main>
  );
}

/** Body copy on a standalone screen. One size, one measure, everywhere. */
export function Lede({ children }: { children: ReactNode }) {
  return <p className="mt-3 max-w-[52ch] text-[13.5px] leading-6 text-muted">{children}</p>;
}

/**
 * A link that is the primary action. The same box as `Button` variant
 * "primary", because it does the same job and a person cannot see which of
 * them is an anchor.
 */
export function LinkButton({
  href,
  children,
  full = false,
  variant = "primary",
}: {
  href: string;
  children: ReactNode;
  full?: boolean;
  variant?: "primary" | "secondary";
}) {
  const tone =
    variant === "primary"
      ? "bg-ink text-white hover:bg-[#2b2b2b]"
      : "border border-rule bg-card text-ink hover:border-rule-strong";
  return (
    <a
      href={href}
      className={`inline-flex h-11 items-center justify-center gap-2.5 rounded-md px-4 text-[14px] font-medium transition-colors ${tone} ${
        full ? "w-full" : ""
      }`}
    >
      {children}
    </a>
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
      className={`overflow-hidden rounded-lg border border-rule bg-card ${className}`}
    >
      {title ? (
        <header className="flex flex-wrap items-center justify-between gap-3 border-b border-rule px-4 py-3">
          <div className="min-w-0">
            <h2 className="text-[13px] font-semibold tracking-extra-tight text-ink">{title}</h2>
            {note ? <p className="mt-0.5 text-[12px] leading-5 text-dim">{note}</p> : null}
          </div>
          {actions ? (
            // max-w-full, or an actions block wider than the card, such as a
            // long error message beside a button, is clipped by the
            // overflow-hidden on the section and simply disappears at a phone
            // width. shrink-0 still keeps a short one from being squeezed by
            // the title.
            <div className="flex min-w-0 max-w-full shrink-0 items-center gap-2">{actions}</div>
          ) : null}
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
  return (
    <table className="af-table w-full border-collapse text-left text-[13px] sm:min-w-[520px]">
      {children}
    </table>
  );
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
  label,
  numeric = false,
  mono = false,
  className = "",
}: {
  children: ReactNode;
  /** The column this cell is in, repeated beside it once the table stacks on a
   *  phone. Give it the same string as the `Th`. Omit it on the cell that
   *  names the row: that one leads the stacked record on its own. */
  label?: string;
  numeric?: boolean;
  mono?: boolean;
  className?: string;
}) {
  return (
    <td
      data-label={label}
      className={`border-b border-rule px-4 py-2.5 align-middle text-ink ${
        numeric ? "tnum text-right" : ""
      } ${mono ? "font-mono text-[12px]" : ""} ${className}`}
    >
      {/* One wrapper, so that when the table stacks on a phone the cell is a
          two-item grid, heading then value, however many nodes the value
          is made of. Without it a cell like "branch #1482" put the branch in
          one grid slot and the number in the next row's heading slot. */}
      <span className="af-cell">{children}</span>
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

/**
 * The primary cell of a row that opens something.
 *
 * A `<tr onClick>` is invisible to a keyboard: it is not focusable, Enter does
 * nothing, and a screen reader is never told the row goes anywhere. Both list
 * screens shipped that way. Putting a real link in the first cell gives the
 * row one focusable, announced, Enter-activated target, and the row-wide click
 * stays as the mouse convenience it always was.
 */
export function CellLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <Link
      href={href}
      className="-mx-1 -my-2 inline-flex min-h-11 items-center px-1 py-2 underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
    >
      {children}
    </Link>
  );
}

/* -------------------------------------------------------------------------
 * Signals
 * ---------------------------------------------------------------------- */

/**
 * A moment in time, said twice.
 *
 * Four screens wrote `<span title={when(x)}>{ago(x)}</span>` by hand, which
 * puts the only precise answer in a tooltip, a thing a phone cannot show and
 * a screen reader announces inconsistently. This is a real `<time>`: the
 * machine-readable instant is the attribute, the exact local time is in the
 * accessible name, and "3h ago" is what the eye gets.
 */
export function When({ value }: { value: string | Date | null | undefined }) {
  const exact = when(value);
  if (exact === "--") return <span className="text-dim">--</span>;
  const iso = (value instanceof Date ? value : new Date(value as string)).toISOString();
  return (
    <time dateTime={iso} title={exact} aria-label={exact} className="whitespace-nowrap">
      {ago(value) || exact}
    </time>
  );
}

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
      className={`inline-flex items-center rounded-sm px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-[0.05em] ${styles[tone]}`}
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
  if (
    [
      "pending",
      "running",
      "waiting",
      "proposed",
      "expiring",
      "warn",
      // In progress. Left out, these fell through to "neutral" and an
      // environment that was still being built looked exactly like one that
      // had been torn down.
      "provisioning",
      "creating",
      "starting",
      "queued",
      "building",
    ].includes(v)
  ) {
    return "warn";
  }
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
  full = false,
}: {
  children: ReactNode;
  onClick?: () => void;
  type?: "button" | "submit";
  variant?: "primary" | "secondary" | "danger";
  disabled?: boolean;
  busy?: boolean;
  /** Fills its container. `LinkButton` has had this since it was written and
   *  `Button` did not, which is why a sign-in form's submit rendered as a
   *  narrow box in the middle of a full-width field. The two components are
   *  the same box and a person cannot see which is an anchor, so they take
   *  the same props. */
  full?: boolean;
}) {
  // whitespace-nowrap, because a two word label breaking across two lines is
  // the shape a control has when it is in trouble, and it happened as soon as
  // a table put a button in a narrow column at a tablet width. A button that is
  // too wide for its column pushes the table into its own horizontal scroll,
  // which TableWrap already provides and which is the better failure.
  const base =
    "inline-flex h-11 items-center justify-center gap-2 whitespace-nowrap rounded-md px-3.5 text-[13px] font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-55 sm:h-9";
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
      className={`${base} ${variants[variant]} ${full ? "w-full" : ""}`}
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
  // The hint and the error sit outside the label, and that is load bearing
  // rather than tidy. A wrapping label computes ONE accessible name out of
  // everything inside it, so the sign-in field was named "Email address We
  // send a link that signs you in. No password." to every screen reader, and
  // to the agents that drive this console the same way. Six Dogfood workflows
  // came back blocked on it. A hint describes a field; it is not its name.
  const id = useId();
  const describedBy = error ? `${id}-error` : hint ? `${id}-hint` : undefined;
  const control =
    describedBy && isValidElement<{ "aria-describedby"?: string }>(children)
      ? cloneElement(children, { "aria-describedby": describedBy })
      : children;

  return (
    <div className="block">
      <label className="block">
        <span className="block text-[12px] font-medium text-muted">{label}</span>
        {control}
      </label>
      {error ? (
        <span id={`${id}-error`} role="alert" className="mt-1.5 block text-[12px] text-fail">
          {error}
        </span>
      ) : hint ? (
        <span id={`${id}-hint`} className="mt-1.5 block text-[12px] leading-5 text-dim">
          {hint}
        </span>
      ) : null}
    </div>
  );
}

/**
 * A select, styled as the input it sits beside.
 *
 * There were three of these: one on the repository picker, one on the members
 * table and one built out of `inputClass` on the network form, at two heights
 * and two paddings. The native arrow is kept: a custom one is a chevron to
 * maintain and a control that stops looking like the platform's.
 */
export const selectClass =
  "h-11 rounded-md border border-rule bg-card px-2.5 text-[13px] text-ink outline-none focus:border-rule-strong disabled:cursor-not-allowed disabled:opacity-60 sm:h-9";

export const inputClass =
  "mt-1.5 h-11 w-full rounded-md border border-rule bg-card px-2.5 text-[13px] text-ink outline-none placeholder:text-dim focus:border-rule-strong sm:h-9";

/**
 * Puts a string on the clipboard and says so.
 *
 * Three screens had written this by hand: the same two second timer, the same
 * cleanup on unmount that two of them remembered, and the same swallowed
 * rejection. The swallow is the part worth keeping and worth explaining, so it
 * lives in one place: `navigator.clipboard` needs a secure origin, and a
 * control plane reached over plain http on a LAN is a real way this console is
 * used. Failing quietly there is correct, because the text beside the button
 * is selectable either way and a red error about a convenience is worse than
 * the convenience not happening.
 */
export function CopyButton({
  value,
  label = "Copy",
  said,
}: {
  value: string;
  /** What the button says at rest. It always says "Copied" afterwards. */
  label?: string;
  /** The whole sentence announced to a screen reader, since somebody who
   *  cannot see the button change is otherwise told nothing at all. */
  said?: string;
}) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );
  return (
    <>
      <Button
        onClick={() => {
          void navigator.clipboard
            ?.writeText(value)
            .then(() => {
              setCopied(true);
              if (timer.current) clearTimeout(timer.current);
              timer.current = setTimeout(() => setCopied(false), 2000);
            })
            .catch(() => undefined);
        }}
      >
        {copied ? "Copied" : label}
      </Button>
      {/* aria-live rather than a tooltip, so the confirmation reaches somebody
          who is not looking at the button they just pressed. */}
      <span aria-live="polite" className="sr-only">
        {copied ? (said ?? "Copied to the clipboard") : ""}
      </span>
    </>
  );
}

/**
 * A command to run somewhere else, with the button that copies it.
 *
 * overflow-x-auto by hand, NOT the shared `.scroll-x` helper. That class turns
 * itself off below 640px, because the tables it was written for stop scrolling
 * and stack instead at that width. A command does not stack, so borrowing the
 * class clipped a long one dead at the card edge on a phone: no scrollbar, no
 * ellipsis, just a sentence that stopped.
 */
export function CommandBlock({
  command,
  said,
  label,
}: {
  command: string;
  said?: string;
  label?: string;
}) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-3">
      <pre className="min-w-0 flex-1 overflow-x-auto rounded-md border border-rule bg-[rgba(16,16,16,0.03)] px-3 py-2.5 font-mono text-[12.5px] leading-6 text-ink">
        <code>{command}</code>
      </pre>
      <CopyButton
        value={command}
        label={label}
        said={said ?? "Command copied to the clipboard"}
      />
    </div>
  );
}

/* -------------------------------------------------------------------------
 * The three states a screen is usually missing
 * ---------------------------------------------------------------------- */

/**
 * A single placeholder bar, the shape of the thing that is coming.
 *
 * Static, deliberately. Two screens reached for `animate-pulse` and a
 * skeleton that throbs is the loudest thing on a page that is, by definition,
 * showing nothing yet. The shape and the position are the signal; the
 * throbbing was decoration on top of a wait.
 */
export function Bar({ className = "h-3 w-full" }: { className?: string }) {
  return <span className={`block rounded-sm bg-[rgba(16,16,16,0.07)] ${className}`} />;
}

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
                  <Bar
                    className={`h-3 ${c === 0 ? "w-[58%]" : c === cols - 1 ? "w-[34%]" : "w-[46%]"}`}
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

/** A card-shaped wait, for screens whose content is cards rather than rows. */
export function CardSkeleton({ count = 2 }: { count?: number }) {
  return (
    <div className="space-y-6" role="status">
      {Array.from({ length: count }).map((_, i) => (
        <div key={i} className="rounded-lg border border-rule bg-card">
          <div className="border-b border-rule px-4 py-3">
            <Bar className="h-3.5 w-32" />
            <Bar className="mt-2 h-2.5 w-56" />
          </div>
          <div className="space-y-3 px-4 py-4">
            <Bar className="h-2.5 w-24" />
            <Bar className="h-1.5 w-full max-w-[420px]" />
            <Bar className="h-9 w-full max-w-[320px]" />
          </div>
        </div>
      ))}
      <span className="sr-only">Loading</span>
    </div>
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

/* -------------------------------------------------------------------------
 * Confirming something that does not come back
 * ---------------------------------------------------------------------- */

/**
 * A confirmation that names what will be destroyed and makes you type it.
 *
 * The native `dialog` element, deliberately, rather than a div with a
 * backdrop. `showModal` gives focus containment, Escape, the inert background
 * and `aria-modal` for nothing, and every hand-rolled version of those in
 * every console gets at least one of them wrong. What is added on top is the
 * part a dialog cannot know: the exact word that has to be typed.
 *
 * `phrase` is the name of the thing, not the word "delete". Typing "delete"
 * proves you can read a label; typing the organization's own slug proves you
 * know which one you are looking at, which is the mistake that actually
 * happens.
 */
export function Confirm({
  open,
  title,
  phrase,
  confirmLabel,
  tone = "danger",
  cancelLabel = "Keep it",
  busy = false,
  error,
  onConfirm,
  onCancel,
  children,
}: {
  open: boolean;
  title: string;
  /** The exact string that has to be typed. Omit for a confirmation that is
   *  serious but reversible, which gets a button and no field. */
  phrase?: string;
  confirmLabel: string;
  /**
   * The weight of the confirm button.
   *
   * Danger by default, because everything this component was written for
   * destroys or withdraws something. `primary` exists for the one shape that is
   * neither: an action that GIVES access back, where a red button says the
   * opposite of what the action does. Restoring a suspended operator is the
   * case that found it, rendered in red beside a cancel button reading "Keep
   * it", which together read as a deletion dialog.
   */
  tone?: "danger" | "primary";
  /** What declining says. "Keep it" is right for destroying something and
   *  wrong for undoing a suspension, where the thing being kept is the
   *  suspension rather than the account. */
  cancelLabel?: string;
  busy?: boolean;
  error?: string | null;
  onConfirm: () => void;
  onCancel: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [typed, setTyped] = useState("");
  const id = useId();

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (open && !el.open) {
      setTyped("");
      el.showModal();
    }
    if (!open && el.open) el.close();
  }, [open]);

  const ready = phrase === undefined || typed.trim() === phrase;

  return (
    <dialog
      ref={ref}
      aria-labelledby={`${id}-title`}
      // Escape fires cancel rather than closing behind the component's back,
      // so the parent's state and the element's state cannot disagree about
      // whether the dialog is open.
      onCancel={(e) => {
        e.preventDefault();
        if (!busy) onCancel();
      }}
      // m-auto is not decoration. A modal dialog is centred by the user agent
      // with `margin: auto` on a box that fills the viewport, and Tailwind's
      // preflight sets `margin: 0` on everything, so without this the dialog
      // opens hard against the top left corner. It looked like a rendering bug
      // and it is a reset doing exactly what it says.
      className="m-auto max-h-[min(90dvh,640px)] w-[min(100vw-2rem,460px)] overflow-y-auto rounded-lg border border-rule bg-card p-0 text-ink backdrop:bg-[rgba(16,16,16,0.5)]"
    >
      <form
        method="dialog"
        onSubmit={(e) => {
          e.preventDefault();
          if (ready && !busy) onConfirm();
        }}
      >
        <div className="border-b border-rule px-5 py-3.5">
          <h2 id={`${id}-title`} className="text-[14px] font-semibold tracking-extra-tight">
            {title}
          </h2>
        </div>
        <div className="space-y-4 px-5 py-4 text-[13px] leading-6 text-muted">
          {children}
          {phrase !== undefined ? (
            <Field label={`Type ${phrase} to confirm`}>
              <input
                autoFocus
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                className={inputClass}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
          ) : null}
          {error ? (
            <p role="alert" className="text-[12px] leading-5 text-fail">
              {error}
            </p>
          ) : null}
        </div>
        <div className="flex flex-wrap justify-end gap-2 border-t border-rule px-5 py-3.5">
          <Button onClick={onCancel} disabled={busy}>
            {cancelLabel}
          </Button>
          <Button type="submit" variant={tone} disabled={!ready} busy={busy}>
            {confirmLabel}
          </Button>
        </div>
      </form>
    </dialog>
  );
}

/* -------------------------------------------------------------------------
 * The three states a screen is usually missing (continued)
 * ---------------------------------------------------------------------- */

/**
 * The three branches in one place, so no screen renders a blank on an error
 * it forgot to handle.
 */
export function Loaded<T>({
  state,
  skeleton,
  framed = false,
  children,
}: {
  state: {
    status: "loading" | "ready" | "error";
    data: T | null;
    error: ApiError | null;
    /** Set when a reload over data already on screen failed. Optional so a
     *  caller holding its own state can still use this. */
    refreshError?: ApiError | null;
    reload: () => void;
  };
  skeleton?: ReactNode;
  /** Set on the screens whose loaded content is cards rather than rows, so the
   *  wait and the failure get a surface too. Without it the provider keys page
   *  put its "your role cannot see this" on the bare page background, which
   *  read as a screen that had half rendered rather than as an answer. */
  framed?: boolean;
  children: (data: T) => ReactNode;
}) {
  if (state.status === "error" && state.error) {
    const failed = <ErrorState error={state.error} retry={state.reload} />;
    return framed ? (
      <div className="rounded-lg border border-rule bg-card">{failed}</div>
    ) : (
      failed
    );
  }
  // "ready with no data" is not a state a page should have to handle, and it
  // is reachable: a hook whose deps have changed still reports the previous
  // result for one render. Handing null to children here crashed the network
  // page to a white screen the first time anybody used it, so this treats it
  // as still loading rather than passing it on.
  if (state.status === "loading" || state.data === null || state.data === undefined) {
    return <>{skeleton ?? <TableSkeleton />}</>;
  }
  // A reload that failed over data already on screen. It is NOT `state.error`,
  // because that branch replaces the page, and replacing a correct table with
  // a full page failure loses what the reader had. The rows stay, this says
  // they are the older answer, and Try again is the same reload.
  //
  // Announced with role="status" rather than role="alert": the reader did not
  // lose anything and is not being interrupted, and an assertive announcement
  // over a table somebody is reading is its own defect.
  const stale = state.refreshError ? (
    <div
      role="status"
      // A surface of its own, with the page's own radius and rule, rather than
      // a bleed strip above the first card: `children` is a different shape on
      // every screen, so anything that tries to attach to what follows attaches
      // correctly on one page and floats on the rest.
      //
      // The warn tint is Badge's, by value, rather than a second one.
      className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-rule bg-[rgba(138,90,0,0.12)] px-4 py-3"
    >
      <p className="text-[12.5px] leading-5 text-ink">
        Could not refresh. Showing the last answer. {state.refreshError.message}
      </p>
      <Button onClick={state.reload}>Try again</Button>
    </div>
  ) : null;

  return (
    <>
      {stale}
      {children(state.data)}
    </>
  );
}
