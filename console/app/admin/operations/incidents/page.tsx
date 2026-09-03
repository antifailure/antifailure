"use client";

/**
 * Incidents & Kill Switches.
 *
 * THE PAGE AN OPERATOR OPENS AT THREE IN THE MORNING, on a phone, to answer one
 * question: is anything paused, and how do I change that. Everything below is
 * arranged around that and nothing else.
 *
 * The four decisions worth stating, because each one is a way this page could
 * have been wrong:
 *
 *   1. STATE IS READABLE AT A GLANCE AND WITHOUT COLOUR. The summary at the top
 *      says how many switches are engaged in words before any chip is read, and
 *      every chip carries the word too. Nothing on this page animates. A dot
 *      that throbs while the reader does nothing is the loudest thing on the
 *      screen and says exactly as much as a still one, and `just motioncheck`
 *      refuses it against the built stylesheet.
 *
 *   2. THE BLAST RADIUS IS SHOWN BEFORE THE CONFIRMATION, NOT AFTER. `effect`
 *      comes from the server's own catalog and is rendered whole, including the
 *      half that says what KEEPS working, because an operator who does not know
 *      that reads will survive maintenance mode will not engage it.
 *
 *   3. THE WAY BACK IS VISIBLE AT ALL TIMES. `release` sits beside every
 *      control whether it is engaged or not. An operator who cannot find the
 *      way back has an outage this product caused, and finding it should not
 *      require having engaged the switch first.
 *
 *   4. THE REASON IS REQUIRED TO ENGAGE AND SHOWN AFTERWARDS. The server
 *      refuses an engage with no reason, so the form does too rather than
 *      letting the reader discover it in an error. Once engaged, the reason,
 *      who set it and when are on the card, because the next person on call
 *      cannot safely release a switch nobody explained.
 *
 * WHAT IS NOT HERE, AND WHY IT IS NOT DRAWN. There is no incident record in
 * this product: no incident table, no timeline, no postmortem, no status page
 * entry, no on-call rota. So this page does not draw one. A page of empty
 * incident cards would read, during an incident, as "no incidents", and that is
 * the most expensive sentence a console can say by accident.
 */

import { useState } from "react";
import Link from "next/link";
import {
  Badge,
  Button,
  Card,
  CardSkeleton,
  Confirm,
  Field,
  Loaded,
  inputClass,
} from "@/components/ui";
import { AdminPage, Facts, StatusChip } from "@/components/admin/primitives";
import { When } from "@/components/ui";
import { operatorMay, useAdminContext } from "@/lib/admin";
import { setControl, useControls, type ControlState } from "@/lib/admin-operations";

export default function OperationsIncidentsPage() {
  const state = useControls();
  const { me } = useAdminContext();
  const mayEngage = operatorMay(me, "admin.emergency.engage");

  return (
    <AdminPage
      href="/admin/operations/incidents"
      lede="Three switches stop parts of this installation for everybody on it. Each one says exactly what it refuses, what keeps working, and how to put it back."
    >
      <Loaded state={state} skeleton={<CardSkeleton count={3} />} framed>
        {(controls) => (
          <div className="space-y-6">
            <Summary controls={controls} mayEngage={mayEngage} />
            {controls.map((c) => (
              <ControlCard
                key={c.name}
                control={c}
                mayEngage={mayEngage}
                onChanged={state.reload}
              />
            ))}
            <NotAnIncidentLog />
          </div>
        )}
      </Loaded>
    </AdminPage>
  );
}

/**
 * The answer to the question, above everything that qualifies it.
 *
 * The count is in the sentence rather than only in the chips below, because a
 * reader scanning three cards for a coloured badge can miss one, and the whole
 * cost of missing one is that they keep looking for a fault that is not there.
 */
