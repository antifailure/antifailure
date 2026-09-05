"use client";

import { useEffect, useId, useRef, useState } from "react";
import { controlPlaneUrl } from "@/lib/control-plane-routes";

/**
 * The application form, and the four things it refuses to get wrong.
 *
 * IT NEVER SAYS "SENT" UNLESS A ROW EXISTS. The control plane answers 201 with
 * the id it wrote and the submission key it was given. Both are checked here
 * before the confirmation replaces the form, so a 200 from a proxy, a truncated
 * body, or a response belonging to an earlier attempt all land in the failure
 * branch with the answers still on screen. The failure that shape prevents is
 * the one the enterprise form was built to end: a cheerful confirmation over a
 * write that did not happen. A candidate who is told their application is in a
 * queue does not apply again.
 *
 * A RETRY OF THE SAME ANSWERS IS NOT A SECOND APPLICATION. The submission key
 * is generated once per distinct payload and reused for every retry of it, and
 * the server derives the row's primary key from the key and the answers
 * together. So pressing the button again after a timeout cannot create a
 * duplicate, and EDITING an answer and pressing again cannot be silently
 * swallowed as a duplicate of the old one: a changed payload is a new key, a
 * new row, and a real acknowledgment of what was actually recorded.
 *
 * THE ANSWERS SURVIVE EVERY FAILURE. Nothing is reset on a refusal and nothing
 * is cleared on a network error. Every text field is uncontrolled, the chosen
 * role is held in state that no failure branch touches, and the fieldset is
 * disabled only while a request is in flight, so a failed submission leaves a
 * filled-in form and a button that says to try again. Making somebody retype an
 * introduction is how a retry becomes an abandonment.
 *
 * THE TWO ROLE IDS ARE FIXED, not `useId` values, because /careers links at
 * them: the role cards above point at `#apply-founding_engineer` and
 * `#apply-founding_growth`. The fragment gives the browser a real focusable
 * target, and the effect below reads the same fragment and selects that role,
 * so following the link chooses what the link said it would. Every other field
 * here takes a generated id, which is what the rest of the site's forms do.
 */

const ROLES = [
  { value: "founding_engineer", label: "Founding engineer" },
  { value: "founding_growth", label: "Founding growth" },
] as const;

type Role = (typeof ROLES)[number]["value"];

/** The fragment a role card links at, for the one role it names. */
const ANCHOR: Record<string, Role> = {
  "apply-founding_engineer": "founding_engineer",
  "apply-founding_growth": "founding_growth",
};

type State =
  | { kind: "idle" }
  | { kind: "sending" }
  | { kind: "failed"; message: string }
  | { kind: "sent"; reference: string };

const FIELD =
  "mt-1.5 h-11 w-full rounded-[8px] border border-black/15 bg-white px-3 text-[15px] text-black outline-none placeholder:text-black/40 focus-visible:border-black/45 focus-visible:ring-2 focus-visible:ring-black/10 disabled:opacity-60";
const LABEL = "block text-[13px] tracking-extra-tight text-gray-new-40";

