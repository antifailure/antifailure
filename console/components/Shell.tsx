"use client";

import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import Link from "next/link";
import {
  GitHubMark,
  IconAudit,
  IconEnvironments,
  IconKeys,
  IconMasking,
  IconMembers,
  IconNetwork,
  IconRuns,
  IconSignOut,
  LogoMark,
} from "@/components/icons";
import { rest, type Session } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import { Button, Field, inputClass } from "@/components/ui";

const NAV = [
  { href: "/environments", label: "Environments", Icon: IconEnvironments },
  { href: "/runs", label: "Runs", Icon: IconRuns },
  { href: "/masking", label: "Masking", Icon: IconMasking },
  { href: "/network", label: "Network", Icon: IconNetwork },
  { href: "/audit", label: "Audit", Icon: IconAudit },
  { href: "/members", label: "Members", Icon: IconMembers },
  { href: "/keys", label: "Provider keys", Icon: IconKeys },
];

/* -------------------------------------------------------------------------
 * Signed out
 * ---------------------------------------------------------------------- */

/**
 * Signing in with a link.
 *
 * Rendered only when the API says this deployment has mail configured, which
 * it reports on the session endpoint. That is the whole reason the field
 * exists: this console is one static export served by every installation, so
 * it cannot know at build time, and a sign-in method offered where it cannot
 * work is worse than one not offered at all.
 *
 * It is the way in that works where github.com is not reachable, which is
 * every preview environment by design and every isolated network by
 * circumstance.
 *
 * The answer is the same for an address with an account, an address without
 * one, and something that is not an address. That is deliberate: three
 * different answers would turn this field into a way to ask whether somebody
 * works here.
 */
function EmailSignIn() {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function send(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await rest("/auth/email", { method: "POST", body: { email } });
      setSent(true);
    } catch {
      // Ours, not theirs. Telling somebody their link is on the way when it
      // is not is the one answer worth distinguishing.
      setError("Could not send a link just now. Try again.");
    } finally {
      setBusy(false);
    }
  }

  if (sent) {
    return (
      <p
        role="status"
        className="mt-6 rounded-[6px] border border-rule bg-card px-3.5 py-3 text-[13px] leading-6 text-muted"
      >
        Check your mail. If {email} has an account here, a sign-in link is on
        its way and it is good for fifteen minutes.
      </p>
    );
  }

  return (
    <>
      <div className="mt-6 flex items-center gap-3" aria-hidden="true">
        <span className="h-px flex-1 bg-rule" />
        <span className="text-[11.5px] uppercase tracking-wide text-dim">or</span>
        <span className="h-px flex-1 bg-rule" />
      </div>
      <form className="mt-5" onSubmit={send}>
        <Field
          label="Email address"
          error={error}
          hint="We send a link that signs you in. No password."
        >
          <input
            className={inputClass}
            type="email"
            name="email"
            required
            autoComplete="email"
            placeholder="you@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        <div className="mt-4">
          <Button type="submit" variant="secondary" busy={busy} disabled={email.trim() === ""}>
            {busy ? "Sending a link" : "Send a sign-in link"}
          </Button>
        </div>
      </form>
    </>
  );
}

