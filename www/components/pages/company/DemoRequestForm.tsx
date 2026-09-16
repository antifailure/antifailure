"use client";

import { useId, useRef, useState } from "react";
import { controlPlaneUrl } from "@/lib/control-plane-routes";
import { leadSubmitted } from "@/lib/analytics";
import {
  COUNTRIES,
  ORG_TYPES,
  composeDemoLead,
  validateDemoRequest,
  type DemoRequestFields,
} from "@/lib/demo-request";
import { FIELD, LABEL } from "./EnterpriseForm";

/**
 * The demo-request form on /request-demo.
 *
 * It is the enterprise form's twin, on purpose. It posts to the same endpoint
 * (POST /v1/leads), reads the same `notified` back, renders the same four
 * states, and wears the same field vocabulary, imported rather than copied. The
 * differences are the ones the page needs: the Harvey field set a sales team
 * asks for, an organization type and a country as selects, and a marketing
 * opt-in. The extra fields the leads table has no column for are folded into
 * the message by composeDemoLead, and `source` is "demo".
 *
 * THE STATES ARE ALL BUILT, which is why this is a client component on an
 * otherwise static page. Idle; sending, with every control disabled and the
 * button naming what it is doing and no spinner; one error with a human
 * sentence and the form still filled so nothing is retyped; and a confirmation
 * that replaces the form. Validation runs here before the network so a missing
 * field is answered instantly rather than after a round trip.
 *
 * THE HONEYPOT is a field a person never sees and a bot fills. It is off-screen
 * rather than display:none, because a bot reads the computed style; it is out
 * of the tab order and autocomplete; and when it comes back with anything in
 * it, the form shows the same confirmation it shows a real submission and posts
 * nothing. A bot that believes it succeeded does not retry, and no row is
 * written. /v1/leads keeps its rate limit and its exact-origin CORS underneath.
 */

type State =
  | { kind: "idle" }
  | { kind: "sending" }
  | { kind: "sent"; notified: boolean }
  | { kind: "failed"; message: string };

