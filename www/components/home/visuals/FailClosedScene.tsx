"use client";

import { useState } from "react";
import { LogoMark } from "@/components/icons";

type Case = "payment" | "email" | "unknown";

const EXAMPLES: { id: Case; label: string; host: string; method: string; path: string; detail: string }[] = [
  { id: "payment", label: "Payment", host: "api.stripe.com", method: "POST", path: "/v1/charges", detail: "amount  49.00 USD" },
  { id: "email", label: "Email", host: "api.sendgrid.com", method: "POST", path: "/v3/mail/send", detail: "message  order receipt" },
  { id: "unknown", label: "Unknown host", host: "api.prod.internal", method: "POST", path: "/billing/sync", detail: "policy  no matching rule" },
];

export function FailClosedScene() {
  const [selected, setSelected] = useState<Case>("payment");
  const example = EXAMPLES.find((item) => item.id === selected)!;

  return (
    <figure className="overflow-hidden rounded-xl border border-forest-line/25 bg-white" aria-label="Interactive side-effect firewall example. Choose a payment, email, or unknown destination to see the policy decision and what the agent learns.">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-b border-stroke px-5 py-3.5 sm:px-6">
        <div className="flex items-center gap-3">
          <LogoMark className="size-4 shrink-0" />
          <span className="text-sm font-medium text-gray-new-10">Antifailure</span>
          <span aria-hidden className="text-gray-new-80">/</span>
          <span className="text-sm text-gray-new-40">Side-effect firewall</span>
        </div>
        <span className="text-sm text-gray-new-40">Interactive policy example</span>
      </div>

      <div className="flex overflow-x-auto border-b border-stroke px-2 sm:px-4" role="group" aria-label="Choose an attempted outbound call">
        {EXAMPLES.map((item) => (
          <button
            key={item.id}
            type="button"
            aria-pressed={selected === item.id}
            onClick={() => setSelected(item.id)}
            className={`min-h-12 shrink-0 border-b-2 px-4 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-[-4px] focus-visible:outline-forest ${selected === item.id ? "border-forest text-gray-new-10" : "border-transparent text-gray-new-40 hover:text-gray-new-10"}`}
          >
            {item.label}
          </button>
        ))}
      </div>

      <div className="grid lg:grid-cols-[minmax(0,0.96fr)_minmax(0,1.04fr)]">
        <div className="min-w-0 bg-gray-new-10 text-sage">
          <div className="flex items-center justify-between gap-4 border-b border-white/15 px-5 py-3 text-sm sm:px-6">
            <span>Outbound request</span>
            <span className="text-sage-2/75">inside the twin</span>
          </div>
          <div className="flex min-h-[260px] flex-col justify-between px-5 py-6 sm:min-h-[315px] sm:px-6">
            <div className="min-w-0">
              <p className="text-sm text-sage-2">Your application tries to reach</p>
              <p className="mt-4 break-all text-xl leading-7 tracking-[-0.025em] text-white sm:text-2xl">{example.host}</p>
              <div className="mt-6 overflow-x-auto border-y border-white/15 py-4 font-mono text-sm leading-7 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-green-52" role="region" tabIndex={0} aria-label="Example outbound request">
                <p className="min-w-max"><span className="mr-3 text-green-52">{example.method}</span>{example.path}</p>
                <p className="min-w-max text-sage-2">{example.detail}</p>
              </div>
            </div>
            <p className="mt-8 text-sm leading-6 text-sage-2">The request reaches the twin&apos;s egress policy before it can leave.</p>
          </div>
        </div>

        <div className="flex min-w-0 flex-col justify-between bg-sage px-5 py-6 sm:px-7 sm:py-7" aria-live="polite">
          <div>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="text-sm font-medium text-forest">Policy decision</p>
              <span className={`text-sm font-medium ${selected === "unknown" ? "text-danger-ink" : "text-positive-ink"}`}>
                {selected === "payment" ? "Mock" : selected === "email" ? "Capture" : "Block"}
              </span>
            </div>

            {selected === "payment" ? (
              <>
                <h3 className="mt-5 max-w-[22ch] text-[clamp(1.65rem,2.6vw,2.5rem)] leading-[1.12] tracking-[-0.04em] text-gray-new-10">
                  Checkout completes. Nobody gets charged.
                </h3>
                <p className="mt-4 text-base leading-6 text-gray-new-40">The app receives a simulated payment response from the local pack. The real processor never receives this test charge.</p>
                <div className="mt-6 border-y border-forest-line/20 bg-white/65 px-4 py-4">
                  <div className="flex items-center justify-between gap-4 text-sm text-gray-new-40"><span>Test receipt</span><span className="font-medium text-positive-ink">Simulated</span></div>
                  <div className="mt-4 flex items-baseline justify-between gap-4 border-t border-stroke pt-3"><strong className="text-2xl font-medium tabular-nums text-gray-new-10">$49.00</strong><span className="text-sm text-gray-new-40">real charge: none</span></div>
                </div>
              </>
            ) : selected === "email" ? (
              <>
                <h3 className="mt-5 max-w-[22ch] text-[clamp(1.65rem,2.6vw,2.5rem)] leading-[1.12] tracking-[-0.04em] text-gray-new-10">
                  Inspect the email without sending it.
                </h3>
                <p className="mt-4 text-base leading-6 text-gray-new-40">The message is captured for review. Your app exercised the send path; a customer inbox did not receive the test mail.</p>
                <div className="mt-6 border-y border-forest-line/20 bg-white/65 px-4 py-4">
                  <div className="flex items-center justify-between gap-4 text-sm text-gray-new-40"><span>Test inbox</span><span className="font-medium text-positive-ink">Captured</span></div>
                  <div className="mt-4 border-t border-stroke pt-3"><strong className="text-base font-medium text-gray-new-10">Order receipt</strong><p className="mt-1 text-sm text-gray-new-40">Available to inspect inside the twin.</p></div>
                </div>
              </>
            ) : (
              <>
                <h3 className="mt-5 max-w-[22ch] text-[clamp(1.65rem,2.6vw,2.5rem)] leading-[1.12] tracking-[-0.04em] text-gray-new-10">
                  An unlisted production API receives nothing.
                </h3>
                <p className="mt-4 text-base leading-6 text-gray-new-40">No host rule matched this request. The default block policy refuses it before it can touch a live service.</p>
                <div className="mt-6 border-y border-forest-line/20 bg-white/65 px-4 py-4">
                  <div className="flex items-center justify-between gap-4 text-sm text-gray-new-40"><span>Decision log</span><span className="font-medium text-danger-ink">Refused</span></div>
                  <div className="mt-4 border-t border-stroke pt-3"><strong className="break-all text-base font-medium text-gray-new-10">{example.host}</strong><p className="mt-1 text-sm text-gray-new-40">Matched rule: none. Applied default: block.</p></div>
                </div>
              </>
            )}
          </div>
          <p className="mt-7 border-t border-forest-line/20 pt-4 text-sm leading-6 text-gray-new-40">
            Your agent sees the policy decision and observed counts through <span className="font-medium text-gray-new-10">inspect_egress_firewall</span>.
          </p>
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1 border-t border-forest-line/20 px-5 py-4 text-sm sm:px-6">
        <span className="font-medium text-forest">Back to your agent as a structured result</span>
        <span className="text-gray-new-40">Policy · observed decisions · findings</span>
      </div>
      <figcaption className="border-t border-stroke px-5 py-3 text-sm leading-6 text-gray-new-40 sm:px-6">
        Illustrative policy decisions, not live customer telemetry. If the decision log cannot be read, Antifailure returns INCONCLUSIVE rather than claiming zero effects.
      </figcaption>
      <details className="border-t border-stroke px-5 text-sm sm:px-6">
        <summary className="flex min-h-11 cursor-pointer items-center font-medium text-forest focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-forest">Compare all three policy outcomes</summary>
        <div className="grid gap-5 border-t border-stroke py-4 leading-6 text-gray-new-40 sm:grid-cols-3">
          <div><h4 className="font-medium text-gray-new-10">Payment mocked</h4><p className="mt-1">A POST to api.stripe.com receives a local simulated response. The test checkout continues without a real processor charge.</p></div>
          <div><h4 className="font-medium text-gray-new-10">Email captured</h4><p className="mt-1">A POST to api.sendgrid.com is retained in the test inbox. The message can be inspected without sending it to a customer.</p></div>
          <div><h4 className="font-medium text-gray-new-10">Production API blocked</h4><p className="mt-1">A POST to the unlisted api.prod.internal host matches no rule. The default block policy refuses it before it can touch the live service.</p></div>
        </div>
      </details>
    </figure>
  );
}