function SignIn({ session }: { session: Session }) {
  // A control plane that predates the methods field answers without one, and
  // GitHub is the way in that has always existed. Read as "GitHub only"
  // rather than as "none", so an older API is a page with one button on it
  // instead of a page with none.
  const methods = session.methods ?? ["github"];
  return (
    <main className="grid min-h-dvh place-items-center px-5 py-10">
      <div className="w-full max-w-[400px]">
        <LogoMark className="h-9 w-9" />
        <h1 className="mt-7 text-[30px] font-semibold leading-dense tracking-tighter text-ink">
          Sign in
        </h1>
        <p className="mt-3 text-[13.5px] leading-6 text-muted">
          This control plane is invitation only while it is in development. Sign
          in with the GitHub account that was invited.
        </p>
        <a
          href="/auth/github"
          className="mt-6 flex h-11 w-full items-center justify-center gap-2.5 rounded-[6px] bg-ink text-[14px] font-medium text-white transition-colors hover:bg-[#2b2b2b]"
        >
          <GitHubMark />
          Continue with GitHub
        </a>
        {methods.includes("email") ? <EmailSignIn /> : null}
        <p className="mt-6 text-[12.5px] leading-6 text-dim">
          The engine itself needs none of this. It is open source, it runs on
          your own machine, and the{" "}
          <a
            className="text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink"
            href="https://antifailure.dev/docs/getting-started/quickstart"
          >
            quickstart
          </a>{" "}
          works without an account.
        </p>
      </div>
    </main>
  );
}

/**
 * Signed in, and in no organization.
 *
 * Not an error and not an empty dashboard. Being let through the door is not
 * being given a tenant: membership arrives when the GitHub App reports an
 * installation, and until it does there is genuinely nothing here to show.
 * Saying so is the difference between a product that is waiting and one that
 * looks broken.
 */
function NoOrganization({ session }: { session: Session }) {
  return (
    <main className="grid min-h-dvh place-items-center px-5 py-10">
      <div className="w-full max-w-[440px]">
        <LogoMark className="h-9 w-9" />
        <h1 className="mt-7 text-[30px] font-semibold leading-dense tracking-tighter text-ink">
          No organization yet
        </h1>
        <p className="mt-3 text-[13.5px] leading-6 text-muted">
          You are signed in as {session.label}. Your account is not a member of
          an organization on this control plane, so there is nothing to show
          you yet -- not an empty dashboard, nothing.
        </p>
        <p className="mt-3 text-[13.5px] leading-6 text-muted">
          Membership follows a GitHub App installation. Once the app is
          installed on an organization you belong to, this page becomes that
          organization.
        </p>
        <div className="mt-6">
          <SignOutButton />
        </div>
      </div>
    </main>
  );
}

function SignOutButton() {
  const [busy, setBusy] = useState(false);
  return (
    <Button
      busy={busy}
      onClick={async () => {
        setBusy(true);
        try {
          await rest("/auth/signout", { method: "POST" });
        } finally {
          window.location.href = "/";
        }
      }}
    >
      <IconSignOut className="h-4 w-4" />
      Sign out
    </Button>
  );
}

/* -------------------------------------------------------------------------
 * Signed in
 * ---------------------------------------------------------------------- */

