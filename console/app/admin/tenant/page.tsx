"use client";

import { Suspense, useState } from "react";
import { useSearchParams } from "next/navigation";
import {
  Badge,
  Button,
  Card,
  Confirm,
  Empty,
  Field,
  Loaded,
  Page,
  TableSkeleton,
  When,
  inputClass,
} from "@/components/ui";
import { operatorMay, resumeTenant, suspendTenant, useAdminContext, useTenant } from "@/lib/admin";
import type { ApiError } from "@/lib/api";

/**
 * One tenant, and the two things an operator can do to it.
 *
 * A QUERY STRING rather than /admin/tenant/[id], because the console is a
 * static export and a dynamic segment cannot be exported without knowing every
 * id at build time. next.config.ts says this outright and the rest of the
 * console already follows it.
 */

/* -------------------------------------------------------------------------
 * Suspend and resume
 * ---------------------------------------------------------------------- */

/**
 * WHY THE EFFECT SENTENCE IS SHOWN VERBATIM AND BEFORE THE ACTION.
 *
 * "Suspended" sounds like "locked out" and it is not. Suspension stops an
 * organization creating anything NEW; what is already running keeps running and
 * everything already there stays readable. An operator who reads it as a
 * lockout during an incident will go looking for the next lever to pull, and
 * the next lever is worse than this one.
 *
 * So the sentence is not decoration and not a tooltip. It is the same string
 * the route returns in its response, shown before the button is pressed rather
 * than after, because the moment it changes somebody's mind is BEFORE.
 */
const SUSPEND_EFFECT =
  "No new environments, agent runs or events. Anything already running keeps running.";

