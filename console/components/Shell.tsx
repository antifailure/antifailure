"use client";

import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import Link from "next/link";
import {
  GitHubMark,
  IconAudit,
  IconEnvironments,
  IconKeys,
  IconLoad,
  IconAnalytics,
  IconMasking,
  IconMembers,
  IconNetwork,
  IconPlan,
  IconRuns,
  IconSettings,
  IconSignOut,
  IconTerminal,
  LogoMark,
} from "@/components/icons";
import { rest, type Session } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import { may } from "@/lib/roles";
import { Button, Field, Lede, LinkButton, Standalone, inputClass } from "@/components/ui";

/**
 * Where to come back to after signing in.
 *
 * A link in a pull request comment points at one page: the environment for
 * that commit, the run, the evidence. Somebody following it who is not signed
 * in used to get the sign-in screen and then the dashboard, with no way back
 * to the thing they clicked, which makes every deep link this product publishes
 * a link to the front door. /device already did this correctly and the rest of
 * the console did not.
 *
 * Read off window rather than from usePathname, because the query string is
 * half the address here: /environments?env=af-1234 and /environments are
 * different pages to the person who followed the link. Guarded, because this
 * console is a static export and window does not exist while it is prerendered.
 */
function returnTo(): string | null {
  if (typeof window === "undefined") return null;
  const here = window.location.pathname + window.location.search;
  // The same shape the API's own safeRedirect accepts: a path on this origin
  // and nothing that could be read as another one. Sending anything else would
  // be refused there and land the person on the dashboard anyway, so it is
  // dropped here where the reason can be written down.
  if (!here.startsWith("/") || here.startsWith("//") || here.includes("\\")) return null;
  return here;
}

const NAV = [
  { href: "/environments", label: "Environments", Icon: IconEnvironments },
  { href: "/runs", label: "Runs", Icon: IconRuns },
  { href: "/load", label: "Load", Icon: IconLoad },
  { href: "/masking", label: "Masking", Icon: IconMasking },
  { href: "/network", label: "Network", Icon: IconNetwork },
  { href: "/audit", label: "Audit", Icon: IconAudit },
  { href: "/members", label: "Members", Icon: IconMembers },
  { href: "/plan", label: "Plan", Icon: IconPlan },
  { href: "/keys", label: "Provider keys", Icon: IconKeys },
  { href: "/cli", label: "Command line", Icon: IconTerminal },
  { href: "/settings", label: "Settings", Icon: IconSettings },
];

/**
 * Analytics is not a customer's page and was in every customer's sidebar.
 *
 * The dashboard behind it covers the WHOLE installation: where people came
 * from, where they landed, how far they got. `routers/analytics.ts` refuses
 * anybody outside the organization named by AF_ANALYTICS_OPERATOR_ORG, so
 * every tenant who clicked it got an error, and on an installation with that
 * variable unset, so did the operator. A navigation entry that always leads to
 * a refusal is worse than no entry.
 *
 * It is not gated on analytics.read. That permission is held by owners and
 * admins of every organization, because it describes a kind of reading rather
 * than a right over this installation. The session's `analyticsOperator` is
 * the only field that answers the question the menu is actually asking.
 */
const OPERATOR_NAV = [
  { href: "/analytics", label: "Analytics", Icon: IconAnalytics },
];

/**
 * The pages that stay reachable when the hosted plan has lapsed.
 *
 * This list is the console half of HOSTED_GATE_EXEMPT in
 * web/apps/api/src/hosted.ts, and it exists for the reason written there: a
 * plan gate may restrict what the product DOES for a customer, and may never
 * restrict their ability to leave, to retrieve what is theirs, or to secure
 * their account. That server-side exemption made billing.manage, data.export,
 * organization.delete, account.close and sessions.manage answer over the API
 * while every screen that reaches them was still refused here, which is a
 * right that exists in the code and not in the product.
 *
 * /plan is the path that RESOLVES the refusal. /exits is the four that do not
 * depend on resolving it.
 *
 * Both render inside the reduced shell below rather than as their own
 * standalone screens, so there is ONE lapsed state with a real header and a
 * way between the two pages, rather than a dead end per route.
 */