export function ApplicationForm() {
  const [state, setState] = useState<State>({ kind: "idle" });
  const [role, setRole] = useState<Role | "">("");
  const id = useId();

  const sending = state.kind === "sending";

  // The role a card above linked at. Read on mount and again on every
  // fragment change, because a reader who is already on this page and presses
  // the other role's card gets a hashchange and no remount.
  useEffect(() => {
    const fromFragment = () => {
      const chosen = ANCHOR[window.location.hash.replace(/^#/, "")];
      if (chosen) setRole(chosen);
    };
    fromFragment();
    window.addEventListener("hashchange", fromFragment);
    return () => window.removeEventListener("hashchange", fromFragment);
  }, []);

  // One key per distinct set of answers. A ref rather than state, because it
  // must not depend on a rerender having happened: two submissions in one tick
  // would otherwise both read the initial value and mint two keys for one set
  // of answers, which is the duplicate this exists to prevent.
  const attempt = useRef<{ payload: string; key: string } | null>(null);
  const inFlight = useRef(false);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    // The ref rather than `sending`, for the same reason: the state behind
    // `sending` is one render behind a submission that has just started.
    if (inFlight.current) return;
    const data = new FormData(event.currentTarget);
    const values = {
      name: String(data.get("name") ?? ""),
      email: String(data.get("email") ?? ""),
      role: String(data.get("role") ?? ""),
      projectUrl: String(data.get("projectUrl") ?? ""),
      why: String(data.get("why") ?? ""),
      compensationAcknowledged: data.get("compensation") === "on",
      website: String(data.get("website") ?? ""),
    };
    const payload = JSON.stringify(values);
    if (attempt.current?.payload !== payload) {
      attempt.current = { payload, key: crypto.randomUUID() };
    }
    const key = attempt.current.key;
    inFlight.current = true;
    setState({ kind: "sending" });
    try {
      await send(values, key);
    } finally {
      // In `finally` and nowhere else. Clearing it after the fetch resolves but
      // before the body is read leaves a window in which a second press starts
      // a second request while this one is still deciding what happened.
      inFlight.current = false;
    }
  }

  async function send(values: Record<string, unknown>, key: string) {
    let response: Response;
    try {
      response = await fetch(controlPlaneUrl("applications.create"), {
        method: "POST",
        // No cookie leaves this browser for the control plane. The endpoint is
        // anonymous, and sending credentials to it would be the only reason it
        // needed a credentialed CORS policy.
        credentials: "omit",
        headers: { "content-type": "application/json" },
        signal: AbortSignal.timeout(20000),
        body: JSON.stringify({ ...values, submissionId: key }),
      });
    } catch {
      setState({
        kind: "failed",
        message:
          "Could not reach the server. Nothing you typed is lost, and pressing it again with the same answers cannot create a duplicate.",
      });
      return;
    }

    if (response.status === 429) {
      setState({
        kind: "failed",
        message: "That was a lot of attempts at once. Wait a minute and press it again.",
      });
      return;
    }

    if (!response.ok) {
      // The server's own sentence when it gave one: its refusals name the
      // field to fix, and a generic message here would hide that.
      let message = "Something went wrong on our side. Press it again in a moment.";
      try {
        const body = (await response.json()) as { error?: unknown };
        if (typeof body.error === "string" && body.error) message = body.error;
      } catch {
        // Keep the default. A body that is not JSON says nothing the status
        // has not already said.
      }
      setState({ kind: "failed", message });
      return;
    }

    // The confirmation is earned here and nowhere else.
    let recorded: { id?: unknown; submissionId?: unknown; recorded?: unknown } = {};
    try {
      recorded = (await response.json()) as typeof recorded;
    } catch {
      // Falls through to the check below, which refuses an unreadable body.
    }
    if (
      recorded.recorded !== true ||
      recorded.submissionId !== key ||
      typeof recorded.id !== "string" ||
      !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(recorded.id)
    ) {
      setState({
        kind: "failed",
        message:
          "We could not confirm your application was recorded, so we will not tell you it was. Nothing you typed is lost. Press it again; the same answers cannot create a duplicate.",
      });
      return;
    }
    setState({ kind: "sent", reference: recorded.id });
  }

  if (state.kind === "sent") {
    return (
      <div role="status" className="rounded-[8px] bg-white p-7 ring-1 ring-black/10 max-md:p-6">
        <h2 className="text-[22px] leading-snug tracking-tighter text-black">
          It is written down.
        </h2>
        <p className="mt-4 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
          Your application is in the private queue a person reads, oldest first.
          This confirms that the row exists. It is not an offer, and it is not a
          promise of a reply on a known day: nothing here mails you on a
          schedule, so no automatic message has been sent.
        </p>
        <p className="mt-4 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
          Keep this reference if you need to ask us to remove it.
        </p>
        <p className="mt-2 font-mono text-[13px] leading-6 break-all text-black">
          {state.reference}
        </p>
      </div>
    );
  }

  return (
    // NO `noValidate`, and that is the one place this form deliberately parts
    // from the enterprise form next door. Two of its controls are a radio group
    // and a required acknowledgment checkbox, and the browser's own refusal
    // names and focuses the exact control that is missing. Turning that off to
    // own the wording would mean a round trip to learn "choose a role", and the
    // server's answer would arrive as a sentence at the bottom of the form
    // rather than at the control. Everything the browser cannot check, which is
    // the work link's scheme, the hidden field, the rate limit and a refused
    // write, still renders in the alert below.
    <form onSubmit={submit} className="rounded-[8px] bg-white p-7 ring-1 ring-black/10 max-md:p-6">
      <fieldset disabled={sending} className="min-w-0">
        <legend className={LABEL}>Which role</legend>
        <div className="mt-1.5 grid gap-1">
          {ROLES.map((option) => (
            <label
              key={option.value}
              // scroll-mt because this is a fragment target: the site's sticky
              // header is 64px, and `scroll-padding-top` in globals.css covers
              // the document scroll, but the label is what a reader should see
              // whole when the browser lands on the radio inside it.
              className="flex min-h-11 scroll-mt-24 cursor-pointer items-center gap-3 text-[15px] tracking-extra-tight text-black"
              htmlFor={`apply-${option.value}`}
            >
              <input
                id={`apply-${option.value}`}
                type="radio"
                name="role"
                value={option.value}
                checked={role === option.value}
                onChange={() => setRole(option.value)}
                required
                className="size-4 accent-black"
              />
              {option.label}
            </label>
          ))}
        </div>

        <div className="mt-5 grid grid-cols-2 gap-x-5 gap-y-5 max-sm:grid-cols-1">
          <div>
            <label className={LABEL} htmlFor={`${id}-name`}>
              Your name
            </label>
            <input
              id={`${id}-name`}
              name="name"
              autoComplete="name"
              required
              maxLength={200}
              className={FIELD}
            />
          </div>
          <div>
            <label className={LABEL} htmlFor={`${id}-email`}>
              Email
            </label>
            <input
              id={`${id}-email`}
              name="email"
              type="email"
              autoComplete="email"
              required
              maxLength={320}
              className={FIELD}
            />
          </div>
        </div>

        <div className="mt-5">
          <label className={LABEL} htmlFor={`${id}-project`}>
            {/* Full `gray-new-40`, not the /70 the sibling enterprise form uses
                for its own hint. Tailwind v4 emits that opacity as a
                `color-mix`, which resolves to #909297 on white and measures
                3.11:1: an axe run on this page named it, and it is a WCAG AA
                failure on the one word that tells somebody a field is
                skippable. Measured 5.36:1 as written. */}
            Link to your work <span className="text-gray-new-40">(optional)</span>
          </label>
          <input
            id={`${id}-project`}
            name="projectUrl"
            type="url"
            maxLength={2000}
            placeholder="https://"
            className={FIELD}
          />
        </div>

        <div className="mt-5">
          <label className={LABEL} htmlFor={`${id}-why`}>
            What have you built or grown, and why this role
          </label>
          <textarea
            id={`${id}-why`}
            name="why"
            rows={6}
            required
            maxLength={4000}
            placeholder="What you made, what broke, what you did about it. A paragraph is fine."
            className={`${FIELD} h-auto py-2.5 leading-6`}
          />
        </div>

        {/* Empty for a person and filled by most scripted posts. `hidden`
            rather than off-screen positioning, so a screen reader is never
            handed a field it must be told to leave alone. */}
        <div hidden aria-hidden="true">
          <label htmlFor={`${id}-website`}>Leave this empty</label>
          <input id={`${id}-website`} name="website" tabIndex={-1} autoComplete="off" />
        </div>

        <label
          className="mt-5 flex items-start gap-3 text-[13px] leading-6 tracking-extra-tight text-gray-new-40"
          htmlFor={`${id}-compensation`}
        >
          <input
            id={`${id}-compensation`}
            name="compensation"
            type="checkbox"
            required
            className="mt-1 size-4 shrink-0 accent-black"
          />
          {/* The numbers, not a summary of them. Somebody who scrolled straight
              to the form has not read the terms band above it. */}
          <span>
            I understand there is no salary for either role currently. Founding
            engineer equity is 0.5% to 2%; founding growth equity is 0.25% to
            2%. The specific terms are not yet agreed.
          </span>
        </label>
      </fieldset>

      {state.kind === "failed" ? (
        <p
          role="alert"
          className="mt-5 rounded-[8px] bg-[#fdf2f0] px-4 py-3 text-[14px] leading-6 tracking-extra-tight text-[#b32d18]"
        >
          {state.message}
        </p>
      ) : null}

      <div className="mt-6 flex flex-wrap items-center gap-x-5 gap-y-3">
        <button
          type="submit"
          disabled={sending}
          className="inline-flex min-h-11 items-center rounded-full bg-black px-6 text-[15px] font-medium text-white transition-colors hover:bg-[#292929] disabled:cursor-not-allowed disabled:opacity-60"
        >
          {/* The label says what is happening. There is no spinner: a disabled
              control with a changed label carries the same state without
              animating. */}
          {sending
            ? "Recording it"
            : state.kind === "failed"
              ? "Try it again"
              : "Send application"}
        </button>
        <p className="text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
          Read by a person, never sold or added to a newsletter. Removed after
          180 days under our{" "}
          <a
            href="/data-retention"
            className="text-black underline decoration-black/20 underline-offset-4"
          >
            retention policy
          </a>{" "}
          and described in our{" "}
          <a
            href="/privacy"
            className="text-black underline decoration-black/20 underline-offset-4"
          >
            privacy policy
          </a>
          .
        </p>
      </div>

      <noscript>
        <p className="mt-4 text-[14px] leading-6 tracking-extra-tight text-[#b32d18]">
          This form needs JavaScript to send an application. With it turned off,
          nothing here can reach us, so please use a browser that allows it
          rather than pressing the button.
        </p>
      </noscript>
    </form>
  );
}
