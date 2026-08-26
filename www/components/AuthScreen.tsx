"use client";

import { useEffect, useState, type ReactNode } from "react";
import { signIn } from "next-auth/react";
import { useChrome } from "./Chrome";
import { LogoMark } from "./icons";

const WAITLIST_KEY = "wt-waitlist";

export type AuthProviderId = "github" | "google" | "microsoft";
export type ConfiguredProviders = Record<AuthProviderId, boolean>;

function writeWaitlist(email: string) {
  localStorage.setItem(WAITLIST_KEY, JSON.stringify({ email, at: Date.now() }));
}

function AuthCover() {
  return (
    <div className="relative hidden h-full overflow-hidden bg-[#050505] lg:block">
      <div
        className="absolute inset-0"
        style={{
          background:
            "radial-gradient(ellipse 90% 70% at 6% 4%, rgba(32, 196, 180, 0.42) 0%, transparent 52%), radial-gradient(ellipse 85% 70% at 96% 98%, rgba(196, 96, 36, 0.55) 0%, transparent 54%)",
        }}
      />
      <div className="auth-honeycomb absolute inset-0 opacity-80" />
      <div className="absolute inset-0 bg-black/25" />
      <div className="relative z-10 flex h-full flex-col items-center justify-center px-16 text-center">
        <LogoMark className="h-14 w-14" />
        <p className="mt-8 max-w-[320px] text-[32px] font-semibold leading-[1.15] tracking-[-0.035em] text-white">
          Know what happens before you deploy.
        </p>
      </div>
    </div>
  );
}

function GitHubGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="h-5 w-5" fill="currentColor" aria-hidden>
      <path d="M12 .3a12 12 0 0 0-3.8 23.4c.6.1.8-.26.8-.58v-2.02c-3.34.72-4.04-1.61-4.04-1.61c-.55-1.39-1.34-1.76-1.34-1.76c-1.09-.75.08-.73.08-.73c1.2.08 1.84 1.24 1.84 1.24c1.07 1.83 2.81 1.3 3.5 1c.1-.78.42-1.3.76-1.6c-2.67-.3-5.47-1.33-5.47-5.93c0-1.31.47-2.38 1.24-3.22c-.13-.3-.54-1.52.12-3.18c0 0 1.01-.32 3.3 1.23a11.5 11.5 0 0 1 6 0c2.29-1.55 3.3-1.23 3.3-1.23c.66 1.66.25 2.88.12 3.18c.77.84 1.24 1.91 1.24 3.22c0 4.61-2.81 5.63-5.48 5.92c.43.37.81 1.1.81 2.22v3.29c0 .32.22.69.82.58A12 12 0 0 0 12 .3z" />
    </svg>
  );
}

function GoogleGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="h-5 w-5" aria-hidden>
      <path fill="#EA4335" d="M12 10.2v3.9h5.5c-.24 1.26-1.7 3.7-5.5 3.7c-3.31 0-6-2.74-6-6.1S8.69 5.6 12 5.6c1.89 0 3.16.8 3.88 1.5l2.64-2.55C16.9 2.95 14.7 2 12 2C6.48 2 2 6.48 2 12s4.48 10 10 10c5.79 0 9.6-4.07 9.6-9.8c0-.66-.07-1.16-.16-1.66H12Z" />
    </svg>
  );
}

function MicrosoftGlyph() {
  return (
    <svg viewBox="0 0 21 21" className="h-4 w-4" aria-hidden>
      <path fill="#F25022" d="M1 1h9v9H1z" />
      <path fill="#7FBA00" d="M11 1h9v9h-9z" />
      <path fill="#00A4EF" d="M1 11h9v9H1z" />
      <path fill="#FFB900" d="M11 11h9v9h-9z" />
    </svg>
  );
}

const SOCIAL: { id: AuthProviderId; authId: string; label: string; Icon: () => ReactNode }[] = [
  { id: "google", authId: "google", label: "Google", Icon: GoogleGlyph },
  { id: "github", authId: "github", label: "GitHub", Icon: GitHubGlyph },
  { id: "microsoft", authId: "microsoft-entra-id", label: "Microsoft", Icon: MicrosoftGlyph },
];