const LAPSED_NAV = [
  { href: "/plan", label: "Plan and billing" },
  { href: "/exits", label: "Your data and account" },
];
const LAPSED_PATHS = LAPSED_NAV.map((n) => n.href);

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
      const back = returnTo();
      await rest("/auth/email", {
        method: "POST",
        body: back ? { email, redirect_to: back } : { email },
      });
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
        className="mt-6 rounded-lg border border-rule bg-card px-3.5 py-3 text-[13px] leading-6 text-muted"
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
        <span className="text-[11.5px] uppercase tracking-wide text-muted">or</span>
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
  const back = returnTo();
  const githubSignInHref = back
    ? `/auth/github?redirect_to=${encodeURIComponent(back)}`
    : "/auth/github";
  return (
    <Standalone title="Sign in" width={400}>
      <Lede>
        {session.signupsOpen
          ? "Sign in with GitHub. On your first visit, you will connect the organization and repositories this control plane should serve."
          : "This control plane is invitation only. Sign in with the GitHub account that was invited."}
      </Lede>
      <div className="mt-6">
        <LinkButton href={githubSignInHref} full>
          <GitHubMark />
          Continue with GitHub
        </LinkButton>
      </div>
      {methods.includes("email") ? <EmailSignIn /> : null}
      <p className="mt-6 text-[12.5px] leading-6 text-muted">
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
    </Standalone>
  );
}

/**
 * Signed in, and in no organization.
 *
 * Not an error and not an empty dashboard. Being let through the door is not
 * being given a tenant, and saying so is the difference between a product that
 * is waiting and one that looks broken.
 *
 * THREE DIFFERENT SCREENS, and the session decides which. Two of the three were
 * added within a week of each other and they answer different questions, so the
 * copy is chosen by both facts rather than by whichever was checked last.
 *
 * With self serve signup ON, arriving here means the provisioning step did not
 * produce an organization. That is rare and is worth REPORTING rather than
 * dressing up as a normal wait: the commonest cause is a slug another account
 * already holds, which the signup deliberately will not adopt. Telling somebody
 * to install an App when the plane was supposed to have handed them a tenant is
 * telling them to fix something that is not their problem.
 *
 * With it OFF, this is the ordinary state: membership arrives when the GitHub
 * App reports an installation or when somebody sends an invitation.
 *
 * And the install address is optional, and BOTH ACTIONS USED TO BE HIDDEN WITH
 * IT. That is how this screen became a dead end on two control planes at once:
 * the copy told somebody to install the App and then recheck their membership,
 * and the only button under it was Sign out. Checking membership is
 * `/auth/github`, which never needed the install address for anything, so it is
 * offered either way and becomes the primary action when there is nothing to
 * install from. The copy changes with it, because prose that names an action
 * the page cannot offer is the failure rather than a symptom of it.
 */