function Summary({ controls, mayEngage }: { controls: ControlState[]; mayEngage: boolean }) {
  const engaged = controls.filter((c) => c.engaged);
  const paused = engaged.length > 0;
  return (
    <section
      // A tint rather than a border colour, matching Badge's own values so the
      // page has one red and one green rather than a second set.
      className={`rounded-lg border border-rule px-4 py-4 ${
        paused ? "bg-[rgba(179,38,30,0.1)]" : "bg-card"
      }`}
      // A status region, not an alert. The reader came here to read it; an
      // assertive announcement over a page somebody navigated to on purpose is
      // its own defect.
      role="status"
    >
      <p className="text-[14px] font-semibold tracking-extra-tight text-ink">
        {paused
          ? engaged.length === 1
            ? `One switch is engaged: ${engaged[0]!.definition.title}.`
            : `${engaged.length} switches are engaged: ${engaged
                .map((c) => c.definition.title)
                .join(", ")}.`
          : "Nothing is paused."}
      </p>
      <p className="mt-1.5 max-w-[62ch] text-[13px] leading-6 text-muted">
        {paused
          ? `${engaged.length === 1 ? "It is" : "They are"} in force for every organization on this installation. ${
              engaged.length === 1 ? "The card" : "The cards"
            } below carr${engaged.length === 1 ? "ies" : "y"} the reason each was set and the way to release it.`
          : "Sign-ups, runs and every request that changes something are all working normally. Nobody has touched a switch on this installation."}
      </p>
      {!mayEngage ? (
        <p className="mt-2 max-w-[62ch] text-[12.5px] leading-5 text-dim">
          Your role can see these switches and cannot throw them. Engaging or releasing one needs{" "}
          <code className="font-mono text-[12px] text-muted">admin.emergency.engage</code>, which
          only the owner and super admin roles hold.
        </p>
      ) : null}
    </section>
  );
}

/**
 * One switch: what it does, what it is doing now, and both directions out.
 *
 * Engage and release are the SAME card rather than two screens. The file that
 * defines these controls makes the point that reversal is part of the
 * definition and not an afterthought, and a console that puts the way back
 * behind a second screen contradicts that at the moment it matters.
 */
