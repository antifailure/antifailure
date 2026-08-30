// Signing in.
//
// Two ways, and which one is offered depends on where this is running. GitHub
// is the front door for a person at a laptop. A link to an address is what
// works in a preview environment, where github.com is not reachable by design,
// and on an isolated network, where it is not reachable at all.
//
// The form is a plain form. It posts to a server action, which means it works
// with JavaScript disabled, and it is labelled the way a form is supposed to
// be labelled: a real <label> bound to the input, and a button whose text says
// what pressing it does. That is not only for screen readers. The agents that
// exercise this application find controls by their accessible name, so a page
// whose label is a grey placeholder is a page nothing can sign into, and the
// same is true for a person using a screen reader. One property, two audiences.

import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { LogoMark } from "../../components/Chrome";
import { Field, Failure, inputClass } from "../../components/ui";

export const metadata: Metadata = { title: "Sign in" };
export const dynamic = "force-dynamic";

const API = (process.env.AF_API_URL ?? "http://127.0.0.1:8080").replace(/\/+$/, "");

/** Only a path on this application, for the same reason the API only accepts
 *  one: this value ends up in a redirect that arrives carrying a session. */
function safeNext(value: string | undefined): string {
  if (!value || !value.startsWith("/") || value.startsWith("//") || value.includes("\\")) return "/";
  return value;
}

async function sendLink(formData: FormData): Promise<void> {
  "use server";

  const email = String(formData.get("email") ?? "").trim();
  const next = safeNext(String(formData.get("next") ?? "/"));
  if (!email) redirect(`/login?next=${encodeURIComponent(next)}&problem=empty`);

  let ok = false;
  try {
    const res = await fetch(`${API}/auth/email`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ email, redirect_to: next }),
      cache: "no-store",
      signal: AbortSignal.timeout(10_000),
    });
    // 200 whether or not the address has an account, which is the point: this
    // page must not become a way to ask whether somebody works here. Anything
    // else is our failure and is said plainly.
    ok = res.ok;
  } catch {
    ok = false;
  }

  redirect(
    ok
      ? `/login?next=${encodeURIComponent(next)}&sent=${encodeURIComponent(email)}`
      : `/login?next=${encodeURIComponent(next)}&problem=unreachable`,
  );
}

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const one = (key: string): string | undefined => {
    const value = params[key];
    return Array.isArray(value) ? value[0] : value;
  };

  const next = safeNext(one("next"));
  const sent = one("sent");
  const problem = one("problem");

  return (
    <div className="mesh-grid flex min-h-dvh flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-[404px]">
        <div className="mb-6 flex items-center gap-2.5">
          <LogoMark className="h-[22px] w-[22px]" />
          <span
            className="font-semibold uppercase text-ink"
            style={{ fontSize: 13.5, letterSpacing: "0.12em" }}
          >
            Antifailure
          </span>
        </div>

        <div className="settle rounded-xl border border-hair bg-surface p-5 sm:p-6">
          <h1 className="text-[20px] font-semibold leading-[1.15] tracking-tighter text-ink">
            {sent ? "Check your email" : "Sign in"}
          </h1>
          <p className="mt-1.5 text-[13px] leading-[1.55] text-muted">
            {sent ? (
              <>
                If <span className="text-ink">{sent}</span> belongs to a member of an organization
                here, a link is on its way. It works once and expires in fifteen minutes.
              </>
            ) : (
              "This is the control plane for one organization: its environments, its runs, its egress policy, and its audit log."
            )}
          </p>

          {problem === "unreachable" ? (
            <div className="mt-4">
              <Failure
                title="The control plane did not answer"
                detail="Nothing was sent. This is ours rather than yours: the sign-in service could not be reached. Try again in a moment."
              />
            </div>
          ) : null}

          {sent ? (
            <div className="mt-5 flex flex-col gap-3">
              <p className="text-[13px] leading-[1.55] text-muted">
                No link? The address may not belong to a member here. Ask somebody who is one to
                invite you, then try again.
              </p>
              <a
                href={`/login?next=${encodeURIComponent(next)}`}
                className="text-[13px] font-medium tracking-snug text-ink underline underline-offset-4 hover:no-underline"
              >
                Use a different address
              </a>
            </div>
          ) : (
            <>
              <form action={sendLink} className="mt-5 flex flex-col gap-3">
                <input type="hidden" name="next" value={next} />
                <Field
                  label="Email address"
                  htmlFor="email"
                  hint="A link is sent here. There is no password and no sign-up: an address works once somebody has invited it."
                >
                  <input
                    id="email"
                    name="email"
                    type="email"
                    autoComplete="email"
                    autoFocus
                    required
                    spellCheck={false}
                    placeholder="you@company.com"
                    className={inputClass}
                    {...(problem === "empty" ? { "aria-invalid": true } : {})}
                  />
                </Field>
                <button
                  type="submit"
                  className="inline-flex h-9 items-center justify-center rounded-lg bg-ink px-3 text-[13.5px] font-medium tracking-snug text-white transition-colors hover:bg-[#1c1c1c] active:translate-y-px"
                >
                  Send link
                </button>
              </form>

              <div className="my-5 flex items-center gap-3">
                <span className="h-px flex-1 bg-hair" />
                <span className="text-[11.5px] uppercase tracking-[0.08em] text-faint">or</span>
                <span className="h-px flex-1 bg-hair" />
              </div>

              <a
                href={`/auth/github?redirect_to=${encodeURIComponent(next)}`}
                className="inline-flex h-9 w-full items-center justify-center gap-2 rounded-lg border border-edge bg-surface px-3 text-[13.5px] font-medium tracking-snug text-ink transition-colors hover:bg-sunken"
              >
                <svg viewBox="0 0 24 24" className="h-4 w-4" fill="currentColor" aria-hidden>
                  <path d="M12 .5A11.5 11.5 0 0 0 .5 12a11.5 11.5 0 0 0 7.86 10.92c.58.1.79-.25.79-.56v-2c-3.2.7-3.88-1.37-3.88-1.37-.53-1.34-1.3-1.7-1.3-1.7-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.2 1.77 1.2 1.03 1.77 2.7 1.26 3.36.96.1-.75.4-1.26.73-1.55-2.55-.29-5.24-1.28-5.24-5.7 0-1.26.45-2.29 1.2-3.1-.13-.29-.53-1.47.1-3.06 0 0 .97-.31 3.18 1.18a11 11 0 0 1 5.8 0c2.2-1.5 3.17-1.18 3.17-1.18.63 1.59.23 2.77.12 3.06.75.81 1.2 1.84 1.2 3.1 0 4.43-2.7 5.4-5.27 5.69.42.36.78 1.06.78 2.13v3.16c0 .31.21.67.8.56A11.5 11.5 0 0 0 23.5 12 11.5 11.5 0 0 0 12 .5Z" />
                </svg>
                Continue with GitHub
              </a>
              <p className="mt-3 text-[12px] leading-[1.5] text-faint">
                GitHub needs a route to github.com. A preview environment has none, which is what
                the link above is for.
              </p>
            </>
          )}
        </div>

        <p className="mt-4 px-1 text-[12px] leading-[1.5] text-faint">
          Signing in does not give this application access to your data. It reads what your
          engines have already sent it.
        </p>
      </div>
    </div>
  );
}
