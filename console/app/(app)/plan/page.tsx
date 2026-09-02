"use client";

import { useState } from "react";
import { mutate, query, useApi } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import {
  Badge,
  Button,
  Card,
  Empty,
  Field,
  Loaded,
  Page,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
  inputClass,
  toneFor,
} from "@/components/ui";

interface Verdict {
  allowed: boolean;
  current: number;
  limit: number;
  reason: string;
}

interface Plan {
  name: string;
  current: boolean;
  quota: { environments: number; goldens: number; artifactGigabytes: number };
  environments: Verdict;
  goldens: Verdict;
}

interface Override {
  scope: "global" | "organization" | "project" | "user";
  reason: string;
  ticket: string | null;
  grantedBy: string;
  grantedAt: string;
  expiresAt: string | null;
}

interface Entitlement {
  key: string;
  value: number | boolean;
  /** What the plan alone would give. Shown struck through beside an override,
   *  so nobody has to open the pricing page to see what was changed. */
  planValue: number | boolean;
  unit: string | null;
  description: string;
  override: Override | null;
}

interface PlanState {
  plan: string;
  plans: Plan[];
  holding: { environments: number; goldens: number };
  entitlements: Entitlement[];
  takesPayment: boolean;
  hostedRequiredPlan: string | null;
  /** Whether billing.set would do anything. False unless whoever runs this
   *  control plane has said they set plans by hand, so the change control below
   *  is drawn only where pressing it works. */
  operatorSetsPlan: boolean;
}

interface SubscriptionState {
  id: string;
  plan: string;
  status: string;
  quantity: number;
  currentPeriodEnd: string | null;
  cancelAtPeriodEnd: boolean;
}

interface BillingState {
  plan: string;
  customer: { email: string | null } | null;
  subscription: SubscriptionState | null;
  liveSubscriptions: number;
  paymentMethod: {
    brand: string | null;
    last4: string | null;
    expMonth: number | null;
    expYear: number | null;
  } | null;
  configured: boolean;
  plans: string[];
  hostedRequiredPlan: string | null;
}

interface Invoice {
  stripe_invoice_id: string;
  number: string | null;
  status: string;
  amount_due: string | number;
  amount_paid: string | number;
  currency: string;
  hosted_invoice_url: string | null;
  created_at: string;
}

interface PageState {
  quota: PlanState;
  billing: BillingState;
  invoices: Invoice[];
}

function money(amount: string | number, currency: string): string {
  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: currency.toUpperCase(),
  }).format(Number(amount) / 100);
}

