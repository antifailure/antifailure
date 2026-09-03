"use client";

import { useState, type ReactNode } from "react";
import {
  Badge,
  Button,
  Card,
  Confirm,
  Field,
  Loaded,
  TableSkeleton,
  When,
  inputClass,
  selectClass,
} from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  Facts,
  FilterBar,
  MetricRow,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import { operatorMay, useAdminContext, useTenants, type Tenant } from "@/lib/admin";
import {
  money,
  moneyAction,
  newIdempotencyKey,
  useAdminBilling,
  type AdminBilling,
  type AdminCharge,
  type AdminInvoice,
  type AdminOperation,
  type AdminSubscription,
} from "@/lib/admin-money";
import type { ApiError } from "@/lib/api";

/**
 * One customer's money, read from Stripe rather than from a copy of it.
 *
 * WHAT AN OPERATOR OPENS THIS TO ANSWER: why was this customer charged that,
 * what are they on, what did they owe, and what has anybody here already done
 * about it. The last of those four is the one no provider dashboard can answer,
 * which is why the operator ledger is on this screen beside the provider's own
 * rows rather than somewhere else.
 *
 * READS GO TO THE PROVIDER, and that is the route's decision rather than this
 * page's: `admin.billing.customer` reads Stripe live and not the local mirror,
 * because on the one occasion the two disagree the mirror is the one that is
 * wrong and the operator is the person least able to tell. What IS local is the
 * seat and entitlement summary, which exists before anybody has checked out and
 * is usually the half a support call is actually about.
 *
 * EVERY WRITE IS ONE COMPONENT. Ten money actions with ten bespoke forms is ten
 * places to forget the reason field, the idempotency key, or the confirmation.
 * `MoneyButton` below is the one place all three are arranged, so an action
 * added later gets them by construction.
 */
