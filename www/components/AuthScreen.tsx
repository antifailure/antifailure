"use client";

import { useEffect, useState } from "react";
import { useChrome } from "./Chrome";
import { LogoMark } from "./icons";
import { joinWaitlist, rememberedEmail } from "@/lib/waitlist";

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
 * goes to the server and is stored. That is the whole product surface, so that
 * is the whole screen.
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
      <div className="relative flex flex-col bg-[#f7f7f5] px-6 py-8 sm:px-8 lg:px-16 max-sm:pb-[max(2rem,env(safe-area-inset-bottom))]">
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
                <h1 className="text-[32px] font-normal leading-dense tracking-tighter text-black max-sm:text-[28px]">
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
                <h1 className="text-[32px] font-normal leading-dense tracking-tighter text-black max-sm:text-[28px]">
                  Join the waitlist
                </h1>
                <p className="mt-4 text-[14px] leading-6 text-black/55">
                  There is no hosted control plane to sign in to yet, so there is
                  nothing here to create an account for. Leave an address and we
                  will tell you when there is.
                </p>
                <p className="mt-3 text-[14px] leading-6 text-black/55">
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

                <label className="mt-8 block text-[13px] text-black/55" htmlFor="email">
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