function Billing() {
  const session = useSessionContext();
  const state = useApi<PageState>(async () => {
    const [quota, billing, invoicePage] = await Promise.all([
      query<PlanState>("billing.get"),
      query<BillingState>("subscriptions.current"),
      query<{ invoices: Invoice[] }>("subscriptions.invoices", { limit: 20 }),
    ]);
    return { quota, billing, invoices: invoicePage.invoices };
  }, []);
  const csrf = session.data?.csrfToken ?? "";
  const mayManage = may(session.data?.role, "billing.manage");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [seats, setSeats] = useState(1);

  if (!mayManage) {
    return (
      <Card title="Plan">
        <Empty title="Your role cannot see this">
          The plan decides this organization&rsquo;s quotas, and changing it needs
          the billing.manage permission, which only an owner holds.
        </Empty>
      </Card>
    );
  }

  async function act(name: string, action: () => Promise<void>) {
    setBusy(name);
    setError(null);
    try {
      await action();
      state.reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That did not work.");
    } finally {
      setBusy(null);
    }
  }

  function choose(plan: string) {
    return act(`choose-${plan}`, async () => {
      await mutate("billing.set", { plan }, csrf);
    });
  }

  function checkout(plan: string) {
    return act(`checkout-${plan}`, async () => {
      const result = await mutate<{ url: string }>(
        "subscriptions.checkout",
        {
          plan,
          seats,
          successUrl: `${window.location.origin}/plan?checkout=success`,
          cancelUrl: `${window.location.origin}/plan`,
        },
        csrf,
      );
      window.location.assign(result.url);
    });
  }

  function portal() {
    return act("portal", async () => {
      const result = await mutate<{ url: string }>(
        "subscriptions.portal",
        { returnUrl: `${window.location.origin}/plan` },
        csrf,
      );
      window.location.assign(result.url);
    });
  }

  function reconcile() {
    return act("reconcile", async () => {
      await mutate("subscriptions.reconcile", {}, csrf);
    });
  }

  function cancel() {
    if (!window.confirm("Cancel this subscription at the end of its paid period?")) return;
    return act("cancel", async () => {
      await mutate("subscriptions.cancel", { reason: "cancelled in the console" }, csrf);
    });
  }

  return (
    <Loaded state={state} skeleton={<TableSkeleton rows={3} cols={4} />}>
      {(data) => (
        <div className="space-y-6">
          <Card
            title="Plan and billing"
            note={
              data.billing.configured
                ? "Stripe holds the card and computes plan changes. This control plane stores only the subscription state and card metadata."
                : data.quota.operatorSetsPlan
                  ? "This installation takes no payment. A plan change here only changes local quotas."
                  : "This installation takes no payment, and its plan is set by whoever runs it."
            }
            actions={
              error ? (
                <span
                  role="alert"
                  className="min-w-0 max-w-[46ch] break-words text-left text-[12px] leading-4 text-fail sm:text-right"
                >
                  {error}
                </span>
              ) : null
            }
          >
            <dl className="grid gap-x-8 gap-y-4 px-4 py-4 sm:grid-cols-2 lg:grid-cols-4">
              <div>
                <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Current plan</dt>
                <dd className="mt-1 text-[13px] text-ink">
                  <Badge tone="pass">{data.billing.plan}</Badge>
                </dd>
              </div>
              <div>
                <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Environments</dt>
                <dd className="tnum mt-1 text-[13px] text-ink">{data.quota.holding.environments}</dd>
              </div>
              <div>
                <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Goldens</dt>
                <dd className="tnum mt-1 text-[13px] text-ink">{data.quota.holding.goldens}</dd>
              </div>
              <div>
                <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Subscription</dt>
                <dd className="mt-1 text-[13px] text-ink">
                  {data.billing.subscription ? (
                    <Badge tone={toneFor(data.billing.subscription.status)}>
                      {data.billing.subscription.status}
                    </Badge>
                  ) : (
                    <span className="text-dim">None</span>
                  )}
                </dd>
              </div>
            </dl>
          </Card>


          <Entitlements entitlements={data.quota.entitlements} plan={data.quota.plan} />

          {data.billing.configured ? (
            <Card
              title="Subscription"
              note={data.billing.hostedRequiredPlan
                ? `This hosted control plane requires the ${data.billing.hostedRequiredPlan} plan.`
                : "Choose the first plan here. Use Stripe's portal for later plan and payment changes."}
              actions={
                <Button busy={busy === "reconcile"} onClick={reconcile}>
                  {busy === "reconcile" ? "Refreshing" : "Refresh from Stripe"}
                </Button>
              }
            >
              {data.billing.subscription ? (
                <div className="grid gap-5 px-4 py-4 sm:grid-cols-2 lg:grid-cols-4">
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.08em] text-dim">Plan</p>
                    <p className="mt-1 text-[13px] text-ink">{data.billing.subscription.plan}</p>
                  </div>
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.08em] text-dim">Seats</p>
                    <p className="tnum mt-1 text-[13px] text-ink">{data.billing.subscription.quantity}</p>
                  </div>
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.08em] text-dim">
                      {data.billing.subscription.cancelAtPeriodEnd ? "Ends" : "Renews"}
                    </p>
                    <p className="mt-1 text-[13px] text-ink">
                      <When value={data.billing.subscription.currentPeriodEnd} />
                    </p>
                  </div>
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.08em] text-dim">Payment method</p>
                    <p className="mt-1 text-[13px] text-ink">
                      {data.billing.paymentMethod?.last4
                        ? `${data.billing.paymentMethod.brand ?? "card"} ending ${data.billing.paymentMethod.last4}`
                        : "Managed in Stripe"}
                    </p>
                  </div>
                  <div className="flex flex-wrap gap-2 sm:col-span-2 lg:col-span-4">
                    <Button variant="primary" busy={busy === "portal"} onClick={portal}>
                      {busy === "portal" ? "Opening" : "Manage in Stripe"}
                    </Button>
                    {!data.billing.subscription.cancelAtPeriodEnd ? (
                      <Button variant="danger" busy={busy === "cancel"} onClick={cancel}>
                        {busy === "cancel" ? "Cancelling" : "Cancel at period end"}
                      </Button>
                    ) : null}
                  </div>
                </div>
              ) : (
                <div className="px-4 py-4">
                  <div className="max-w-60">
                    <Field label="Seats" hint="The quantity on the Stripe subscription.">
                      <input
                        className={inputClass}
                        type="number"
                        min={1}
                        max={1000}
                        step={1}
                        value={seats}
                        onChange={(event) => setSeats(Math.max(1, Number(event.target.value) || 1))}
                      />
                    </Field>
                  </div>
                  <div className="mt-5 flex flex-wrap gap-2">
                    {data.billing.plans.map((plan, index) => (
                      <Button
                        key={plan}
                        variant={index === 0 ? "primary" : "secondary"}
                        busy={busy === `checkout-${plan}`}
                        onClick={() => checkout(plan)}
                      >
                        {busy === `checkout-${plan}` ? "Opening checkout" : `Subscribe to ${plan}`}
                      </Button>
                    ))}
                  </div>
                </div>
              )}
            </Card>
          ) : null}

          <Card
            title="What each plan allows"
            note="A plan that is already over its limit refuses the next environment and removes nothing that exists."
          >
            <TableWrap>
              <Table>
                <thead>
                  <tr>
                    <Th>Plan</Th>
                    <Th numeric>Environments</Th>
                    <Th numeric>Goldens</Th>
                    <Th numeric>Artifacts</Th>
                    <Th>Room</Th>
                    <Th>Choose</Th>
                  </tr>
                </thead>
                <tbody>
                  {data.quota.plans
                    .filter((p) =>
                      !data.billing.hostedRequiredPlan ||
                      p.name === "free" ||
                      p.name === data.billing.hostedRequiredPlan,
                    )
                    .map((p) => (
                    <Row key={p.name}>
                      <Td>
                        {p.name}
                        {p.current ? <span className="ml-2 text-dim">current</span> : null}
                      </Td>
                      <Td label="Environments" numeric>{p.quota.environments}</Td>
                      <Td label="Goldens" numeric>{p.quota.goldens}</Td>
                      <Td label="Artifacts" numeric>{p.quota.artifactGigabytes} GiB</Td>
                      <Td label="Room">
                        {p.environments.allowed ? (
                          <Badge tone="pass">room for more</Badge>
                        ) : (
                          <Badge tone="warn">
                            {p.environments.current} of {p.environments.limit} held
                          </Badge>
                        )}
                      </Td>
                      <Td label="Choose">
                        {p.current ? (
                            <span className="text-dim">Current</span>
                        ) : data.billing.configured ? (
                          <span className="text-dim">
                            {data.billing.plans.includes(p.name) ? "Use checkout" : "Not offered here"}
                          </span>
                        ) : data.quota.operatorSetsPlan ? (
                          <Button busy={busy === `choose-${p.name}`} onClick={() => choose(p.name)}>
                            {busy === `choose-${p.name}` ? "Changing" : `Move to ${p.name}`}
                          </Button>
                        ) : (
                          <span className="text-dim">Ask the operator</span>
                        )}
                      </Td>
                    </Row>
                    ))}
                </tbody>
              </Table>
            </TableWrap>
          </Card>

          {data.billing.configured ? (
            <Card title="Invoices" note="Newest first. The rendered invoice stays at Stripe.">
              {data.invoices.length === 0 ? (
                <Empty title="No invoices yet">
                  An invoice appears here after Stripe creates one for this organization.
                </Empty>
              ) : (
                <TableWrap>
                  <Table>
                    <thead>
                      <tr>
                        <Th>Invoice</Th>
                        <Th>Status</Th>
                        <Th numeric>Amount</Th>
                        <Th>Created</Th>
                        <Th>Document</Th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.invoices.map((invoice) => (
                        <Row key={invoice.stripe_invoice_id}>
                          <Td label="Invoice" mono>{invoice.number ?? invoice.stripe_invoice_id}</Td>
                          <Td label="Status">
                            <Badge tone={toneFor(invoice.status)}>{invoice.status}</Badge>
                          </Td>
                          <Td label="Amount" numeric>
                            {money(
                              Number(invoice.amount_paid) > 0
                                ? invoice.amount_paid
                                : invoice.amount_due,
                              invoice.currency,
                            )}
                          </Td>
                          <Td label="Created"><When value={invoice.created_at} /></Td>
                          <Td label="Document">
                            {invoice.hosted_invoice_url ? (
                              <a
                                href={invoice.hosted_invoice_url}
                                className="inline-flex min-h-11 items-center underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink sm:min-h-0"
                              >
                                Open invoice
                              </a>
                            ) : (
                              <span className="text-dim">Unavailable</span>
                            )}
                          </Td>
                        </Row>
                      ))}
                    </tbody>
                  </Table>
                </TableWrap>
              )}
            </Card>
          ) : null}
        </div>
      )}
    </Loaded>
  );
}

