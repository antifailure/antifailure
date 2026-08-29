"use client";

import { useEffect, useState } from "react";
import { useChrome } from "./Chrome";
import { LogoMark } from "./icons";
import { joinWaitlist, rememberedEmail } from "@/lib/waitlist";

/**
 * Where the hosted control plane is.
 *
 * A constant rather than a build-time variable, because this is a static export
 * and an unset NEXT_PUBLIC_ variable would silently produce a link to
 * "undefined/auth/github" in the published site.
 */
const CONTROL_PLANE = "https://app.dev.antifailure.dev";

function GitHubMark({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
    </svg>
  );
}

/**
 * There is no account system yet, so this screen does not pretend there is one.
 *
 * It previously rendered an email field, a password field with a show/hide
 * toggle and `autocomplete="new-password"`, and a Continue button. No password
 * was ever checked, stored or sent: the submit handler called `setPassword("")`
 * and dropped it. The only configured sign-in was OAuth, which succeeded and
 * then landed the visitor on a page saying they were on a waitlist. So the
 * field taught browsers to offer to save a credential for a site that had no
 * account to attach it to, and people reuse passwords.
 *
 * What is real is the waitlist, and it is now actually a waitlist: the address
 * goes to the server and is stored.
 *
 * There is now also a hosted control plane, invitation only, so the screen
 * offers both: sign in with GitHub if you have been invited, join the waitlist
 * if you have not. The page said "there is no hosted control plane to sign in
 * to yet" for as long as there was one, which reads to an invited person as
 * being turned away.
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

export function AuthScreen({ mode }: { mode: "signin" | "signup" }) {
  const { openSheet } = useChrome();
  const [email, setEmail] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState<string | null>(null);
  const [already, setAlready] = useState(false);

  useEffect(() => {
    const known = rememberedEmail();
    if (known) {
      setDone(known);
      setAlready(true);
    }
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    const result = await joinWaitlist(email, mode);
    setBusy(false);
    if (!result.ok) {
      setError(result.message);
      return;
    }
    setAlready(result.alreadyJoined);
    setDone(result.email);
  };

  return (
    // No forced `overflow: hidden` on the document and no `h-dvh` cage. The
    // previous version pinned the page to the viewport height and hid the
    // overflow, which put the submit button below the fold and out of reach on
    // a phone. This scrolls.
    <div className="grid min-h-dvh w-full bg-[#f7f7f5] lg:grid-cols-[2fr_3fr]">
      <AuthCover />
      <div className="relative flex flex-col bg-[#f7f7f5] px-6 py-8 sm:px-8 lg:px-16">
        <a
          href="/"
          className="inline-flex w-fit shrink-0 items-center gap-2 text-[13px] text-black/45 hover:text-black"
        >
          <svg viewBox="0 0 16 16" className="h-3.5 w-3.5" fill="none" aria-hidden>
            <path d="M10 3 5 8l5 5" stroke="currentColor" strokeWidth="1.4" />
          </svg>
          Home
        </a>

        <div className="flex flex-1 items-center justify-center py-10">
          <div className="w-full max-w-[400px]">
            {done ? (
              <>
                <h1 className="text-[32px] font-normal leading-dense tracking-tighter text-black">
                  {already ? "You are already on the list" : "You are on the list"}
                </h1>
                <p className="mt-4 text-[14px] leading-6 text-black/55">
                  We have {done}. You will hear from us when there is an
                  environment you can connect a repository to, and not before.
                  No newsletter.
                </p>
                <p className="mt-4 text-[14px] leading-6 text-black/55">
                  In the meantime the engine is open source and runs entirely on
                  your own machine. The{" "}
                  <a
                    className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
                    href="/docs/getting-started/quickstart"
                  >
                    quickstart
                  </a>{" "}
                  goes from nothing to a working environment without an account.
                </p>
                <a
                  href="/docs"
                  className="mt-8 flex h-12 w-full items-center justify-center rounded-full bg-black text-[15px] font-medium text-white hover:bg-[#292929]"
                >
                  Read the documentation
                </a>
              </>
            ) : (
              <form onSubmit={submit} noValidate>
                <h1 className="text-[32px] font-normal leading-dense tracking-tighter text-black">
                  Sign in
                </h1>
                <p className="mt-4 text-[14px] leading-6 text-black/55">
                  The hosted control plane is invitation only while it is in
                  development. If your account has been invited, sign in with
                  GitHub. If it has not, leave an address below and we will tell
                  you when it opens.
                </p>

                <a
                  href={CONTROL_PLANE + "/auth/github"}
                  className="mt-6 flex h-12 w-full items-center justify-center gap-2.5 rounded-full bg-black text-[15px] font-medium text-white hover:bg-[#292929]"
                >
                  <GitHubMark className="h-[18px] w-[18px]" />
                  Continue with GitHub
                </a>

                <div className="mt-7 flex items-center gap-4" aria-hidden>
                  <span className="h-px flex-1 bg-black/10" />
                  <span className="text-[12px] text-black/40">
                    not invited yet
                  </span>
                  <span className="h-px flex-1 bg-black/10" />
                </div>
                <p className="mt-6 text-[14px] leading-6 text-black/55">
                  The engine itself needs none of this. It is open source, it
                  runs locally, and the{" "}
                  <a
                    className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
                    href="/docs/getting-started/quickstart"
                  >
                    quickstart
                  </a>{" "}
                  works today.
                </p>

                <label className="mt-7 block text-[13px] text-black/55" htmlFor="email">
                  Email
                </label>
                <input
                  id="email"
                  type="email"
                  value={email}
                  placeholder="you@company.com"
                  autoComplete="email"
                  required
                  aria-invalid={error ? true : undefined}
                  aria-describedby={error ? "waitlist-error" : undefined}
                  onChange={(e) => {
                    setEmail(e.target.value);
                    setError("");
                  }}
                  className="mt-1.5 h-12 w-full rounded-md border border-black/15 bg-white px-3 text-[14px] text-black outline-none placeholder:text-black/30 focus:border-black/35"
                />

                {error ? (
                  <p id="waitlist-error" className="mt-3 text-[13px] text-[#b32d18]" role="alert">
                    {error}
                  </p>
                ) : null}

                <button
                  type="submit"
                  disabled={busy}
                  className="mt-6 h-12 w-full rounded-full bg-black text-[15px] font-medium text-white hover:bg-[#292929] disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {busy ? "Adding you" : "Join the waitlist"}
                </button>

                <p className="mt-5 text-[12px] leading-5 text-black/45">
                  We store the address and nothing else. It is used to tell you
                  when the hosted product exists. See the{" "}
                  <button
                    type="button"
                    className="text-black/70 underline decoration-black/20"
                    onClick={() => openSheet("privacy")}
                  >
                    privacy notice
                  </button>
                  .
                </p>
              </form>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