export default function CustomersBillingPage() {
  const [search, setSearch] = useState("");
  const [org, setOrg] = useState<Tenant | null>(null);

  return (
    <AdminPage href="/admin/customers/billing">
      {org === null ? (
        <PickOrganization search={search} setSearch={setSearch} onPick={setOrg} />
      ) : (
        <Billing org={org} onClear={() => setOrg(null)} />
      )}
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Choosing whose money
 * ---------------------------------------------------------------------- */

function PickOrganization({
  search,
  setSearch,
  onPick,
}: {
  search: string;
  setSearch: (s: string) => void;
  onPick: (t: Tenant) => void;
}) {
  const state = useTenants(search);
  const columns: Column<Tenant>[] = [
    {
      key: "name",
      header: "Organization",
      cell: (t) => (
        <>
          <span className="block truncate font-medium text-ink">{t.name}</span>
          <span className="block truncate font-mono text-[12px] text-muted">{t.slug}</span>
        </>
      ),
    },
    { key: "plan", header: "Plan", cell: (t) => t.plan },
    { key: "members", header: "Members", numeric: true, cell: (t) => t.members.toLocaleString() },
    { key: "created", header: "Created", cell: (t) => <When value={t.createdAt} /> },
    { key: "open", header: "Open", cell: (t) => <Button onClick={() => onPick(t)}>Open</Button> },
  ];

  return (
    <Card title="Whose billing">
      <FilterBar
        search={{
          value: search,
          onChange: setSearch,
          label: "Search organizations by name or slug",
          placeholder: "Name or slug",
        }}
      />
      <Loaded state={state} skeleton={<TableSkeleton rows={5} cols={5} />}>
        {(rows) => (
          <DataTable
            columns={columns}
            rows={rows}
            keyOf={(t) => t.id}
            empty={
              <EmptyList title={search ? "No organization matches that" : "No organizations yet"}>
                {search
                  ? "Nothing on this installation has that name or slug. Clear the search to see every organization."
                  : "Nobody has created an organization here, so there is no billing to look at."}
              </EmptyList>
            }
            footer={
              <More
                shown={rows.length}
                noun={{ one: "organization", many: "organizations" }}
                hasMore={state.hasMore}
                busy={state.busy}
                error={state.moreError}
                onMore={state.more}
              />
            }
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The account
 * ---------------------------------------------------------------------- */

function Billing({ org, onClear }: { org: Tenant; onClear: () => void }) {
  const { me } = useAdminContext();
  const state = useAdminBilling(org.id);
  const mayWrite = operatorMay(me, "admin.billing.write");

  return (
    <div className="grid gap-5">
      <Loaded
        state={state}
        framed
        skeleton={
          <div className="grid gap-5">
            <TableSkeleton rows={2} cols={4} />
            <TableSkeleton rows={4} cols={5} />
          </div>
        }
      >
        {(data) =>
          data === null ? (
            <EmptyList title="No organization loaded">Pick one from the list.</EmptyList>
          ) : (
            <Account org={org} data={data} mayWrite={mayWrite} reload={state.reload} onClear={onClear} />
          )
        }
      </Loaded>
    </div>
  );
}

function Account({
  org,
  data,
  mayWrite,
  reload,
  onClear,
}: {
  org: Tenant;
  data: AdminBilling;
  mayWrite: boolean;
  reload: () => void;
  onClear: () => void;
}) {
  const currency = data.customer?.currency ?? "usd";
  const seats = data.summary.seats;

  return (
    <div className="grid gap-5">
      <Card
        title={org.name}
        note={org.slug}
        actions={
          <span className="flex flex-wrap gap-2">
            <Button onClick={reload}>Refresh</Button>
            <Button onClick={onClear}>Back to the list</Button>
          </span>
        }
      >
        <Facts
          facts={[
            {
              label: "Plan on the account",
              value: data.org.plan,
              // The column entitlement checks run against, which is not always
              // what the provider last confirmed. Both are shown, labelled
              // differently, because when they disagree that IS the answer.
            },
            {
              label: "Plan at the provider",
              value: data.summary.subscription
                ? `${data.summary.subscription.plan} (${data.summary.subscription.status})`
                : null,
            },
            {
              label: "Takes payment",
              value: data.takesPayment ? (
                <Badge tone="pass">Stripe configured</Badge>
              ) : (
                <Badge tone="warn">no Stripe configuration</Badge>
              ),
            },
            { label: "Stripe customer", value: data.customer?.id ?? null, mono: true },
            { label: "Billing email", value: data.customer?.email ?? null },
            {
              label: "Delinquent",
              value: data.customer ? (
                data.customer.delinquent ? (
                  <Badge tone="fail">yes</Badge>
                ) : (
                  <Badge tone="pass">no</Badge>
                )
              ) : null,
            },
            { label: "Discount", value: data.customer?.discountCoupon ?? null, mono: true },
          ]}
        />
      </Card>

      <MetricRow
        metrics={[
          {
            label: "Credit",
            value: data.customer ? money(data.customer.creditMinor, currency) : null,
            note: "What they may spend before being charged",
          },
          {
            label: "Owed",
            value: data.customer ? money(data.customer.owedMinor, currency) : null,
            note: "Carried at the provider",
          },
          {
            label: "Seats used",
            value: seats.used,
            note:
              seats.limit === null
                ? `${seats.members} members, ${seats.openInvitations} open invitations, no limit`
                : `of ${seats.limit}${seats.atLimit ? ", at the limit" : ""}`,
          },
          {
            label: "Hand written grants",
            value: data.summary.overrides.length,
            note: "Live entitlement overrides on this organization",
          },
        ]}
      />

      {!data.takesPayment ? (
        <Card title="This installation has no Stripe configuration">
          <div className="px-4 py-4 text-[13px] leading-6 text-muted">
            <p>
              The subscription, invoice and charge tables below are empty because there is nowhere
              to read them from, not because this customer has none. Setting{" "}
              <span className="font-mono text-[12px]">AF_STRIPE_SECRET_KEY</span> and the price
              variables is what fills them.
            </p>
            <p className="mt-3">
              The seat and entitlement figures above are this deployment&apos;s own and are real
              either way.
            </p>
          </div>
        </Card>
      ) : data.customer === null ? (
        <Card title="This organization has never started a checkout">
          <div className="px-4 py-4 text-[13px] leading-6 text-muted">
            There is no Stripe customer for it, so there is nothing to refund, credit or cancel.
            The seat and entitlement figures above are still the real ones, and a hand written
            grant is how an organization gets more than its plan without paying.
          </div>
        </Card>
      ) : null}

      <Subscriptions data={data} mayWrite={mayWrite} reload={reload} />
      <Invoices data={data} mayWrite={mayWrite} reload={reload} />
      <Charges data={data} mayWrite={mayWrite} reload={reload} />
      <Grants data={data} />
      {mayWrite && data.customer ? <CreditCard data={data} reload={reload} /> : null}
      <Ledger data={data} />
    </div>
  );
}

/* -------------------------------------------------------------------------
 * The provider's rows
 * ---------------------------------------------------------------------- */

function Subscriptions({
  data,
  mayWrite,
  reload,
}: {
  data: AdminBilling;
  mayWrite: boolean;
  reload: () => void;
}) {
  const columns: Column<AdminSubscription>[] = [
    { key: "id", header: "Subscription", mono: true, cell: (s) => s.id },
    { key: "status", header: "Status", cell: (s) => <StatusChip value={s.status} /> },
    { key: "price", header: "Price", mono: true, cell: (s) => s.priceId ?? "--" },
    { key: "qty", header: "Seats", numeric: true, cell: (s) => s.quantity.toLocaleString() },
    {
      key: "period",
      header: "Period ends",
      cell: (s) => <When value={s.currentPeriodEnd} />,
    },
    {
      key: "cancel",
      header: "Cancelling",
      cell: (s) => (s.cancelAtPeriodEnd ? <Badge tone="warn">at period end</Badge> : "no"),
    },
    ...(mayWrite
      ? [
          {
            key: "actions",
            header: "Actions",
            cell: (s: AdminSubscription) => (
              <span className="flex flex-wrap gap-2">
                <MoneyButton
                  label="Change plan"
                  title={`Change the plan on ${s.id}`}
                  path="admin.billing.changePlan"
                  orgId={data.org.id}
                  base={{ subscriptionId: s.id }}
                  fields={[
                    {
                      name: "plan",
                      label: "Plan",
                      kind: "select",
                      options: ["team", "enterprise"],
                      initial: "team",
                    },
                    {
                      name: "prorate",
                      label: "Proration",
                      kind: "select",
                      options: ["yes", "no"],
                      initial: "yes",
                      hint: "Whether the provider charges or credits the difference now. Required by the route: there is no default, because the two answers are different amounts of money.",
                    },
                  ]}
                  onDone={reload}
                >
                  Moves them onto a different price at the provider. The plan column on the
                  organization is what quotas are derived from and is changed separately.
                </MoneyButton>
                <MoneyButton
                  label="Extend trial"
                  title={`Extend the trial on ${s.id}`}
                  path="admin.billing.extendTrial"
                  orgId={data.org.id}
                  base={{ subscriptionId: s.id }}
                  fields={[
                    { name: "until", label: "Trial ends", kind: "datetime", initial: "" },
                  ]}
                  onDone={reload}
                >
                  Pushes the end of the trial out. They are not charged until it passes.
                </MoneyButton>
                <MoneyButton
                  label="Discount"
                  title={`Apply a coupon to ${s.id}`}
                  path="admin.billing.discount"
                  orgId={data.org.id}
                  base={{ subscriptionId: s.id }}
                  fields={[
                    {
                      name: "coupon",
                      label: "Coupon",
                      kind: "text",
                      initial: "",
                      hint: "The coupon id as it exists at the provider. This screen cannot create one.",
                    },
                  ]}
                  onDone={reload}
                >
                  Attaches an existing coupon. It applies from the next invoice.
                </MoneyButton>
                {s.status === "canceled" || s.cancelAtPeriodEnd ? (
                  <MoneyButton
                    label="Reactivate"
                    title={`Reactivate ${s.id}`}
                    path="admin.billing.reactivate"
                    orgId={data.org.id}
                    base={{ subscriptionId: s.id }}
                    fields={[]}
                    onDone={reload}
                  >
                    Clears the pending cancellation so the subscription renews as normal.
                  </MoneyButton>
                ) : (
                  <MoneyButton
                    label="Cancel"
                    title={`Cancel ${s.id}`}
                    path="admin.billing.cancel"
                    orgId={data.org.id}
                    base={{ subscriptionId: s.id }}
                    fields={[]}
                    danger
                    onDone={reload}
                  >
                    Ends the subscription at the provider. What that does to their access depends
                    on the plan column on the organization, which this does not change.
                  </MoneyButton>
                )}
              </span>
            ),
          },
        ]
      : []),
  ];

  return (
    <Card title="Subscriptions" note="Read from Stripe, not from the local mirror.">
      <DataTable
        columns={columns}
        rows={data.subscriptions}
        keyOf={(s) => s.id}
        empty={
          <EmptyList title="No subscription">
            {data.takesPayment
              ? "This customer has no subscription at the provider. They are on whatever the organization's plan column says, which is what quotas are derived from."
              : "There is no Stripe configuration on this installation, so there is nowhere to read a subscription from."}
          </EmptyList>
        }
      />
    </Card>
  );
}

function Invoices({
  data,
  mayWrite,
  reload,
}: {
  data: AdminBilling;
  mayWrite: boolean;
  reload: () => void;
}) {
  const columns: Column<AdminInvoice>[] = [
    { key: "number", header: "Invoice", mono: true, cell: (i) => i.number ?? i.id },
    { key: "status", header: "Status", cell: (i) => <StatusChip value={i.status} /> },
    { key: "due", header: "Due", numeric: true, cell: (i) => money(i.amountDue, i.currency) },
    { key: "paid", header: "Paid", numeric: true, cell: (i) => money(i.amountPaid, i.currency) },
    { key: "period", header: "Period ends", cell: (i) => <When value={i.periodEnd} /> },
    {
      key: "hosted",
      header: "At the provider",
      cell: (i) =>
        i.hostedInvoiceUrl ? (
          <a
            className="inline-flex min-h-11 items-center underline decoration-[rgba(16,16,16,0.35)] underline-offset-4 sm:min-h-0"
            href={i.hostedInvoiceUrl}
            target="_blank"
            rel="noreferrer"
          >
            Open the invoice
          </a>
        ) : (
          "--"
        ),
    },
    ...(mayWrite
      ? [
          {
            key: "actions",
            header: "Actions",
            cell: (i: AdminInvoice) => (
              <span className="flex flex-wrap gap-2">
                <MoneyButton
                  label="Retry payment"
                  title={`Retry payment on ${i.number ?? i.id}`}
                  path="admin.billing.retryPayment"
                  orgId={data.org.id}
                  base={{ invoiceId: i.id }}
                  fields={[]}
                  onDone={reload}
                >
                  Asks the provider to charge the card on file again. It fails the same way it
                  failed last time if nothing about the card has changed.
                </MoneyButton>
                <MoneyButton
                  label="Resend"
                  title={`Resend ${i.number ?? i.id}`}
                  path="admin.billing.resendInvoice"
                  orgId={data.org.id}
                  base={{ invoiceId: i.id }}
                  fields={[]}
                  onDone={reload}
                >
                  Sends the invoice email again, to the address the provider holds.
                </MoneyButton>
              </span>
            ),
          },
        ]
      : []),
  ];

  return (
    <Card title="Invoices">
      <DataTable
        columns={columns}
        rows={data.invoices}
        keyOf={(i) => i.id}
        empty={
          <EmptyList title="No invoices">
            Nothing has been billed to this customer yet. An invoice appears here the first time
            the provider raises one.
          </EmptyList>
        }
      />
    </Card>
  );
}

function Charges({
  data,
  mayWrite,
  reload,
}: {
  data: AdminBilling;
  mayWrite: boolean;
  reload: () => void;
}) {
  const columns: Column<AdminCharge>[] = [
    { key: "id", header: "Charge", mono: true, cell: (ch) => ch.id },
    { key: "status", header: "Status", cell: (ch) => <StatusChip value={ch.status} /> },
    { key: "amount", header: "Amount", numeric: true, cell: (ch) => money(ch.amount, ch.currency) },
    {
      key: "refunded",
      header: "Refunded",
      numeric: true,
      cell: (ch) => money(ch.amountRefunded, ch.currency),
    },
    {
      key: "flags",
      header: "Flags",
      cell: (ch) => (
        <span className="flex flex-wrap gap-1.5">
          {ch.refunded ? <Badge tone="warn">refunded</Badge> : null}
          {ch.disputed ? <Badge tone="fail">disputed</Badge> : null}
          {!ch.refunded && !ch.disputed ? <span className="text-dim">none</span> : null}
        </span>
      ),
    },
    { key: "created", header: "Created", cell: (ch) => <When value={ch.created} /> },
    {
      key: "failure",
      header: "Why it failed",
      cell: (ch) => (
        <span className="block max-w-[36ch] break-words">{ch.failureMessage ?? "--"}</span>
      ),
    },
    ...(mayWrite
      ? [
          {
            key: "actions",
            header: "Actions",
            cell: (ch: AdminCharge) =>
              ch.refunded ? (
                <span className="text-dim">fully refunded</span>
              ) : (
                <MoneyButton
                  label="Refund"
                  title={`Refund ${ch.id}`}
                  path="admin.billing.refund"
                  orgId={data.org.id}
                  base={{ chargeId: ch.id }}
                  danger
                  fields={[
                    {
                      name: "amountMinor",
                      label: "Amount in minor units",
                      kind: "number",
                      initial: "",
                      hint: `Leave it empty to refund the whole charge, ${money(ch.amount, ch.currency)}. In minor units, so ${ch.currency.toUpperCase()} 1.00 is 100.`,
                    },
                    {
                      name: "category",
                      label: "Category",
                      kind: "select",
                      options: ["requested_by_customer", "duplicate", "fraudulent"],
                      initial: "requested_by_customer",
                      hint: "The provider's own reason codes. Marking something fraudulent has consequences at the provider beyond this refund.",
                    },
                  ]}
                  onDone={reload}
                >
                  Money leaves the account and does not come back. The refund is recorded in the
                  operator ledger below with your reason and cannot be undone from this screen.
                </MoneyButton>
              ),
          },
        ]
      : []),
  ];

  return (
    <Card title="Charges">
      <DataTable
        columns={columns}
        rows={data.charges}
        keyOf={(ch) => ch.id}
        empty={
          <EmptyList title="No charges">
            Nothing has been taken from this customer. A charge appears here the first time a
            payment succeeds or fails.
          </EmptyList>
        }
      />
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * This deployment's own rows
 * ---------------------------------------------------------------------- */

function Grants({ data }: { data: AdminBilling }) {
  type Grant = AdminBilling["summary"]["overrides"][number];
  const columns: Column<Grant>[] = [
    { key: "feature", header: "Feature", mono: true, cell: (g) => g.feature },
    { key: "scope", header: "Scope", cell: (g) => g.scope },
    { key: "value", header: "Granted", numeric: true, cell: (g) => String(g.value) },
    {
      key: "plan",
      header: "The plan alone",
      numeric: true,
      cell: (g) => String(g.planValue),
    },
    {
      key: "reason",
      header: "Reason",
      cell: (g) => <span className="block max-w-[36ch] break-words">{g.reason}</span>,
    },
    { key: "ticket", header: "Ticket", cell: (g) => g.ticket ?? "--" },
    { key: "by", header: "Granted by", cell: (g) => g.grantedBy },
    { key: "expires", header: "Expires", cell: (g) => <When value={g.expiresAt} /> },
  ];

  return (
    <Card
      title="Hand written grants"
      note="Entitlement overrides live right now. Revoked and expired ones are not shown, because the route leaves them out rather than this screen filtering them."
    >
      <DataTable
        columns={columns}
        rows={data.summary.overrides}
        keyOf={(g) => `${g.scope}:${g.feature}`}
        empty={
          <EmptyList title="No grants">
            This organization gets exactly what its plan says. A grant here is how it gets more
            without the plan changing, and it is issued from the entitlements surface.
          </EmptyList>
        }
      />
    </Card>
  );
}

function Ledger({ data }: { data: AdminBilling }) {
  const columns: Column<AdminOperation>[] = [
    { key: "action", header: "Action", cell: (o) => o.action.replace(/\./g, " ") },
    { key: "state", header: "State", cell: (o) => <StatusChip value={o.state} /> },
    {
      key: "amount",
      header: "Amount",
      numeric: true,
      cell: (o) => (o.amount_minor === null ? "--" : money(Number(o.amount_minor), o.currency)),
    },
    { key: "actor", header: "Operator", cell: (o) => o.actor_label },
    {
      key: "reason",
      header: "Reason",
      cell: (o) => <span className="block max-w-[40ch] break-words">{o.reason}</span>,
    },
    { key: "object", header: "At the provider", mono: true, cell: (o) => o.provider_object_id ?? "--" },
    {
      key: "error",
      header: "What went wrong",
      cell: (o) => (
        <span className="block max-w-[36ch] break-words">{o.error_message ?? "--"}</span>
      ),
    },
    { key: "started", header: "Started", cell: (o) => <When value={o.started_at} /> },
  ];

  return (
    <Card
      title="What operators have done here"
      note="This deployment's own ledger, which the provider has never heard of. The fifty most recent."
    >
      <DataTable
        columns={columns}
        rows={data.operations}
        keyOf={(o) => o.idempotency_key}
        empty={
          <EmptyList title="Nobody has moved money on this account">
            No refund, credit, plan change or cancellation has been made from this portal. Anything
            done here lands in this table with the reason its operator typed.
          </EmptyList>
        }
      />
    </Card>
  );
}

function CreditCard({ data, reload }: { data: AdminBilling; reload: () => void }) {
  const currency = data.customer?.currency ?? "usd";
  return (
    <Card title="Add credit">
      <div className="px-4 py-4">
        <p className="mb-3 max-w-[62ch] text-[13px] leading-6 text-muted">
          Credit is spent against future invoices at the provider. It is not a refund: nothing goes
          back to a card. Current credit is{" "}
          <span className="tnum font-medium text-ink">
            {money(data.customer?.creditMinor ?? 0, currency)}
          </span>
          .
        </p>
        <MoneyButton
          label="Add credit"
          title="Add credit to this customer"
          path="admin.billing.credit"
          orgId={data.org.id}
          base={{ currency }}
          fields={[
            {
              name: "amountMinor",
              label: "Amount in minor units",
              kind: "number",
              initial: "",
              hint: `In minor units, so ${currency.toUpperCase()} 1.00 is 100. The currency is the customer's own and is not chosen here.`,
            },
          ]}
          onDone={reload}
        >
          The amount is added to their balance at the provider and spent against their next
          invoice.
        </MoneyButton>
      </div>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * One money action
 * ---------------------------------------------------------------------- */

interface MoneyField {
  name: string;
  label: string;
  kind: "text" | "number" | "select" | "datetime";
  initial: string;
  hint?: string;
  options?: string[];
}

/**
 * A button that opens a confirmation, collects the action's own fields plus the
 * reason, and sends it once.
 *
 * THE IDEMPOTENCY KEY IS MINTED WHEN THE DIALOG OPENS, not when the button is
 * pressed, and that is the whole double press guarantee. A key generated at
 * press time is a DIFFERENT key on the second press, which is exactly the case
 * it exists to collapse. The server derives one from the parameters when none
 * is sent, which also collapses two byte identical requests; a key held by the
 * form collapses them even when the operator edited the reason in between.
 *
 * THE REASON IS NOT OPTIONAL and is checked here as well as at the route,
 * because the message differs: here it names the field while somebody is
 * looking at it, and there it names the action in a record read a year later.
 */
function MoneyButton({
  label,
  title,
  path,
  orgId,
  base,
  fields,
  children,
  danger = false,
  onDone,
}: {
  label: string;
  title: string;
  path: string;
  orgId: string;
  base: Record<string, unknown>;
  fields: MoneyField[];
  children: ReactNode;
  danger?: boolean;
  onDone: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [key, setKey] = useState<string | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  function start() {
    setValues(Object.fromEntries(fields.map((f) => [f.name, f.initial])));
    setReason("");
    setError(null);
    setKey(newIdempotencyKey());
    setOpen(true);
  }

  async function send() {
    if (reason.trim().length < 8) {
      setError("Say why, in at least eight characters. It is recorded against the money.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const input: Record<string, unknown> = { orgId, ...base, reason: reason.trim() };
      if (key) input.idempotencyKey = key;
      for (const f of fields) {
        const raw = values[f.name] ?? "";
        if (raw === "") continue;
        if (f.kind === "number") input[f.name] = Number(raw);
        else if (f.name === "prorate") input[f.name] = raw === "yes";
        else if (f.kind === "datetime") input[f.name] = new Date(raw).toISOString();
        else input[f.name] = raw;
      }
      const result = await moneyAction(path, input);
      setOpen(false);
      // `replayed` is the ledger saying this exact key had already run, which
      // is a success and not a failure. Saying so is the difference between an
      // operator trusting the double press guard and pressing again.
      setDone(
        result.replayed
          ? "Already done. That key had run before, so nothing happened a second time."
          : "Done. It is in the ledger below with your reason.",
      );
      onDone();
    } catch (err) {
      setError((err as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Button variant={danger ? "danger" : "secondary"} onClick={start}>
        {label}
      </Button>
      {done ? (
        <span role="status" className="block text-[12px] leading-5 text-muted">
          {done}
        </span>
      ) : null}

      <Confirm
        open={open}
        title={title}
        confirmLabel={label}
        busy={busy}
        error={error}
        onCancel={() => {
          setOpen(false);
          setError(null);
        }}
        onConfirm={() => void send()}
      >
        <p className="text-[13px] leading-6 text-muted">{children}</p>
        <div className="mt-4 grid gap-4">
          {fields.map((f) => (
            <div key={f.name}>
              {f.kind === "select" ? (
                <>
                  <label
                    htmlFor={`money-${f.name}`}
                    className="block text-[12px] font-medium text-muted"
                  >
                    {f.label}
                  </label>
                  <select
                    id={`money-${f.name}`}
                    className={`${selectClass} mt-1.5 w-full`}
                    value={values[f.name] ?? f.initial}
                    onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
                    aria-describedby={f.hint ? `money-${f.name}-hint` : undefined}
                  >
                    {(f.options ?? []).map((o) => (
                      <option key={o} value={o}>
                        {o.replace(/_/g, " ")}
                      </option>
                    ))}
                  </select>
                  {f.hint ? (
                    <span
                      id={`money-${f.name}-hint`}
                      className="mt-1.5 block text-[12px] leading-5 text-dim"
                    >
                      {f.hint}
                    </span>
                  ) : null}
                </>
              ) : (
                <Field label={f.label} hint={f.hint}>
                  <input
                    className={inputClass}
                    type={
                      f.kind === "number" ? "number" : f.kind === "datetime" ? "datetime-local" : "text"
                    }
                    value={values[f.name] ?? f.initial}
                    onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
                    // tabular figures on the amount fields, so a number typed
                    // into one lines up with the same number rendered in the
                    // table it came from.
                    inputMode={f.kind === "number" ? "numeric" : undefined}
                  />
                </Field>
              )}
            </div>
          ))}
          <Field
            label="Reason"
            hint="At least eight characters. Written into this deployment's own ledger beside the amount, and read by whoever asks about this later."
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
    </>
  );
}
