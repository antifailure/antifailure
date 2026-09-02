"use client";

/**
 * The operator portal's chrome.
 *
 * WHY IT LOOKS DELIBERATELY UNLIKE THE CUSTOMER CONSOLE. The two are served
 * from one origin and a person on this team will have both open. Every control
 * in here acts on somebody else's account, so the one thing the chrome has to
 * do before it does anything else is answer "which of the two am I looking at"
 * from across a desk. That is the dark rail and the standing OPERATOR label,
 * and it is the only place this portal departs from the console's visual
 * system: the tokens, the type scale, the cards, the tables and every state
 * component are the console's, unchanged.
 *
 * It is NOT a warning colour and there is nothing animated. A portal an
 * operator lives in all day cannot spend its attention budget on telling them
 * where they are; it has to be legible at a glance and then get out of the way.
 */

import { useEffect, useRef, useState } from "react";
import { usePathname } from "next/navigation";
import Link from "next/link";
import { IconSignOut, LogoMark } from "@/components/icons";
import { Button, Field, Lede, Standalone, inputClass } from "@/components/ui";
import { ADMIN_NAV, ADMIN_OVERVIEW } from "@/lib/admin-nav";
import type { AdminNavItem } from "@/lib/admin-nav";
import { adminSignIn, adminSignOut, operatorMay, useAdminContext } from "@/lib/admin";
import type { AdminMe } from "@/lib/admin";
import type { ApiError } from "@/lib/api";

/* -------------------------------------------------------------------------
 * Signed out
 * ---------------------------------------------------------------------- */

function SignIn({ onSignedIn }: { onSignedIn: () => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await adminSignIn(email, password);
      onSignedIn();
    } catch (err) {
      // The server sends one sentence for a wrong password, an unknown address,
      // an operator with no password and a suspended one, so that this screen
      // cannot be used to find out which operators exist. It is shown as sent
      // rather than replaced with something friendlier, because a friendlier
      // message here would be a guess about which of those four happened.
      setError((err as ApiError).message);
      setBusy(false);
    }
  }

  return (
    <Standalone title="Operator sign-in" width={400}>
      <Lede>
        This is the platform&rsquo;s own portal. It is a separate account from your Antifailure
        organization, and signing in here does not sign you in there.
      </Lede>
      <form onSubmit={submit} className="mt-7 grid gap-4" noValidate>
        <Field label="Email">
          <input
            className={inputClass}
            type="email"
            name="email"
            autoComplete="username"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        <Field label="Password">
          <input
            className={inputClass}
            type="password"
            name="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        {error ? (
          // role="alert" because the reader submitted and is waiting for this
          // one answer, which is the case an assertive announcement is for.
          <p role="alert" className="text-[12.5px] leading-5 text-fail">
            {error}
          </p>
        ) : null}
        {/* busy rather than a spinner beside it: the console's Button already
            has a pressed-and-waiting state, and a second convention for "this
            is working" is how two buttons in one product stop matching. */}
        <Button type="submit" variant="primary" busy={busy} disabled={busy}>
          Sign in
        </Button>
      </form>
    </Standalone>
  );
}

/* -------------------------------------------------------------------------
 * Impersonating
 * ---------------------------------------------------------------------- */

/**
 * What an operator sees when their session is impersonating a customer.
 *
 * A whole screen rather than a banner over a working portal, because the gate
 * refuses EVERY admin procedure while this is true. A portal rendered with a
 * warning strip on top would be a page of failed panels underneath, and the
 * reader would have to infer the one cause from a dozen symptoms.
 */
function Impersonating({ label }: { label: string }) {
  return (
    <Standalone title="This session is impersonating a customer" width={460} alert>
      <Lede>
        You are signed in as <strong className="font-medium text-ink">{label}</strong>, and this
        session is currently acting as a customer. The operator portal is closed to it until the
        impersonation ends. That is deliberate: starting an impersonation is itself an operator
        action, so a session that is already inside one cannot take another.
      </Lede>
      <div className="mt-7">
        <Button
          variant="primary"
          onClick={() => {
            void adminSignOut().then(() => window.location.reload());
          }}
        >
          End this session
        </Button>
      </div>
    </Standalone>
  );
}

/* -------------------------------------------------------------------------
 * The chrome
 * ---------------------------------------------------------------------- */

/**
 * One navigation entry.
 *
 * A real anchor, so it can be opened in a new tab, focused, and announced as a
 * link. `aria-current="page"` rather than colour alone: the current entry is
 * the one piece of state in this rail, and a lighter background is not
 * something a screen reader can report.
 */
