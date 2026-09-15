import { LogoMark } from "@/components/icons";
import { DemoRequestForm } from "./DemoRequestForm";

/**
 * The demo request page, which is where the site now sends everyone who used
 * to be pointed at self-serve sign-up.
 *
 * WHY THIS PAGE EXISTS. The hosted control plane no longer opens itself to a
 * stranger: `AF_SELF_SERVE_SIGNUP` is off, so a GitHub exchange creates no
 * organization on its own, and a "Start free" button that promised one was
 * describing a door that does not open. Every self-serve sign-up call to
 * action across the site now leads here instead, and existing operators keep
 * signing in with GitHub at /signin.
 *
 * WHY IT IS A FORM AND NOT A CALENDAR. Booking a call lives on /contact, where
 * cal.com already runs, and it stays the one place on the site that reaches a
 * person on a known day. This page is the other half: the Harvey-style request
 * form a sales team reads, so somebody who is not ready to pick a slot can
 * still say who they are and what they run. It writes a lead the same way the
 * enterprise form does, into the product's own database, and a person answers
 * it.
 *
 * WHY THERE IS NO LOGO WALL. The site carries no customer logos anywhere, its
 * own brief says "measurable evidence, or no claim", and a strip of borrowed
 * marks with no relationship behind them is the one thing that would make this
 * page read as generated rather than owned. The left panel carries the pitch
 * and three claims the product actually makes instead.
 */

/** The three claims, drawn from the product brief and the trust model already
 *  on the site. Each is a thing the product does, not a superlative: no
 *  "world-class", no invented number, no borrowed logo. */
const POINTS = [
  {
    title: "A disposable production twin",
    body: "Every risky change runs against an isolated, production-shaped copy, then the copy is torn down.",
  },
  {
    title: "Fail closed, customer-hosted",
    body: "Production data stays inside your boundary. An unverified golden cannot be branched, and the twin has no route out.",
  },
  {
    title: "Evidence, not a green checkmark",
    body: "Every verdict arrives with the rows it read, the trace it took and a recording of the attempt, so a reviewer can check it.",
  },
];

function DemoCover() {
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
      <div className="relative z-10 flex h-full flex-col justify-center px-16 py-20 xl:px-20">
        <LogoMark className="h-12 w-12" />
        <p className="mt-8 max-w-[420px] text-[36px] font-normal leading-dense tracking-tighter text-black xl:text-[40px]">
          Know what happens before you deploy.
        </p>
        <ul className="mt-14 max-w-[440px] space-y-8">
          {POINTS.map((point) => (
            <li key={point.title} className="border-l border-black/12 pl-5">
              <h2 className="text-[17px] leading-snug tracking-extra-tight text-black">
                {point.title}
              </h2>
              <p className="mt-1.5 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
                {point.body}
              </p>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

export function RequestDemo() {
  return (
    // The same split shell as the sign-in screen: a 2fr cover and a 3fr
    // column, no forced viewport height, so the form scrolls rather than being
    // trapped below the fold on a phone.
    <div className="grid min-h-dvh w-full bg-[#f7f7f5] lg:grid-cols-[2fr_3fr]">
      <DemoCover />
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

        <div className="flex flex-1 flex-col justify-center py-10 max-sm:py-6">
          <div className="w-full max-w-[640px]">
            {/* The same headline as the left cover would be a repeat on a phone,
                where the cover is hidden, so this one names the action. */}
            <h1 className="text-[32px] font-normal leading-dense tracking-tighter text-black max-sm:text-[28px]">
              Request a demo
            </h1>
            <p className="mt-4 max-w-[540px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
              Tell us who you are and what you run, and we set up a walkthrough
              on a deployment your team is nervous about. A person reads every
              request and replies to arrange a time.
            </p>

            <div className="mt-9">
              <DemoRequestForm />
            </div>

            <div className="mt-10 border-t border-black/10 pt-6">
              <p className="text-[13.5px] leading-6 text-gray-new-40">
                Already have an organization?{" "}
                <a
                  className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
                  href="/signin"
                >
                  Sign in with GitHub
                </a>{" "}
                and you land in the one you belong to.
              </p>
              <p className="mt-4 text-[13.5px] leading-6 text-gray-new-40">
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
          </div>
        </div>
      </div>
    </div>
  );
}