export function DemoRequestForm() {
  const [state, setState] = useState<State>({ kind: "idle" });
  const formRef = useRef<HTMLFormElement>(null);
  const id = useId();

  const sending = state.kind === "sending";

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (sending) return;
    const data = new FormData(event.currentTarget);

    // The honeypot, checked first. A person leaves it empty because they never
    // see it; a bot fills it. Answer the way a real submission is answered and
    // write nothing, so the bot learns nothing and does not come back.
    if (String(data.get("company_website") ?? "").trim() !== "") {
      setState({ kind: "sent", notified: false });
      formRef.current?.reset();
      return;
    }

    const fields: DemoRequestFields = {
      firstName: String(data.get("firstName") ?? ""),
      lastName: String(data.get("lastName") ?? ""),
      email: String(data.get("email") ?? ""),
      company: String(data.get("company") ?? ""),
      jobTitle: String(data.get("jobTitle") ?? ""),
      phone: String(data.get("phone") ?? ""),
      orgType: String(data.get("orgType") ?? ""),
      country: String(data.get("country") ?? ""),
      marketingOptIn: data.get("marketingOptIn") === "on",
    };

    const problem = validateDemoRequest(fields);
    if (problem) {
      // The form keeps everything typed; only the message changes.
      setState({ kind: "failed", message: problem });
      return;
    }

    setState({ kind: "sending" });

    let response: Response;
    try {
      response = await fetch(controlPlaneUrl("leads.create"), {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(composeDemoLead(fields)),
      });
    } catch {
      setState({
        kind: "failed",
        message:
          "Could not reach the server. Check your connection and press it again; nothing you typed is lost.",
      });
      return;
    }

    if (response.status === 429) {
      setState({
        kind: "failed",
        message: "That was a lot of attempts at once. Wait a minute and press it again.",
      });
      leadSubmitted("refused");
      return;
    }

    if (!response.ok) {
      let message = "Something went wrong on our side. Press it again in a moment.";
      try {
        const body = (await response.json()) as { error?: unknown };
        if (typeof body.error === "string" && body.error) message = body.error;
      } catch {
        // Keep the default.
      }
      setState({ kind: "failed", message });
      leadSubmitted("refused");
      return;
    }

    let notified = false;
    try {
      const body = (await response.json()) as { notified?: unknown };
      notified = body.notified === true;
    } catch {
      // A 2xx whose body cannot be read is still a recorded lead.
    }
    setState({ kind: "sent", notified });
    leadSubmitted(notified ? "notified" : "recorded");
    formRef.current?.reset();
  }

  if (state.kind === "sent") {
    return (
      <div role="status" className="rounded-[8px] bg-white p-5 ring-1 ring-black/10 sm:p-6">
        <h2 className="text-[20px] leading-snug tracking-tighter text-black">
          It is in the queue.
        </h2>
        <p className="mt-4 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
          {state.notified
            ? "Somebody has been told, and it is written down behind them, so it is not waiting on one person reading their mail. Expect a reply to set up a time."
            : "It is written into the product database, read by a person oldest first, and never sold or added to a newsletter. Expect a reply to set up a time."}
        </p>
        <p className="mt-4 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
          Nothing waits on us for the parts that do not need us. The engine is
          open source and runs on your own machine, and the{" "}
          <a
            className="text-black underline decoration-black/20 underline-offset-4 hover:decoration-black"
            href="/docs/getting-started/quickstart"
          >
            quickstart
          </a>{" "}
          needs no account at all.
        </p>
      </div>
    );
  }

  return (
    <form
      ref={formRef}
      onSubmit={submit}
      noValidate
      className="rounded-[8px] bg-white p-5 ring-1 ring-black/10 sm:p-6"
    >
      <div className="grid grid-cols-2 gap-x-4 gap-y-3.5 max-sm:grid-cols-1">
        <div>
          <label className={LABEL} htmlFor={`${id}-firstName`}>
            First name
          </label>
          <input
            id={`${id}-firstName`}
            name="firstName"
            autoComplete="given-name"
            required
            disabled={sending}
            className={FIELD}
          />
        </div>
        <div>
          <label className={LABEL} htmlFor={`${id}-lastName`}>
            Last name
          </label>
          <input
            id={`${id}-lastName`}
            name="lastName"
            autoComplete="family-name"
            required
            disabled={sending}
            className={FIELD}
          />
        </div>
        <div>
          <label className={LABEL} htmlFor={`${id}-email`}>
            Business email
          </label>
          <input
            id={`${id}-email`}
            name="email"
            type="email"
            autoComplete="email"
            required
            disabled={sending}
            className={FIELD}
          />
        </div>
        <div>
          <label className={LABEL} htmlFor={`${id}-company`}>
            Company name
          </label>
          <input
            id={`${id}-company`}
            name="company"
            autoComplete="organization"
            required
            disabled={sending}
            className={FIELD}
          />
        </div>
        <div>
          <label className={LABEL} htmlFor={`${id}-jobTitle`}>
            Job title
          </label>
          <input
            id={`${id}-jobTitle`}
            name="jobTitle"
            autoComplete="organization-title"
            required
            disabled={sending}
            className={FIELD}
          />
        </div>
        <div>
          <label className={LABEL} htmlFor={`${id}-phone`}>
            Phone <span className="text-gray-new-50">(optional)</span>
          </label>
          <input
            id={`${id}-phone`}
            name="phone"
            type="tel"
            autoComplete="tel"
            inputMode="tel"
            disabled={sending}
            className={FIELD}
          />
        </div>
        <div>
          <label className={LABEL} htmlFor={`${id}-orgType`}>
            Organization type
          </label>
          <select
            id={`${id}-orgType`}
            name="orgType"
            required
            defaultValue=""
            disabled={sending}
            className={`${FIELD} appearance-none bg-[length:16px] bg-[right_0.75rem_center] bg-no-repeat pr-10`}
            style={{
              backgroundImage:
                "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='16' height='16' viewBox='0 0 16 16' fill='none'%3E%3Cpath d='M4 6l4 4 4-4' stroke='%23666a73' stroke-width='1.4'/%3E%3C/svg%3E\")",
            }}
          >
            <option value="" disabled>
              Select one
            </option>
            {ORG_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className={LABEL} htmlFor={`${id}-country`}>
            Country
          </label>
          <select
            id={`${id}-country`}
            name="country"
            required
            defaultValue=""
            disabled={sending}
            className={`${FIELD} appearance-none bg-[length:16px] bg-[right_0.75rem_center] bg-no-repeat pr-10`}
            style={{
              backgroundImage:
                "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='16' height='16' viewBox='0 0 16 16' fill='none'%3E%3Cpath d='M4 6l4 4 4-4' stroke='%23666a73' stroke-width='1.4'/%3E%3C/svg%3E\")",
            }}
          >
            <option value="" disabled>
              Select one
            </option>
            {COUNTRIES.map((country) => (
              <option key={country} value={country}>
                {country}
              </option>
            ))}
          </select>
        </div>
      </div>

      {/* The honeypot. Off-screen rather than display:none so a bot reading the
          computed style still finds it, out of the tab order and autocomplete
          so a person using a keyboard or a password manager never lands in it,
          and marked aria-hidden so a screen reader skips it. */}
      <div aria-hidden className="absolute left-[-9999px] top-[-9999px] h-0 w-0 overflow-hidden">
        <label htmlFor={`${id}-company_website`}>Company website</label>
        <input
          id={`${id}-company_website`}
          name="company_website"
          type="text"
          tabIndex={-1}
          autoComplete="off"
        />
      </div>

      <label className="mt-4 flex items-start gap-3">
        <input
          type="checkbox"
          name="marketingOptIn"
          disabled={sending}
          className="mt-0.5 size-4 shrink-0 rounded-[4px] border-black/25 text-black accent-black focus-visible:ring-2 focus-visible:ring-black/10"
        />
        <span className="text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
          Send me the occasional product update. Unchecked by default, no
          newsletter.
        </span>
      </label>

      {state.kind === "failed" ? (
        <p
          role="alert"
          className="mt-4 rounded-[8px] bg-[#fdf2f0] px-4 py-3 text-[14px] leading-6 tracking-extra-tight text-[#b32d18]"
        >
          {state.message}
        </p>
      ) : null}

      <div className="mt-5 flex flex-wrap items-center gap-x-4 gap-y-2">
        <button
          type="submit"
          disabled={sending}
          className="inline-flex min-h-11 items-center rounded-full bg-black px-6 text-[15px] font-medium text-white transition-colors hover:bg-[#292929] disabled:cursor-not-allowed disabled:opacity-60"
        >
          {/* No spinner: a disabled control with a changed label says the same
              thing without animating. */}
          {sending ? "Requesting" : "Request a demo"}
        </button>
        <p className="text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
          Stored in the product database, read by a person, never sold.
        </p>
      </div>
    </form>
  );
}