function NavLink({ item, onNavigate }: { item: AdminNavItem; onNavigate?: () => void }) {
  const path = usePathname();
  // Exact for the overview, prefix for everything else. Without the exception
  // /admin would be marked current on all twenty three pages; with the prefix,
  // a detail route under a section keeps its section marked, which is what the
  // reader wants when they are two levels in.
  const current = item.href === "/admin" ? path === item.href : path.startsWith(item.href);
  const { Icon } = item;
  return (
    <li>
      <Link
        href={item.href}
        onClick={onNavigate}
        aria-current={current ? "page" : undefined}
        title={item.summary}
        className={[
          // min-h-11 so every target clears the 44px floor on a phone, where
          // this list is the drawer rather than the rail.
          "flex min-h-11 items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13.5px] leading-5",
          current
            ? "bg-[rgba(255,255,255,0.12)] font-medium text-white"
            : "text-[rgba(255,255,255,0.72)] hover:bg-[rgba(255,255,255,0.07)] hover:text-white",
        ].join(" ")}
      >
        <Icon className="h-4 w-4 shrink-0" />
        {/* The label wraps rather than truncating. "Experiments & Feature
            Flags" does not fit 232px on one line, and an entry ending in an
            ellipsis is an entry somebody has to click to identify. */}
        <span className="min-w-0">{item.label}</span>
      </Link>
    </li>
  );
}

/**
 * The whole rail: the overview, then six groups.
 *
 * The groups are real `<ul>`s under real headings rather than one flat list
 * with dividers, so the structure a sighted reader gets from the spacing is
 * the structure a screen reader gets from the markup. Twenty three entries in
 * one undifferentiated list is a list nobody navigates twice.
 *
 * An entry whose permission the operator does not hold is not rendered, and a
 * GROUP whose entries are all hidden loses its heading too. A heading over
 * nothing reads as a section that failed to load.
 */