export function AuthScreen({
  mode,
  configured,
  sessionEmail,
  oauthError,
}: {
  mode: "signin" | "signup";
  configured: ConfiguredProviders;
  sessionEmail: string | null;
  oauthError?: string | null;
}) {
  const { openSheet } = useChrome();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState(oauthError ?? "");
  const [done, setDone] = useState<string | null>(sessionEmail);
  const [busy, setBusy] = useState<string | null>(null);
  const [unset, setUnset] = useState<AuthProviderId | null>(null);

  useEffect(() => {
    if (!sessionEmail) return;
    writeWaitlist(sessionEmail);
    setDone(sessionEmail);
  }, [sessionEmail]);

  useEffect(() => {
    const html = document.documentElement;
    const body = document.body;
    const prev = {
      htmlOverflow: html.style.overflow,
      bodyOverflow: body.style.overflow,
      htmlHeight: html.style.height,
      bodyHeight: body.style.height,
      htmlOverscroll: html.style.overscrollBehavior,
      bodyOverscroll: body.style.overscrollBehavior,
    };
    html.style.overflow = "hidden";
    body.style.overflow = "hidden";
    html.style.height = "100%";
    body.style.height = "100%";
    html.style.overscrollBehavior = "none";
    body.style.overscrollBehavior = "none";
    return () => {
      html.style.overflow = prev.htmlOverflow;
      body.style.overflow = prev.bodyOverflow;
      html.style.height = prev.htmlHeight;
      body.style.height = prev.bodyHeight;
      html.style.overscrollBehavior = prev.htmlOverscroll;
      body.style.overscrollBehavior = prev.bodyOverscroll;
    };
  }, []);

  const submitWaitlist = (e: React.FormEvent) => {
    e.preventDefault();
    const value = email.trim().toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)) {
      setError("Enter a work email.");
      return;
    }
    writeWaitlist(value);
    setPassword("");
    setDone(value);
    setError("");
  };

  const startOAuth = async (id: AuthProviderId, authId: string) => {
    setUnset(null);
    setError("");
    if (!configured[id]) {
      setUnset(id);
      return;
    }
    setBusy(id);
    await signIn(authId, { callbackUrl: `${mode === "signup" ? "/signup" : "/signin"}?joined=1` });
  };

  return (
    <div className="grid h-dvh max-h-dvh w-full overflow-hidden bg-black lg:grid-cols-[2fr_3fr]">
      <AuthCover />
      <div className="relative flex h-full min-h-0 flex-col overflow-hidden bg-black px-8 py-8 lg:px-16">
        <a href="/" className="inline-flex shrink-0 items-center gap-2 text-[13px] text-white/45 hover:text-white">
          <svg viewBox="0 0 16 16" className="h-3.5 w-3.5" fill="none" aria-hidden>
            <path d="M10 3 5 8l5 5" stroke="currentColor" strokeWidth="1.4" />
          </svg>
          Home
        </a>

        <div className="flex min-h-0 flex-1 items-center justify-center overflow-hidden py-4">
          <div className="w-full max-w-[400px]">
            {done ? (
              <>
                <h1 className="text-[32px] font-semibold tracking-[-0.035em] text-white">
                  You’re on the waitlist
                </h1>
                <p className="mt-3 text-[14px] leading-6 text-white/50">
                  We’ll email {done} when the control plane can connect a repo.
                </p>
                <a
                  href="/"
                  className="mt-8 flex h-12 w-full items-center justify-center rounded-md border border-white/20 text-[15px] text-white hover:bg-white/5"
                >
                  Back to home
                </a>
              </>
            ) : (
              <form onSubmit={submitWaitlist}>
                <h1 className="text-[32px] font-semibold tracking-[-0.035em] text-white">
                  {mode === "signup" ? "Create your free account" : "Log in to your account"}
                </h1>
                <p className="mt-6 text-[13px] text-white/45">Connect to Antifailure with:</p>

                <div className="mt-4 space-y-2.5">
                  {SOCIAL.map(({ id, authId, label, Icon }) => (
                    <button
                      key={id}
                      type="button"
                      onClick={() => startOAuth(id, authId)}
                      className="flex h-12 w-full items-center justify-center gap-2.5 rounded-md border border-white/15 bg-transparent text-[15px] text-white hover:bg-white/[0.06]"
                    >
                      <Icon />
                      {busy === id ? `${label}…` : label}
                    </button>
                  ))}
                </div>
                {unset ? (
                  <p className="mt-3 text-[12px] text-white/45" role="status">
                    {unset === "github" ? "GitHub" : unset === "google" ? "Google" : "Microsoft"} is
                    not set up yet. Add the app keys to .env.local — see .env.example.
                  </p>
                ) : null}

                <div className="my-7 flex items-center gap-3">
                  <span className="h-px flex-1 bg-white/12" />
                  <span className="text-[12px] text-white/40">Or continue with Email</span>
                  <span className="h-px flex-1 bg-white/12" />
                </div>

                <label className="block text-[13px] text-white/55" htmlFor="email">
                  Email
                </label>
                <input
                  id="email"
                  type="email"
                  value={email}
                  placeholder="youremail@email.com"
                  autoComplete="email"
                  onChange={(e) => {
                    setEmail(e.target.value);
                    setError("");
                  }}
                  className="mt-1.5 h-12 w-full rounded-md border border-white/15 bg-transparent px-3 text-[14px] text-white outline-none placeholder:text-white/30 focus:border-white/35"
                />

                <label className="mt-4 block text-[13px] text-white/55" htmlFor="password">
                  Password
                </label>
                <div className="relative mt-1.5">
                  <input
                    id="password"
                    type={showPassword ? "text" : "password"}
                    value={password}
                    placeholder="Enter a unique password"
                    autoComplete={mode === "signup" ? "new-password" : "current-password"}
                    onChange={(e) => setPassword(e.target.value)}
                    className="h-12 w-full rounded-md border border-white/15 bg-transparent px-3 pr-11 text-[14px] text-white outline-none placeholder:text-white/30 focus:border-white/35"
                  />
                  <button
                    type="button"
                    className="absolute inset-y-0 right-0 px-3 text-white/40 hover:text-white/70"
                    onClick={() => setShowPassword((v) => !v)}
                    aria-label={showPassword ? "Hide password" : "Show password"}
                  >
                    <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" aria-hidden>
                      {showPassword ? (
                        <path
                          d="M3 3l18 18M10.5 10.7a2.5 2.5 0 0 0 3.5 3.5M9.9 5.1A10 10 0 0 1 12 5c5.2 0 9.3 3.5 10.5 7c-.4 1.2-1.1 2.4-2 3.4M6.1 6.4C4.2 7.7 2.7 9.4 1.5 12c1.2 3.5 5.3 7 10.5 7c1.4 0 2.7-.2 3.9-.7"
                          stroke="currentColor"
                          strokeWidth="1.5"
                        />
                      ) : (
                        <>
                          <path
                            d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"
                            stroke="currentColor"
                            strokeWidth="1.5"
                          />
                          <circle cx="12" cy="12" r="2.5" stroke="currentColor" strokeWidth="1.5" />
                        </>
                      )}
                    </svg>
                  </button>
                </div>

                {error ? (
                  <p className="mt-3 text-[13px] text-red-400" role="alert">
                    {error}
                  </p>
                ) : null}

                <button
                  type="submit"
                  className="mt-6 h-12 w-full rounded-md border border-white/20 text-[15px] text-white hover:bg-white/5"
                >
                  Continue
                </button>

                {mode === "signup" ? (
                  <p className="mt-5 text-[12px] leading-5 text-white/40">
                    By creating an account you agree to the{" "}
                    <button
                      type="button"
                      className="text-white/70 underline decoration-white/20"
                      onClick={() => openSheet("terms")}
                    >
                      Terms of Service
                    </button>{" "}
                    and our{" "}
                    <button
                      type="button"
                      className="text-white/70 underline decoration-white/20"
                      onClick={() => openSheet("privacy")}
                    >
                      Privacy Policy
                    </button>
                    . We’ll occasionally send you emails about news, products, and services; you can
                    opt-out anytime.
                  </p>
                ) : null}

                <p className="mt-8 text-[14px] text-white/50">
                  {mode === "signup" ? (
                    <>
                      Already have an account?{" "}
                      <a href="/signin" className="text-[#3b82f6] hover:underline">
                        Log in
                      </a>
                    </>
                  ) : (
                    <>
                      Don’t have an account?{" "}
                      <a href="/signup" className="text-[#3b82f6] hover:underline">
                        Sign up
                      </a>
                    </>
                  )}
                </p>
              </form>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