function ControlCard({
  control,
  mayEngage,
  onChanged,
}: {
  control: ControlState;
  mayEngage: boolean;
  onChanged: () => void;
}) {
  const [reason, setReason] = useState("");
  const [confirming, setConfirming] = useState<null | "engage" | "release">(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const d = control.definition;
  const trimmed = reason.trim();

  async function run(engaged: boolean) {
    setBusy(true);
    setError(null);
    try {
      await setControl(control.name, engaged, engaged ? trimmed : null);
      setConfirming(null);
      setReason("");
      onChanged();
    } catch (e) {
      // Kept on the dialog rather than closing it. A failed engage that closes
      // the confirmation leaves the reader looking at an unchanged page with no
      // idea whether they pressed the button.
      setError(e instanceof Error ? e.message : "The control plane refused that.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card
      title={d.title}
      actions={
        <StatusChip
          value={control.engaged ? "engaged" : "released"}
          tone={control.engaged ? "fail" : "pass"}
        />
      }
    >
      <div className="space-y-4 px-4 py-4">
        <div>
          <p className="text-[11.5px] font-medium uppercase tracking-[0.08em] text-dim">
            What engaging it does
          </p>
          <p className="mt-1.5 max-w-[70ch] text-[13px] leading-6 text-ink">{d.effect}</p>
        </div>

        <div>
          <p className="text-[11.5px] font-medium uppercase tracking-[0.08em] text-dim">
            How to put it back
          </p>
          <p className="mt-1.5 max-w-[70ch] text-[13px] leading-6 text-muted">{d.release}</p>
        </div>

        {/* Where the refusal actually lives. Printed because a switch that
            claims an enforcement it does not have is the failure the controls
            catalog exists to prevent, and a test greps this exact file for this
            exact symbol. An operator can check the claim. */}
        <p className="text-[12px] leading-5 text-dim">
          Refused by{" "}
          <code className="break-all font-mono text-[12px] text-muted">{d.enforcedBy}</code>
        </p>

        {control.engaged ? (
          <div className="rounded-md border border-rule bg-paper">
            <Facts
              facts={[
                {
                  label: "Reason",
                  value: control.reason ?? (
                    <span className="text-warn">No reason was recorded.</span>
                  ),
                },
                { label: "Engaged by", value: control.engagedBy, mono: true },
                { label: "Engaged", value: <When value={control.engagedAt} /> },
              ]}
            />
          </div>
        ) : control.updatedAt ? (
          <p className="text-[12.5px] leading-5 text-dim">
            Last released <When value={control.updatedAt} />
            {control.engagedBy ? ` by ${control.engagedBy}` : ""}.
          </p>
        ) : null}

        {mayEngage ? (
          control.engaged ? (
            <div className="flex flex-wrap items-center gap-3">
              <Button onClick={() => setConfirming("release")}>Release {d.title}</Button>
              <span className="text-[12.5px] text-muted">
                Takes effect on the next request. Nothing has to be restarted.
              </span>
            </div>
          ) : (
            <div className="space-y-3">
              <Field
                label="Why you are engaging this"
                hint="Required, and recorded. It is what the next person on call reads before releasing it."
              >
                <input
                  className={inputClass}
                  value={reason}
                  maxLength={500}
                  onChange={(e) => setReason(e.target.value)}
                  placeholder="What is happening, in one line"
                  autoComplete="off"
                />
              </Field>
              <Button
                variant="danger"
                disabled={trimmed === ""}
                onClick={() => setConfirming("engage")}
              >
                Engage {d.title}
              </Button>
            </div>
          )
        ) : null}
      </div>

      <Confirm
        open={confirming === "engage"}
        title={`Engage ${d.title}?`}
        // The control's OWN name, typed. Not the word "confirm": typing that
        // proves you can read a label, and typing `new_runs` proves you know
        // which of the three switches is under your finger, which is the
        // mistake that actually happens on a page with three of them.
        phrase={control.name}
        confirmLabel={`Engage ${d.title}`}
        busy={busy}
        error={error}
        onConfirm={() => run(true)}
        onCancel={() => {
          if (!busy) {
            setConfirming(null);
            setError(null);
          }
        }}
      >
        <p className="text-ink">{d.effect}</p>
        <p>
          This applies to every organization on this installation, immediately, and stays until
          somebody releases it.
        </p>
        <p>
          <span className="text-dim">Recorded reason: </span>
          <span className="break-words text-ink">{trimmed}</span>
        </p>
      </Confirm>

      <Confirm
        open={confirming === "release"}
        title={`Release ${d.title}?`}
        // No phrase. Releasing is the safe direction and the one an operator
        // may be doing under pressure; making them type an identifier to STOP
        // an outage would be ceremony pointed the wrong way.
        confirmLabel={`Release ${d.title}`}
        busy={busy}
        error={error}
        onConfirm={() => run(false)}
        onCancel={() => {
          if (!busy) {
            setConfirming(null);
            setError(null);
          }
        }}
      >
        <p className="text-ink">{d.release}</p>
        {control.reason ? (
          <p>
            <span className="text-dim">It was engaged because: </span>
            <span className="break-words text-ink">{control.reason}</span>
          </p>
        ) : null}
      </Confirm>
    </Card>
  );
}

/**
 * What this page is not.
 *
 * Said plainly rather than left for the reader to discover by looking for a
 * section that is not there. The alternative on offer was a page of incident
 * cards over a table that does not exist, and an empty one of those reads as
 * "no incidents" during the exact hour somebody is having one.
 */
function NotAnIncidentLog() {
  return (
    <Card title="There is no incident record in this product">
      <div className="space-y-3 px-4 py-4 text-[13px] leading-6 text-muted">
        <p className="max-w-[70ch]">
          Nothing in this schema stores an incident, a timeline, a postmortem, a status page entry
          or an on-call rota. This page is the three installation-wide switches and their current
          state, which is what genuinely exists.
        </p>
        <p className="max-w-[70ch]">
          Every engage and release writes to the platform audit chain, at critical and high
          severity, with the reason and the operator who acted. That is the history, and it is on{" "}
          <Link
            href="/admin/security/audit"
            className="underline decoration-[rgba(16,16,16,0.35)] underline-offset-4"
          >
            the operator log
          </Link>
          .
        </p>
        <p className="max-w-[70ch]">
          Two narrower controls live elsewhere and are deliberately not duplicated here: suspending
          one organization is on that tenant&apos;s page, and killing one feature flag is under
          Feature Flags. Both are per tenant or per feature. These three are the whole
          installation.
        </p>
        <p className="max-w-[70ch] text-dim">
          <Badge tone="neutral">what would change this</Badge> An incident record would need a
          table of its own, written to when a switch is engaged and when a health check turns red,
          plus somewhere to attach a narrative. That is a schema change, not a page.
        </p>
      </div>
    </Card>
  );
}