function NoOrganization({ session }: { session: Session }) {
  const shouldHaveOne = session.selfServeSignup === true;
  return (
    <Standalone title="No organization yet" width={440}>
      <Lede>
        You are signed in as {session.label}. Your account is not a member of
        an organization on this control plane, so there is nothing to show you
        yet. Not an empty dashboard. Nothing.
      </Lede>
      <Lede>
        {shouldHaveOne
          ? "Signing up here normally creates one for you, so this is not the usual outcome. It happens when the name your account would take is already in use. Installing the GitHub App on an organization gives you that one instead, and an invitation from somebody else works too."
          : session.githubAppInstallUrl
            ? "Membership follows a GitHub App installation or an invitation from somebody already inside. Install it on the organization you want to connect, then ask GitHub to check your membership again."
            : "Membership follows a GitHub App installation or an invitation from somebody already inside, and this control plane has not been told where to install its App. If you already belong to a connected organization, check again below. If you do not, whoever runs this control plane has to give you the installation address."}
      </Lede>
      <div className="mt-6 space-y-3">
        {session.githubAppInstallUrl ? (
          <LinkButton href={session.githubAppInstallUrl} full>
            <GitHubMark />
            Install the GitHub App
          </LinkButton>
        ) : null}
        <LinkButton
          href="/auth/github"
          full
          variant={session.githubAppInstallUrl ? "secondary" : "primary"}
        >
          Check my GitHub membership
        </LinkButton>
      </div>
      <div className="mt-6">
        <SignOutButton />
      </div>
    </Standalone>
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
  const session = useSessionContext();
  // Appended rather than merged into NAV, so the operator's one extra entry
  // sits at the end and the order every customer sees never moves.
  const items = session.data?.analyticsOperator ? [...NAV, ...OPERATOR_NAV] : NAV;
  return (
    <ul className="space-y-0.5">
      {items.map(({ href, label, Icon }) => {
        const active = pathname === href;
        return (
          <li key={href}>
            <Link
              href={href}
              onClick={onNavigate}
              aria-current={active ? "page" : undefined}
              className={`flex h-9 items-center gap-2.5 rounded-md px-2.5 text-[13px] tracking-snug transition-colors ${
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
  const pathname = usePathname();
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
      <Standalone title="The control plane did not answer" width={400} alert>
        <Lede>{session.error?.message}</Lede>
        <div className="mt-6">
          <Button onClick={session.reload}>Try again</Button>
        </div>
      </Standalone>
    );
  }

  const me = session.data as Session;
  if (!me.signedIn) return <SignIn session={me} />;
  if (!me.orgId) return <NoOrganization session={me} />;

  const needsPlan = me.hostedRequiredPlan && me.hostedAccess === false;
  // The billing page answers under billing.manage, which only an owner holds.
  // An admin, member or viewer sent to /plan gets a refusal, so this screen
  // used to offer everybody exactly one action and offer three of the four
  // roles an action that could not work.
  const mayBill = may(me.role, "billing.manage");
  const lapsedNav = LAPSED_NAV.filter((item) => item.href !== "/plan" || mayBill);
  if (needsPlan && !LAPSED_PATHS.includes(pathname)) {
    return (
      <Standalone title="Enterprise access required" width={460}>
        <Lede>
          This hosted control plane serves organizations on the enterprise
          plan. Environments, runs, masking, egress and the audit log are
          closed until a subscription is in place.
        </Lede>
        <Lede>
          Four things stay open whatever the plan says, because they are how
          you leave rather than what you bought: take a copy of everything,
          sign a session out, delete the organization, and close your account.
        </Lede>
        <div className="mt-6 space-y-3">
          {mayBill ? (
            <LinkButton href="/plan" full>
              Open plan and billing
            </LinkButton>
          ) : null}
          <LinkButton href="/exits" full variant={mayBill ? "secondary" : "primary"}>
            Your data and account
          </LinkButton>
          {mayBill ? null : (
            <p className="text-[12.5px] leading-6 text-muted">
              Subscribing is an owner&rsquo;s to do, and your role is{" "}
              {me.role ?? "unknown"}. Ask an owner to open plan and billing.
            </p>
          )}
          <div className="flex justify-center pt-1">
            <SignOutButton />
          </div>
        </div>
      </Standalone>
    );
  }

  if (needsPlan) {
    return (
      <div className="min-h-dvh">
        <header className="border-b border-rule bg-paper">
          <div className="mx-auto flex min-h-14 w-full max-w-[1120px] items-center justify-between gap-4 px-5 sm:px-8 lg:px-10">
            <Link href={mayBill ? "/plan" : "/exits"} className="flex min-h-11 items-center gap-2">
              <LogoMark className="h-[18px] w-[18px]" />
              <span className="text-[13px] font-semibold uppercase tracking-[0.12em] text-ink">
                Antifailure
              </span>
            </Link>
            <SignOutButton />
          </div>
          {/* The second row carries the label and the two links.

              The label was on the top row and hidden below sm, which is a
              problem on a screen that asks you to type it back: closing an
              account confirms against it. Putting it back on the top row at
              320 truncated it to two characters, which is worse than absent. It
              gets its own line instead, where it fits whole at every width.

              The links are hidden when only one survives the filter, which is
              what a member, an admin or a viewer sees: a single tab, always
              active, pointing at the page you are already on, is chrome that
              carries no information. */}
          <div className="mx-auto flex w-full max-w-[1120px] flex-wrap items-center justify-between gap-x-4 gap-y-1 px-5 pb-2 sm:px-8 lg:px-10">
            {lapsedNav.length > 1 ? (
              <nav aria-label="Available on a lapsed plan">
                <ul className="flex flex-wrap gap-1">
                  {lapsedNav.map((item) => {
                    const active = pathname === item.href;
                    return (
                      <li key={item.href}>
                        <Link
                          href={item.href}
                          aria-current={active ? "page" : undefined}
                          className={`flex h-9 items-center rounded-md px-2.5 text-[13px] tracking-snug transition-colors ${
                            active
                              ? "bg-[rgba(16,16,16,0.06)] font-medium text-ink"
                              : "text-muted hover:bg-[rgba(16,16,16,0.035)] hover:text-ink"
                          }`}
                        >
                          {item.label}
                        </Link>
                      </li>
                    );
                  })}
                </ul>
              </nav>
            ) : (
              <span />
            )}
            <span className="min-w-0 truncate py-1 text-[12.5px] text-muted">
              Signed in as {me.label}
            </span>
          </div>
        </header>
        <main>{children}</main>
      </div>
    );
  }

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
        <Link href="/environments" className="-ml-2 flex min-h-11 items-center gap-2 rounded-md px-2">
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
          className="grid h-11 w-11 place-items-center rounded-md text-ink hover:bg-[rgba(16,16,16,0.05)]"
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
          <div className="absolute inset-y-0 right-0 flex w-[min(300px,86vw)] flex-col overflow-y-auto border-l border-rule bg-paper px-3 py-4">
            <div className="flex items-center justify-between px-2.5">
              <span className="text-[13px] font-semibold uppercase tracking-[0.12em] text-ink">
                Menu
              </span>
              <button
                ref={closer}
                type="button"
                onClick={() => setMenu(false)}
                aria-label="Close the menu"
                className="grid h-11 w-11 place-items-center rounded-md text-ink hover:bg-[rgba(16,16,16,0.05)]"
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