function SuspendControls({
  orgId,
  name,
  suspended,
  onChanged,
}: {
  orgId: string;
  name: string;
  suspended: boolean;
  onChanged: () => void;
}) {
  const { me } = useAdminContext();
  const [asking, setAsking] = useState(false);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [effect, setEffect] = useState<string | null>(null);

  // The nav hides what an operator cannot do and so does this, but the server
  // refuses regardless: the button is a convenience, never the enforcement.
  if (!operatorMay(me, "admin.tenants.suspend")) return null;

  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
      setAsking(false);
      setReason("");
      onChanged();
    } catch (err) {
      setError((err as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  if (suspended) {
    return (
      <div className="flex flex-col gap-2">
        <Button
          variant="primary"
          busy={busy}
          disabled={busy}
          onClick={() => void run(() => resumeTenant(orgId))}
        >
          Resume this organization
        </Button>
        {error ? (
          <p role="alert" className="text-[12.5px] leading-5 text-fail">
            {error}
          </p>
        ) : null}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <Button variant="danger" onClick={() => setAsking(true)}>
        Suspend this organization
      </Button>
      {effect ? (
        <p role="status" className="text-[12.5px] leading-5 text-muted">
          Suspended. {effect}
        </p>
      ) : null}

      <Confirm
          open={asking}
          title={`Suspend ${name}?`}
          confirmLabel="Suspend"
          busy={busy}
          error={error}
          onCancel={() => {
            setAsking(false);
            setError(null);
          }}
          onConfirm={() =>
            void run(async () => {
              const result = await suspendTenant(orgId, reason);
              // Shown as the route worded it rather than as this page would
              // have worded it. Two sentences that mean the same thing today
              // are two sentences that disagree after somebody edits one.
              setEffect(result.effect);
            })
          }
        >
          <p className="text-[13px] leading-6 text-muted">
            <strong className="font-medium text-ink">What this does:</strong> {SUSPEND_EFFECT}
          </p>
          <p className="mt-3 text-[13px] leading-6 text-muted">
            The customer sees this in their own audit log, attributed to the platform rather than
            to somebody in their organization.
          </p>
          <div className="mt-4">
            <Field
              label="Reason"
              hint="Recorded in both audit logs and shown to the customer. Required."
            >
              <input
                className={inputClass}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                maxLength={500}
                required
              />
            </Field>
          </div>
        </Confirm>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * The page
 * ---------------------------------------------------------------------- */

function Detail() {
  const params = useSearchParams();
  const slug = params.get("org");
  // The list route is the one that can find a tenant by slug, and it is the
  // only read this page needs. A second detail route would be a second query
  // to keep in step with this screen.
  const state = useTenant(slug ?? "");

  if (!slug) {
    return (
      <Page title="Tenant">
        <Card>
          <Empty title="No organization named">
            This page needs an organization in its address. Open a tenant from the tenant list
            rather than typing the address by hand.
          </Empty>
        </Card>
      </Page>
    );
  }

  return (
    <Loaded state={state} skeleton={<Page title={slug}><TableSkeleton rows={3} cols={2} /></Page>}>
      {(page) => {
        const tenant = page.rows.find((t) => t.slug === slug) ?? null;
        if (!tenant) {
          return (
            <Page title={slug}>
              <Card>
                <Empty title="No such organization">
                  Nothing on this installation has the slug {slug}. It may have been deleted, or
                  the address may be mistyped.
                </Empty>
              </Card>
            </Page>
          );
        }

        return (
          <Page
            title={tenant.name}
            lede={
              <>
                <span className="font-mono">{tenant.slug}</span> on the {tenant.plan} plan
              </>
            }
            actions={
              tenant.suspended ? <Badge tone="fail">suspended</Badge> : <Badge tone="pass">active</Badge>
            }
          >
            <div className="grid gap-5 lg:grid-cols-[1fr_320px]">
              <Card title="Account">
                <dl className="grid gap-0">
                  <Fact label="Members" value={tenant.members.toLocaleString()} numeric />
                  <Fact
                    label="Environments"
                    value={tenant.environments.toLocaleString()}
                    numeric
                    hint="Not torn down"
                  />
                  <Fact label="Plan" value={tenant.plan} />
                  <Fact label="Created" value={<When value={tenant.createdAt} />} />
                  {tenant.suspendedReason ? (
                    <Fact label="Suspended because" value={tenant.suspendedReason} />
                  ) : null}
                </dl>
              </Card>

              <Card title="Operator actions">
                <div className="px-4 py-4">
                  {tenant.suspended ? (
                    <p className="mb-3 text-[13px] leading-6 text-muted">
                      This organization is suspended. {SUSPEND_EFFECT}
                    </p>
                  ) : (
                    <p className="mb-3 text-[13px] leading-6 text-muted">
                      Suspending stops new work and leaves what is running alone.
                    </p>
                  )}
                  <SuspendControls
                    orgId={tenant.id}
                    name={tenant.name}
                    suspended={tenant.suspended}
                    onChanged={state.reload}
                  />
                </div>
              </Card>
            </div>
          </Page>
        );
      }}
    </Loaded>
  );
}

/** One row of the account facts. A definition list rather than a table,
 *  because this is one record and a table of one row is a table pretending. */
function Fact({
  label,
  value,
  numeric = false,
  hint,
}: {
  label: string;
  value: React.ReactNode;
  numeric?: boolean;
  hint?: string;
}) {
  return (
    // min-w-0 on the row and break-words on the value, and this is a real
    // defect rather than defensive styling.
    //
    // A suspension reason is 500 characters of whatever an operator pasted,
    // and what they paste is an abuse-report URL, a ticket link, a uuid or an
    // evidence hash: one unbreakable token. With overflow-wrap at its default
    // `normal` that token cannot break, so it forces this flex row to its own
    // width, and every SIBLING row is justify-between, so all their values are
    // pushed off-screen together.
    //
    // What makes it worse than a layout bug is that the page reports
    // scrollWidth === clientWidth, so there is NO horizontal scrollbar. An
    // operator triaging a suspended tenant sees Members, Environments, Plan
    // and Created with labels and no values, and reads that as "this tenant
    // has no data" rather than as clipping. Measured at 390px: eleven elements
    // computing 1747px wide, values pushed to x=1768.
    <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-rule px-4 py-3 last:border-b-0">
      <dt className="text-[12.5px] text-muted">
        {label}
        {hint ? <span className="ml-1.5 text-[11.5px] text-dim">{hint}</span> : null}
      </dt>
      {/* tnum on the figures so Members and Environments line up with each
          other down the card rather than drifting by digit width. */}
      <dd
        className={`min-w-0 break-words text-[13.5px] text-ink ${numeric ? "tnum tabular-nums" : ""}`}
      >
        {value}
      </dd>
    </div>
  );
}

export default function AdminTenantPage() {
  return (
    <Suspense fallback={<Page title="Tenant"><TableSkeleton rows={3} cols={2} /></Page>}>
      <Detail />
    </Suspense>
  );
}
