"use client";

import { useEffect } from "react";
import { useChrome } from "./Chrome";
import { LogoMark } from "./icons";
import { controlPlaneUrl } from "@/lib/control-plane-routes";
import { ctaEngaged } from "@/lib/analytics";

function GitHubMark({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
    </svg>
  );
}

/**
 * The sign-up screen, which for the first time describes something that happens.
 *
 * WHAT THIS PAGE HAS BEEN, in order, because each version was a true statement
 * about the product at the time and the page is where they went stale.
 *
 * It rendered an email field, a password field with a show and hide toggle and
 * `autocomplete="new-password"`, and a Continue button. No password was ever
 * checked, stored or sent: the submit handler called `setPassword("")` and
 * dropped it. So the field taught browsers to offer to save a credential for a
 * site with no account to attach it to, and people reuse passwords.
 *
 * Then it was a waitlist that wrote to `localStorage` under a heading promising
 * mail, which nothing could have sent because the address never left the
 * browser. Then it was a waitlist that really did post to a server, under a
 * heading promising mail that still could not be sent: antifailure.dev publishes
 * no mail exchanger, an SPF policy of `v=spf1 -all` and a DMARC policy of
 * reject, so the domain authorizes no sender at all. Somebody who left an
 * address was waiting for a message with no route to them.
 *
 * The heading was "Request access", because nothing here started. Now something
 * does. Pressing the button completes a GitHub exchange against the control
 * plane, which writes a user, creates an organization on the free plan and
 * makes that person its owner, and the free plan's quotas are enforced against
 * it. So the page says what will happen, in the order it happens, and every
 * sentence names something in the code rather than something we intend.
 *
 * THERE IS NO FORM ON THIS PAGE, and that is the design rather than an
 * omission. A field asking for an address would be asking for something the
 * next screen is about to receive from GitHub, already verified, and a second
 * copy typed by hand is a second thing to be wrong. It also means this screen
 * has no request to make and so no loading, error or empty state to design: the
 * one button is a link. The screen that DOES take something from a stranger is
 * the enterprise form on /contact, and the states live there.
 */
function AuthCover() {
  return (
    <div className="relative hidden h-full overflow-hidden bg-[#f7f7f5] lg:block">
      <div
        className="absolute inset-0"
        style={{
          background:
            "radial-gradient(ellipse 90% 70% at 6% 4%, rgba(51, 191, 0, 0.16) 0%, transparent 52%), radial-gradient(ellipse 85% 70% at 96% 98%, rgba(16, 16, 20, 0.10) 0%, transparent 54%)",
        }}
      />
      <div className="auth-honeycomb absolute inset-0 opacity-80" />
      <div className="relative z-10 flex h-full flex-col items-center justify-center px-16 text-center">
        <LogoMark className="h-14 w-14" />
        <p className="mt-8 max-w-[320px] text-[32px] font-normal leading-dense tracking-tighter text-black">
          Know what happens before you deploy.
        </p>
      </div>
    </div>
  );
}

/**
 * What pressing the button does, in the order the control plane does it.
 *
 * Three steps rather than a list of benefits, because a benefit is a claim and
 * a step is checkable. Each of these is one thing in the source: the exchange
 * refuses an account GitHub reports no verified address for, `provision.ts`
 * creates the organization and writes the owner membership, and `PLAN_QUOTAS`
 * is what `environments.create` refuses against.
 */
const STEPS: { title: string; body: string }[] = [
  {
    title: "GitHub confirms who you are",
    body: "We ask GitHub for your account and a verified email address. An account with no verified address is refused, and no password is created here or anywhere else.",
  },
  {
    title: "You land in your own organization",
    body: "Named after your GitHub account, on the free plan, with you as its owner. Installing our GitHub App later adopts that same organization rather than making a second one.",
  },
  {
    title: "You connect a repository",
    body: "From the console. Nothing runs until you do, and runs execute in your cloud with your credentials.",
  },
];

function StepList() {
  return (
    <ol className="mt-7 space-y-5">
      {STEPS.map((step, i) => (
        <li key={step.title} className="flex gap-3.5">
          <span
            aria-hidden
            className="mt-px flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-black/15 bg-white text-[12px] font-medium tabular-nums text-black/70"
          >
            {i + 1}
          </span>
          <span className="min-w-0">
            <span className="block text-[14px] font-medium leading-6 text-black">{step.title}</span>
            <span className="mt-0.5 block text-[13.5px] leading-6 text-black/55">{step.body}</span>
          </span>
        </li>
      ))}
    </ol>
  );
}

