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

import { useState } from "react";
import { usePathname } from "next/navigation";
import Link from "next/link";
import {
  IconAudit,
  IconOperators,
  IconSignOut,
  IconTenants,
  LogoMark,
} from "@/components/icons";
import { Button, Field, Lede, Standalone, inputClass } from "@/components/ui";
import { adminSignIn, adminSignOut, operatorMay, useAdminContext } from "@/lib/admin";
import type { ApiError } from "@/lib/api";

const NAV = [
  { href: "/admin", label: "Tenants", Icon: IconTenants, permission: "admin.tenants.read" },
  { href: "/admin/audit", label: "Operator log", Icon: IconAudit, permission: "admin.audit.read" },
  {
    href: "/admin/operators",
    label: "Operators",
    Icon: IconOperators,
    permission: "admin.operators.read",
  },
];

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
        <Button type="submit" variant="primary" full disabled={busy}>
          {busy ? "Signing in" : "Sign in"}
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
          full
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

function NavList({ onNavigate }: { onNavigate?: () => void }) {
  const path = usePathname();
  const { me } = useAdminContext();
  const entries = NAV.filter((n) => operatorMay(me, n.permission));

  return (
    <ul className="grid gap-0.5">
      {entries.map(({ href, label, Icon }) => {
        // Exact match for the index, prefix for the rest, or /admin would be
        // marked current on every page in the portal.
        const current = href === "/admin" ? path === href : path.startsWith(href);
        return (
          <li key={href}>
            <Link
              href={href}
              onClick={onNavigate}
              aria-current={current ? "page" : undefined}
              className={[
                // min-h-11 so every target clears the 44px floor on a phone,
                // where this list is the drawer rather than the rail.
                "flex min-h-11 items-center gap-2.5 rounded-md px-2.5 text-[13.5px]",
                current
                  ? "bg-[rgba(255,255,255,0.12)] font-medium text-white"
                  : "text-[rgba(255,255,255,0.72)] hover:bg-[rgba(255,255,255,0.07)] hover:text-white",
              ].join(" ")}
            >
              <Icon className="h-4 w-4 shrink-0" />
              {label}
            </Link>
          </li>
        );
      })}
    </ul>
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
          <Button variant="primary" full onClick={reload}>
            Try again
          </Button>
        </div>
      </Standalone>
    );
  }

  if (!me) return <SignIn onSignedIn={reload} />;
  if (me.impersonating) return <Impersonating label={me.label} />;

  return (
    <div className="min-h-dvh lg:grid lg:grid-cols-[232px_1fr]">
      <aside className="sticky top-0 hidden h-dvh flex-col border-r border-rule bg-ink px-3 py-4 lg:flex">
        <Wordmark />
        <nav aria-label="Operator portal" className="mt-6 flex-1">
          <NavList />
        </nav>
        <Who />
      </aside>

      <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-rule bg-ink px-4 lg:hidden">
        <Wordmark />
        <button
          type="button"
          onClick={() => setMenu(true)}
          aria-expanded={menu}
          aria-label="Open the menu"
          className="grid h-11 w-11 place-items-center rounded-md text-white hover:bg-[rgba(255,255,255,0.1)]"
        >
          <svg viewBox="0 0 20 20" className="h-5 w-5" fill="none" aria-hidden>
            <path d="M3 6h14M3 10h14M3 14h14" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
          </svg>
        </button>
      </header>

      {menu ? (
        <div className="fixed inset-0 z-40 lg:hidden">
          <button
            type="button"
            aria-label="Close the menu"
            onClick={() => setMenu(false)}
            className="absolute inset-0 bg-[rgba(16,16,16,0.45)]"
          />
          <div className="absolute inset-y-0 right-0 flex w-[min(300px,86vw)] flex-col overflow-y-auto border-l border-rule bg-ink px-3 py-4">
            <div className="flex items-center justify-between px-2.5">
              <span className="text-[13px] font-semibold uppercase tracking-[0.12em] text-white">
                Operator
              </span>
              <button
                type="button"
                onClick={() => setMenu(false)}
                aria-label="Close the menu"
                className="-mr-2 grid h-11 w-11 place-items-center rounded-md text-white hover:bg-[rgba(255,255,255,0.1)]"
              >
                <svg viewBox="0 0 20 20" className="h-5 w-5" fill="none" aria-hidden>
                  <path d="M5 5l10 10M15 5 5 15" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                </svg>
              </button>
            </div>
            <nav aria-label="Operator portal" className="mt-6 flex-1">
              <NavList onNavigate={() => setMenu(false)} />
            </nav>
            <Who />
          </div>
        </div>
      ) : null}

      <main className="min-w-0">{children}</main>
    </div>
  );
}
