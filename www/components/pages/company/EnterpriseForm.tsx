"use client";

import { useId, useRef, useState } from "react";
import { controlPlaneUrl } from "@/lib/control-plane-routes";
import { leadSubmitted } from "@/lib/analytics";

/**
 * The one form on this site that takes something from a stranger.
 *
 * WHAT IT REPLACES, because the shape of the failure decides the shape of this
 * component. The site's only commercial route was a waitlist: it posted an
 * address to a function that wrote one row into a store nothing in the
 * repository could read, and mailed nobody, on a domain that publishes no mail
 * exchanger and an SPF policy authorizing no sender. The confirmation promised
 * a message anyway. Nothing could have sent one.
 *
 * So the two rules here are:
 *
 * IT POSTS AT A REAL ENDPOINT. `POST /v1/leads` on the control plane writes a
 * row into the product's own database, which is backed up, restored, drilled,
 * and readable by an operator through `af-control-plane-backup leads`. That is
 * a different host from this one, which is why the endpoint is the only route
 * on that server with a CORS header, scoped to exactly this origin and carrying
 * no credentials.
 *
 * IT SAYS WHICH THING HAPPENED. The response carries `notified`, and the
 * confirmation below renders one of two different sentences from it. On a
 * deployment with a mailer, somebody has been told. On ours today, the lead is
 * recorded and a person reads the queue, and the screen says so in those words.
 * A single cheerful promise of contact would be the waitlist again, with better
 * spacing.
 *
 * THE STATES ARE ALL BUILT, and they are the reason this is a client component
 * on an otherwise static page. Idle, submitting with the control disabled and
 * the button saying what it is doing, one error with a human sentence and the
 * form still filled in so nothing is retyped, and a confirmation that replaces
 * the form rather than sitting under it.
 */

type State =
  | { kind: "idle" }
  | { kind: "sending" }
  | { kind: "sent"; notified: boolean }
  | { kind: "failed"; message: string };

const FIELD =
  "mt-1.5 h-11 w-full rounded-[8px] border border-black/15 bg-white px-3 text-[15px] text-black outline-none placeholder:text-black/40 focus-visible:border-black/45 focus-visible:ring-2 focus-visible:ring-black/10 disabled:opacity-60";
const LABEL = "block text-[13px] tracking-extra-tight text-gray-new-40";

export function EnterpriseForm() {
  const [state, setState] = useState<State>({ kind: "idle" });
  const formRef = useRef<HTMLFormElement>(null);
  const id = useId();

  const sending = state.kind === "sending";

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (sending) return;
    const data = new FormData(event.currentTarget);
    setState({ kind: "sending" });

    let response: Response;
    try {
      response = await fetch(controlPlaneUrl("leads.create"), {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          name: String(data.get("name") ?? ""),
          email: String(data.get("email") ?? ""),
          company: String(data.get("company") ?? ""),
          seats: data.get("seats") ? Number(data.get("seats")) : null,
          message: String(data.get("message") ?? ""),
          source: "contact",
        }),
      });
    } catch {
      // A network failure, a blocked request, or an origin the control plane
      // will not answer. The form keeps everything that was typed, because
      // making somebody fill it in twice is how a retry becomes an abandonment.
      setState({
        kind: "failed",
        message:
          "Could not reach the server. Check your connection and press it again; nothing you typed is lost.",
      });
      // Deliberately not counted. `refused` means the endpoint answered and
      // would not take it, which is a fact about the submission; a request that
      // never arrived says nothing about this person and counting it would put
      // every flaky connection in the denominator of the acquisition funnel.
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
      // The server's own sentence when it gave one. A generic failure message
      // on a form is how people conclude a product is broken, and the server's
      // refusals here name the field to fix.
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
      // A 2xx whose body cannot be read is still a recorded lead. The
      // confirmation then says the more conservative of the two things.
    }
    setState({ kind: "sent", notified });
    // THE ONLY PRODUCER OF site.lead_submitted, which is the last step of the
    // acquisition funnel. The event carries the channel the SESSION started on
    // rather than this page's referrer, so the funnel answers what brought
    // somebody here rather than which page held the form. It carries no name,
    // no address and nothing that was typed: see www/lib/beacon.ts for the
    // whole list of what leaves the browser.
    leadSubmitted(notified ? "notified" : "recorded");
    formRef.current?.reset();
  }

  if (state.kind === "sent") {
    return (
      <div
        role="status"
        className="rounded-[8px] bg-white p-7 ring-1 ring-black/10 max-md:p-6"
      >
        <h3 className="text-[20px] leading-snug tracking-tighter text-black">
          It is written down.
        </h3>
        <p className="mt-4 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
          {state.notified
            ? "Somebody has been told, and it is in the queue behind them, so it is not waiting on one person reading their mail."
            : "It is in the queue a person reads, oldest first. Nothing here mails you on a schedule, so if you need an answer on a known day, book a call above: that is a real calendar with real openings."}
        </p>
        <p className="mt-4 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
          Nothing is waiting on us for the parts that do not need us. The engine
          is open source and runs on your own machine, and the{" "}
          <a
            className="text-black underline decoration-black/20 underline-offset-4 hover:decoration-black"
            href="/docs/getting-started/quickstart"
          >
            quickstart
          </a>{" "}
          needs no account at all.
        </p>
        <button
          type="button"
          onClick={() => setState({ kind: "idle" })}
          className="mt-6 inline-flex min-h-11 items-center rounded-full border border-black/15 bg-white px-5 text-[14px] font-medium text-black transition-colors hover:border-black/35"
        >
          Send another
        </button>
      </div>
    );
  }

  return (
    <form
      ref={formRef}
      onSubmit={submit}
      noValidate
      className="rounded-[8px] bg-white p-7 ring-1 ring-black/10 max-md:p-6"
    >
      <div className="grid grid-cols-2 gap-x-5 gap-y-5 max-sm:grid-cols-1">
        <div>
          <label className={LABEL} htmlFor={`${id}-name`}>
            Your name
          </label>
          <input
            id={`${id}-name`}
            name="name"
            autoComplete="name"
            required
            disabled={sending}
            className={FIELD}
          />
        </div>
        <div>
          <label className={LABEL} htmlFor={`${id}-email`}>
            Work email
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
            Company
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
          <label className={LABEL} htmlFor={`${id}-seats`}>
            Seats, roughly{" "}
            <span className="text-gray-new-40/70">(skip it if you do not know)</span>
          </label>
          <input
            id={`${id}-seats`}
            name="seats"
            type="number"
            min={1}
            inputMode="numeric"
            disabled={sending}
            className={FIELD}
          />
        </div>
      </div>

      <div className="mt-5">
        <label className={LABEL} htmlFor={`${id}-message`}>
          What you need
        </label>
        <textarea
          id={`${id}-message`}
          name="message"
          rows={4}
          required
          disabled={sending}
          placeholder="Seats and roles, single sign-on, a security review, an agreement to sign, where your data has to stay."
          className={`${FIELD} h-auto py-2.5 leading-6`}
        />
      </div>

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
          {/* The label says what is happening rather than staying still while a
              round trip runs. There is no spinner: a disabled control with a
              changed label communicates the same state without animating. */}
          {sending ? "Sending" : "Send it"}
        </button>
        <p className="text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
          It is stored in the product database, read by a person, and never sold
          or added to a newsletter.
        </p>
      </div>
    </form>
  );
}
