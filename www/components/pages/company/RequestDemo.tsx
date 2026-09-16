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
 * WHY IT FITS ONE SCREEN. The first version wore the login screen's split
 * shell but not its restraint: a tall intro, a card with generous padding and
 * a two-paragraph footer stacked past the fold, so the page scrolled where the
 * login page sat still. This version keeps every field the sales team asks for
 * and instead spends less on rhythm: a tighter intro, a denser two-column grid
 * inside a compact card, and a single terse footer line. At the laptop heights
 * people here actually use, 800 through 982 tall, the whole page holds inside
 * the viewport with no page scroll, the way the login page does.
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
    body: "Every verdict arrives with the rows it read, the trace it took and a recording of the attempt.",
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
      <div className="relative z-10 flex h-full flex-col justify-center px-16 py-12 xl:px-20">
        <LogoMark className="h-11 w-11" />
        <p className="mt-7 max-w-[420px] text-[34px] font-normal leading-dense tracking-tighter text-black xl:text-[38px]">
          Know what happens before you deploy.
        </p>
        <ul className="mt-10 max-w-[440px] space-y-6">
          {POINTS.map((point) => (
            <li key={point.title} className="border-l border-black/12 pl-5">
              <h2 className="text-[16px] leading-snug tracking-extra-tight text-black">
                {point.title}
              </h2>
              <p className="mt-1 text-[13.5px] leading-6 tracking-extra-tight text-gray-new-40">
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
    // column. min-h-dvh sets the floor without caging the page, so a phone
    // with more fields than a laptop viewport can hold still scrolls rather
    // than clipping the submit button, while a laptop holds the whole thing.
    <div className="grid min-h-dvh w-full bg-[#f7f7f5] lg:grid-cols-[2fr_3fr]">
      <DemoCover />
      <div className="relative flex flex-col bg-[#f7f7f5] px-6 py-6 sm:px-8 lg:px-14 max-sm:pb-[max(1.5rem,env(safe-area-inset-bottom))]">
        <a
          href="/"
          className="inline-flex h-9 w-fit shrink-0 items-center gap-2 text-[13px] text-black/60 hover:text-black"
        >
          <svg viewBox="0 0 16 16" className="h-3.5 w-3.5" fill="none" aria-hidden>
            <path d="M10 3 5 8l5 5" stroke="currentColor" strokeWidth="1.4" />
          </svg>
          Home
        </a>

        <div className="flex flex-1 flex-col justify-center py-4 max-sm:py-3">
          <div className="w-full max-w-[600px]">
            {/* The same headline as the left cover would be a repeat on a phone,
                where the cover is hidden, so this one names the action. */}
            <h1 className="text-[28px] font-normal leading-dense tracking-tighter text-black max-sm:text-[26px]">
              Request a demo
            </h1>
            <p className="mt-2.5 max-w-[540px] text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
              Tell us who you are and what you run. A person reads every request
              and replies to set up a walkthrough on a deployment your team is
              nervous about.
            </p>

            <div className="mt-5">
              <DemoRequestForm />
            </div>

            <p className="mt-5 text-[13px] leading-6 text-gray-new-40">
              Already have an organization?{" "}
              <a
                className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
                href="/signin"
              >
                Sign in with GitHub
              </a>
              . The engine itself is open source and its{" "}
              <a
                className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
                href="/docs/getting-started/quickstart"
              >
                quickstart
              </a>{" "}
              needs no account at all.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