function NavList({ me, onNavigate }: { me: AdminMe | null; onNavigate?: () => void }) {
  const groups = ADMIN_NAV.map((group) => ({
    ...group,
    items: group.items.filter((item) => operatorMay(me, item.permission)),
  })).filter((group) => group.items.length > 0);

  return (
    <div className="grid gap-5">
      {operatorMay(me, ADMIN_OVERVIEW.permission) ? (
        <ul className="grid gap-0.5">
          <NavLink item={ADMIN_OVERVIEW} onNavigate={onNavigate} />
        </ul>
      ) : null}

      {groups.map((group) => (
        <section key={group.slug} aria-labelledby={`nav-${group.slug}`}>
          <h2
            id={`nav-${group.slug}`}
            className="px-2.5 pb-1.5 text-[11px] font-medium uppercase tracking-[0.1em] text-[rgba(255,255,255,0.55)]"
          >
            {group.label}
          </h2>
          <ul className="grid gap-0.5">
            {group.items.map((item) => (
              <NavLink key={item.href} item={item} onNavigate={onNavigate} />
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function Who() {
  const { me } = useAdminContext();
  if (!me) return null;
  return (
    <div className="mt-4 border-t border-[rgba(255,255,255,0.14)] pt-3">
      <p className="truncate px-2.5 text-[12.5px] text-white">{me.label}</p>
      <p className="truncate px-2.5 text-[12px] text-[rgba(255,255,255,0.6)]">{me.email}</p>
      <p className="mt-1 px-2.5 text-[11.5px] uppercase tracking-[0.1em] text-[rgba(255,255,255,0.6)]">
        {me.role.replace(/_/g, " ")}
      </p>
      <button
        type="button"
        onClick={() => {
          void adminSignOut().then(() => window.location.reload());
        }}
        className="mt-2 flex min-h-11 w-full items-center gap-2.5 rounded-md px-2.5 text-[13.5px] text-[rgba(255,255,255,0.72)] hover:bg-[rgba(255,255,255,0.07)] hover:text-white"
      >
        <IconSignOut className="h-4 w-4 shrink-0" />
        Sign out
      </button>
    </div>
  );
}

/** The rail's wordmark, which says OPERATOR rather than Antifailure. The
 *  product name is not the useful word here: everything in this window is
 *  Antifailure, and the thing worth reading is whose data it acts on. */
function Wordmark() {
  return (
    <span className="flex items-center gap-2 px-2.5">
      <LogoMark className="h-[18px] w-[18px] text-white" />
      <span className="text-[13px] font-semibold uppercase tracking-[0.12em] text-white">
        Operator
      </span>
    </span>
  );
}

/**
 * The rail's navigation, on a phone.
 *
 * A native `dialog` opened with `showModal`, which is the same choice `Confirm`
 * and `Drawer` make and for the same reasons: it brings focus containment,
 * Escape, an inert background and `aria-modal` with it, and every hand rolled
 * drawer in every console gets at least one of those wrong. It also RESTORES
 * focus to the button that opened it on close, which is the half of focus
 * management that is always the one left out.
 *
 * The one thing the element does not reliably give is a still page underneath,
 * so the body is locked here, and unlocked in the same effect's teardown so an
 * unmount while open cannot leave the page unscrollable.
 */
function NavDrawer({
  open,
  me,
  onClose,
}: {
  open: boolean;
  me: AdminMe | null;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (open && !el.open) el.showModal();
    if (!open && el.open) el.close();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  }, [open]);

  return (
    <dialog
      ref={ref}
      data-surface="inverted"
      aria-label="Operator portal"
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        // The backdrop is this element's own pseudo element, so a click on it
        // targets the dialog. Anything inside the panel stops before here.
        if (e.target === ref.current) onClose();
      }}
      // m-0 with mr-auto puts it against the left edge instead of the middle,
      // where the user agent would centre it. Tailwind's preflight zeroes the
      // margin that would have done the centring, so both halves are needed.
      className="m-0 mr-auto h-dvh max-h-dvh w-[min(300px,88vw)] max-w-full flex-col overflow-y-auto border-r border-rule bg-ink p-0 backdrop:bg-[rgba(16,16,16,0.55)] lg:hidden"
    >
      <div className="flex min-h-dvh flex-col px-3 py-4">
        <div className="flex items-center justify-between gap-2">
          <Wordmark />
          <button
            type="button"
            onClick={onClose}
            aria-label="Close the menu"
            className="-mr-1 grid h-11 w-11 shrink-0 place-items-center rounded-md text-white hover:bg-[rgba(255,255,255,0.1)]"
          >
            <svg viewBox="0 0 20 20" className="h-5 w-5" fill="none" aria-hidden>
              <path
                d="M5 5l10 10M15 5 5 15"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>
        <nav aria-label="Sections" className="mt-6 flex-1">
          <NavList me={me} onNavigate={onClose} />
        </nav>
        <Who />
      </div>
    </dialog>
  );
}

export function AdminShell({ children }: { children: React.ReactNode }) {
  const { me, status, error, reload } = useAdminContext();
  const [menu, setMenu] = useState(false);

  if (status === "loading") {
    // A quiet hold rather than a spinner. The portal is one fetch from ready
    // and a spinner here would be the only moving thing on the screen.
    return (
      <main className="grid min-h-dvh place-items-center px-5">
        <p className="text-[13px] text-muted">Checking your operator session</p>
      </main>
    );
  }

  // 401 is "not signed in", which is a screen and not a failure. Anything else
  // is the control plane failing to answer, and saying so beats a sign-in form
  // that will not work.
  if (status === "error" && error && error.status !== 401) {
    return (
      <Standalone title="The control plane did not answer" width={440} alert>
        <Lede>{error.message}</Lede>
        <div className="mt-7">
          <Button variant="primary" onClick={reload}>
            Try again
          </Button>
        </div>
      </Standalone>
    );
  }

  if (!me) return <SignIn onSignedIn={reload} />;
  if (me.impersonating) return <Impersonating label={me.label} />;

  return (
    <div className="min-h-dvh lg:grid lg:grid-cols-[236px_1fr]">
      {/* The rail scrolls on its own rather than with the page. Twenty three
          entries are taller than a laptop viewport, and a rail that scrolls the
          document takes the content with it. */}
      <aside
        data-surface="inverted"
        className="sticky top-0 hidden h-dvh flex-col overflow-y-auto border-r border-rule bg-ink px-3 py-4 lg:flex"
      >
        <Wordmark />
        <nav aria-label="Operator portal" className="mt-6 flex-1">
          <NavList me={me} />
        </nav>
        <Who />
      </aside>

      <header
        data-surface="inverted"
        className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-rule bg-ink px-4 lg:hidden"
      >
        <Wordmark />
        <button
          type="button"
          onClick={() => setMenu(true)}
          aria-expanded={menu}
          aria-label="Open the menu"
          className="-mr-1 grid h-11 w-11 place-items-center rounded-md text-white hover:bg-[rgba(255,255,255,0.1)]"
        >
          <svg viewBox="0 0 20 20" className="h-5 w-5" fill="none" aria-hidden>
            <path
              d="M3 6h14M3 10h14M3 14h14"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
            />
          </svg>
        </button>
      </header>

      <NavDrawer open={menu} me={me} onClose={() => setMenu(false)} />

      <main className="min-w-0">{children}</main>
    </div>
  );
}
