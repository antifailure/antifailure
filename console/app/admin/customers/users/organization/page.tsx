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
import { Facts, MetricRow } from "@/components/admin/primitives";
import { operatorMay, resumeTenant, suspendTenant, useAdminContext, useTenant } from "@/lib/admin";
import type { ApiError } from "@/lib/api";

/**
 * One tenant, and the two things an operator can do to it.
 *
 * A QUERY STRING rather than a dynamic /[slug] segment, because the console is a
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
            <MetricRow
              metrics={[
                { label: "Members", value: tenant.members },
                { label: "Environments", value: tenant.environments, note: "Not torn down" },
              ]}
            />

            <div className="mt-5 grid gap-5 lg:grid-cols-[1fr_320px]">
              <Card title="Account">
                {/* The shared description list rather than a bespoke one.
                    The defect it has to survive is real and was found here: a
                    suspension reason is 500 characters of whatever an operator
                    pasted, and what they paste is an abuse-report URL, a
                    ticket link, a uuid or an evidence hash, which is one
                    unbreakable token. The earlier flex row let that token
                    force its own width and push every sibling row's value off
                    screen, with scrollWidth equal to clientWidth so there was
                    no scrollbar to hint at it. An operator triaging a
                    suspended tenant saw four labels with no values and read it
                    as "this tenant has no data". `Facts` is a grid whose value
                    column is minmax(0, 1fr) with break-words on the value, so
                    the token wraps instead of widening anything. */}
                <Facts
                  facts={[
                    { label: "Plan", value: tenant.plan },
                    { label: "Created", value: <When value={tenant.createdAt} /> },
                    { label: "Suspended because", value: tenant.suspendedReason },
                  ]}
                />
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

export default function AdminTenantPage() {
  return (
    <Suspense fallback={<Page title="Tenant"><TableSkeleton rows={3} cols={2} /></Page>}>
      <Detail />
    </Suspense>
  );
}