export default function PlanPage() {
  return (
    <Page
      title="Plan"
      lede="Subscription, invoices, and the quotas this organization may hold at once."
    >
      <Billing />
    </Page>
  );
}

/**
 * What this organization is entitled to, and which of it is not the plan.
 *
 * The whole point of the card is the second column. An override that renders
 * as an ordinary number is a limit nobody can explain: the customer's finance
 * department reads it, compares it with the pricing page, and opens a ticket
 * asking which one is a mistake. So a grant is marked, the plan's own value is
 * shown struck through beside it, and the reason and the expiry are on the
 * row rather than behind a hover, because a phone cannot hover.
 */
function Entitlements({ entitlements, plan }: { entitlements: Entitlement[]; plan: string }) {
  const granted = entitlements.filter((e) => e.override !== null).length;
  return (
    <Card
      title="Limits"
      note={
        granted === 0
          ? `Every limit below is the ${plan} plan's own.`
          : granted === 1
            ? "One of these limits was set for this organization rather than by the plan."
            : `${granted} of these limits were set for this organization rather than by the plan.`
      }
    >
      {entitlements.length === 0 ? (
        <Empty title="No limits to show">
          The control plane did not report any. Reload, and if it stays empty the plan it is
          reading may be one it does not have limits for.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                <Th>Limit</Th>
                <Th numeric>Applies</Th>
                <Th numeric>On the {plan} plan</Th>
                <Th>Why</Th>
              </tr>
            </thead>
            <tbody>
              {entitlements.map((e) => (
                <Row key={e.key}>
                  <Td>
                    <span className="font-medium text-ink">{label(e.key)}</span>
                    <span className="mt-0.5 block max-w-[42ch] text-[12px] leading-5 text-dim">
                      {e.description}
                    </span>
                  </Td>
                  {/* nowrap on both number columns, and ONLY from sm up.
                      A limit that wraps puts a strikethrough across two lines,
                      which stops reading as "replaced" and starts reading as a
                      rendering fault; the table scrolls on its own if that
                      makes it too wide, which is the better failure.
                      Below sm the table stacks into a two column grid whose
                      first column is 10.5ch, and an unwrappable "ON THE FREE
                      PLAN" overflows it and lands on top of its own value. The
                      breakpoint is the same 640px the stacking media query in
                      globals.css uses, so the two cannot disagree. */}
                  <Td label="Applies" numeric className="sm:whitespace-nowrap">
                    <span className="font-medium">{value(e.value, e.unit)}</span>
                  </Td>
                  {/* The plan's own number, kept even when it is the same, so the
                      two columns can be read straight down rather than the eye
                      having to work out which rows are missing one. */}
                  <Td label={`On the ${plan} plan`} numeric className="sm:whitespace-nowrap">
                    {e.override === null ? (
                      <span className="text-dim">Same</span>
                    ) : (
                      <span className="text-muted line-through">{value(e.planValue, e.unit)}</span>
                    )}
                  </Td>
                  <Td label="Why">
                    {e.override === null ? (
                      <span className="text-dim">The plan</span>
                    ) : (
                      <div className="max-w-[46ch] space-y-1">
                        <Badge tone="warn">Override</Badge>
                        <p className="text-[12px] leading-5 text-ink">{e.override.reason}</p>
                        <p className="text-[12px] leading-5 text-dim">
                          Set by {e.override.grantedBy} <When value={e.override.grantedAt} />
                          {e.override.expiresAt ? (
                            <>
                              {" "}
                              &middot; ends <When value={e.override.expiresAt} />
                            </>
                          ) : null}
                          {e.override.ticket ? <> &middot; {e.override.ticket}</> : null}
                        </p>
                      </div>
                    )}
                  </Td>
                </Row>
              ))}
            </tbody>
          </Table>
        </TableWrap>
      )}
    </Card>
  );
}

/** `perRunHours` as a person reads it. Written out rather than de-camel-cased
 *  by a regular expression, because "Per day hours" is not what anybody calls
 *  it and a limit somebody is being refused by has to be nameable. */
function label(key: string): string {
  const names: Record<string, string> = {
    environments: "Live environments",
    goldens: "Golden snapshots",
    artifactGigabytes: "Artifact storage",
    perRunHours: "Longest single run",
    perDayHours: "Environment time per day",
    seats: "Seats",
    apiRateMultiplier: "API rate",
    retentionDays: "History kept",
  };
  return names[key] ?? key;
}

/** A limit with its unit. A bare integer in a table of limits is a number
 *  whose meaning the reader has to guess, and two of these are hours. */
function value(v: number | boolean, unit: string | null): string {
  if (typeof v === "boolean") return v ? "Yes" : "No";
  const n = v.toLocaleString();
  if (!unit) return n;
  // A multiplier is written against the number, not beside it: "1 x" reads as
  // a number and a stray letter, "1x" reads as a multiplier.
  return unit === "x" ? `${n}x` : `${n} ${unit}`;
}