function NavList({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  return (
    <ul className="space-y-0.5">
      {NAV.map(({ href, label, Icon }) => {
        const active = pathname === href;
        return (
          <li key={href}>
            <Link
              href={href}
              onClick={onNavigate}
              aria-current={active ? "page" : undefined}
              className={`flex h-9 items-center gap-2.5 rounded-[5px] px-2.5 text-[13px] tracking-snug transition-colors ${
                active
                  ? "bg-[rgba(16,16,16,0.06)] font-medium text-ink"
                  : "text-muted hover:bg-[rgba(16,16,16,0.035)] hover:text-ink"
              }`}
            >
              <Icon className={`h-4 w-4 shrink-0 ${active ? "text-ink" : "text-dim"}`} />
              {label}
            </Link>
          </li>
        );
      })}
    </ul>
  );
}

function Who({ session }: { session: Session }) {
  return (
    <div className="border-t border-rule px-2.5 pt-3">
      <p className="truncate text-[12.5px] font-medium text-ink">{session.label}</p>
      <p className="mt-0.5 text-[11.5px] uppercase tracking-[0.08em] text-dim">
        {session.role ?? "no role"}
      </p>
      <div className="mt-3">
        <SignOutButton />
      </div>
    </div>
  );
}

export function Shell({ children }: { children: ReactNode }) {
  const session = useSessionContext();
  const [menu, setMenu] = useState(false);
  const closer = useRef<HTMLButtonElement>(null);

  // Escape closes the drawer, and while it is open the page behind it does not
  // scroll. Both are the parts of a mobile menu that usually get skipped.
  useEffect(() => {
    if (!menu) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenu(false);
    };
    document.addEventListener("keydown", onKey);
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    closer.current?.focus();
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = previous;
    };
  }, [menu]);

  if (session.status === "loading") {
    return (
      <div className="grid min-h-dvh place-items-center" role="status">
        <LogoMark className="h-8 w-8 opacity-40" />
        <span className="sr-only">Loading</span>
      </div>
    );
  }

  // A session endpoint that cannot be reached is not the same as being signed
  // out, and offering "Continue with GitHub" to somebody whose network is down
  // sends them through an OAuth round trip to land back here.
  if (session.status === "error") {
    return (
      <main className="grid min-h-dvh place-items-center px-5 py-10">
        <div className="w-full max-w-[400px]" role="alert">
          <LogoMark className="h-9 w-9" />
          <h1 className="mt-7 text-[26px] font-semibold leading-dense tracking-tighter text-ink">
            The control plane did not answer
          </h1>
          <p className="mt-3 text-[13.5px] leading-6 text-muted">{session.error?.message}</p>
          <div className="mt-6">
            <Button onClick={session.reload}>Try again</Button>
          </div>
        </div>
      </main>
    );
  }

  const me = session.data as Session;
  if (!me.signedIn) return <SignIn session={me} />;
  if (!me.orgId) return <NoOrganization session={me} />;

  return (
    <div className="min-h-dvh lg:grid lg:grid-cols-[232px_1fr]">
      {/* Desktop rail */}
      <aside className="sticky top-0 hidden h-dvh flex-col border-r border-rule bg-[#fbfbfa] px-3 py-4 lg:flex">
        <Link href="/environments" className="flex items-center gap-2 px-2.5">
          <LogoMark className="h-[18px] w-[18px]" />
          <span className="text-[13px] font-semibold uppercase tracking-[0.12em] text-ink">
            Antifailure
          </span>
        </Link>
        <nav aria-label="Console" className="mt-6 flex-1">
          <NavList />
        </nav>
        <Who session={me} />
      </aside>

      {/* Mobile bar */}
      <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-rule bg-paper px-4 lg:hidden">
        <Link href="/environments" className="flex items-center gap-2">
          <LogoMark className="h-[18px] w-[18px]" />
          <span className="text-[13px] font-semibold uppercase tracking-[0.12em] text-ink">
            Antifailure
          </span>
        </Link>
        <button
          type="button"
          onClick={() => setMenu(true)}
          aria-expanded={menu}
          aria-label="Open the menu"
          className="grid h-11 w-11 place-items-center rounded-[5px] text-ink"
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
            className="absolute inset-0 bg-[rgba(16,16,16,0.35)]"
          />
          <div className="absolute inset-y-0 right-0 flex w-[min(300px,86vw)] flex-col border-l border-rule bg-paper px-3 py-4">
            <div className="flex items-center justify-between px-2.5">
              <span className="text-[13px] font-semibold uppercase tracking-[0.12em] text-ink">
                Menu
              </span>
              <button
                ref={closer}
                type="button"
                onClick={() => setMenu(false)}
                aria-label="Close the menu"
                className="grid h-11 w-11 place-items-center rounded-[5px] text-ink"
              >
                <svg viewBox="0 0 20 20" className="h-4 w-4" fill="none" aria-hidden>
                  <path d="M5 5l10 10M15 5L5 15" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                </svg>
              </button>
            </div>
            <nav aria-label="Console" className="mt-5 flex-1">
              <NavList onNavigate={() => setMenu(false)} />
            </nav>
            <Who session={me} />
          </div>
        </div>
      ) : null}

      <main className="min-w-0">{children}</main>
    </div>
  );
}