export function AuthScreen({ mode }: { mode: "signin" | "signup" }) {
  const { openSheet } = useChrome();
  const signUp = mode === "signup";

  // One engagement per time this screen is reached. It moved here when the
  // waitlist dialog was replaced by a page, and it stays now that the waitlist
  // itself is gone: `site.cta_engaged` is a catalogued event and the producer
  // would otherwise have no caller anywhere in the tree. In an effect on mount
  // rather than on the links that lead here, because several routes arrive at
  // this screen and instrumenting each of them is one more place for the next
  // one to be forgotten.
  useEffect(() => {
    ctaEngaged("waitlist_open");
  }, []);


  return (
    // No forced `overflow: hidden` on the document and no `h-dvh` cage. The
    // previous version pinned the page to the viewport height and hid the
    // overflow, which put the submit button below the fold and out of reach on
    // a phone. This scrolls.
    <div className="grid min-h-dvh w-full bg-[#f7f7f5] lg:grid-cols-[2fr_3fr]">
      <AuthCover />
      <div className="relative flex flex-col bg-[#f7f7f5] px-6 py-8 sm:px-8 lg:px-16 max-sm:pb-[max(2rem,env(safe-area-inset-bottom))]">
        <a
          href="/"
          className="inline-flex h-11 w-fit shrink-0 items-center gap-2 text-[13px] text-black/60 hover:text-black"
        >
          <svg viewBox="0 0 16 16" className="h-3.5 w-3.5" fill="none" aria-hidden>
            <path d="M10 3 5 8l5 5" stroke="currentColor" strokeWidth="1.4" />
          </svg>
          Home
        </a>

        <div className="flex flex-1 items-center justify-center py-10 max-sm:py-6">
          <div className="w-full max-w-[400px]">
            <h1 className="text-[32px] font-normal leading-dense tracking-tighter text-black max-sm:text-[28px]">
              {/* The same words as the route title in lib/routes.ts, which is
                  what the tab, the link preview and the markdown twin carry.
                  They differed by one word and the twin check caught it: a
                  reader arriving from a search result should land on the
                  heading they clicked. */}
              {signUp ? "Create an account" : "Sign in"}
            </h1>
            <p className="mt-4 text-[14px] leading-6 text-black/55">
              {signUp
                ? "One button, no card, and no invitation needed. You keep the account whether or not you ever pay us anything."
                : "Continue with the GitHub account you signed up with. You land in the organization you belong to."}
            </p>

            <a
              href={controlPlaneUrl("auth.github")}
              className="mt-6 flex h-12 w-full items-center justify-center gap-2.5 rounded-full bg-black text-[15px] font-medium text-white hover:bg-[#292929]"
            >
              <GitHubMark className="h-[18px] w-[18px]" />
              Continue with GitHub
            </a>

            {signUp ? (
              <>
                <div className="mt-8 flex items-center gap-4" aria-hidden>
                  <span className="h-px flex-1 bg-black/10" />
                  <span className="text-[12px] text-black/55">what happens next</span>
                  <span className="h-px flex-1 bg-black/10" />
                </div>
                <StepList />

                <p className="mt-8 text-[13.5px] leading-6 text-black/55">
                  What the free plan holds, and what it refuses when you reach
                  it, is on the{" "}
                  <a
                    className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
                    href="/pricing"
                  >
                    pricing page
                  </a>
                  . Adding people to your organization is a page in the console
                  once you are in.
                </p>
              </>
            ) : (
              <p className="mt-7 text-[13.5px] leading-6 text-black/55">
                No account yet?{" "}
                <a
                  className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
                  href="/signup"
                >
                  Create one
                </a>
                . It takes the same button and no card. If somebody invited you
                to their organization, open the link they sent instead: it puts
                you in theirs rather than in one of your own.
              </p>
            )}

            {/* The two cases the button above does not serve, named rather
                than left for somebody to guess at. Both are real paths: an
                invitation link is a token the control plane resolves at
                /auth/invitation, and the contact page reaches a person on a
                known day, which is the only route on this site that does. */}
            <div className="mt-9 border-t border-black/10 pt-6">
              <p className="text-[13.5px] leading-6 text-black/55">
                Buying for a team with seats, single sign-on, a security review
                or an agreement to sign?{" "}
                <a
                  className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
                  href="/contact#enterprise"
                >
                  Talk to us
                </a>{" "}
                and a person answers.
              </p>
              <p className="mt-4 text-[13.5px] leading-6 text-black/55">
                The engine itself needs none of this. It is open source, it runs
                entirely on your own machine, and the{" "}
                <a
                  className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
                  href="/docs/getting-started/quickstart"
                >
                  quickstart
                </a>{" "}
                goes from nothing to a working environment without an account.
              </p>
            </div>

            <p className="mt-7 text-[12px] leading-5 text-black/60">
              Signing in stores your GitHub account id, login, name, avatar and
              verified email address, and a session record with your address and
              browser. Nothing on this site asks for a password. See the{" "}
              <button
                type="button"
                className="text-black/70 underline decoration-black/20"
                onClick={() => openSheet("privacy")}
              >
                privacy notice
              </button>
              .
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
