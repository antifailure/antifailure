// The two whole-page states that are not a page.
//
// Both exist because the alternative is a page that looks broken. Somebody
// whose session is scoped to no organization sees every table empty, and
// somebody looking at this while the API restarts sees the same. Neither is an
// empty organization, and saying so is the difference between a support ticket
// and a refresh.

import { LinkButton } from "./ui";
import { LogoMark } from "./Chrome";

function Sheet({
  title,
  children,
  action,
}: {
  title: string;
  children: React.ReactNode;
  action?: React.ReactNode;
}) {
  return (
    <div className="mesh-grid flex min-h-dvh flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-[440px]">
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
            {title}
          </h1>
          <div className="mt-2 flex flex-col gap-2 text-[13px] leading-[1.6] text-muted">
            {children}
          </div>
          {action ? <div className="mt-5">{action}</div> : null}
        </div>
      </div>
    </div>
  );
}

/** Signed in, and in no organization. Every query would return nothing. */
export function NoOrganization() {
  return (
    <Sheet
      title="Your session is not in an organization"
      action={
        <form action="/signout" method="post">
          <button
            type="submit"
            className="inline-flex h-9 items-center rounded-lg border border-edge bg-surface px-3 text-[13.5px] font-medium tracking-snug text-ink transition-colors hover:bg-sunken"
          >
            Sign out and try again
          </button>
        </form>
      }
    >
      <p>
        You are signed in, and this session is not scoped to one organization, so there is nothing
        to show. Every page here reads one tenant&rsquo;s rows and yours is unset.
      </p>
      <p>
        That happens for two reasons: nobody has added you to an organization yet, or you belong to
        several and none was chosen for you. Guessing one would put you somewhere you did not ask to
        be, where the pages are empty for a reason nobody can see.
      </p>
    </Sheet>
  );
}

/** The API did not answer. Deliberately not the sign-in page. */
export function Unreachable({ detail }: { detail: string }) {
  return (
    <Sheet title="The control plane did not answer" action={<LinkButton href="/">Try again</LinkButton>}>
      <p>
        This is ours rather than yours. Your session is untouched: you have not been signed out, and
        nothing you were looking at has changed.
      </p>
      <p className="font-mono text-[12.5px] text-faint">{detail}</p>
    </Sheet>
  );
}
