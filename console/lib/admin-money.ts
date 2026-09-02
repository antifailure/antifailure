"use client";

/**
 * The money lane's client, beside `lib/admin.ts` rather than inside it.
 *
 * Same reason that file gives for not being part of `api.ts`: the transport is
 * reused and nothing else is. `query`, `mutate` and `useApi` come from there
 * unchanged, so the error shape, the credentials mode and the CSRF header have
 * one definition. What lives here is only this lane's shapes and its calls, so
 * a lane can be read, reviewed and changed without touching the file every
 * other lane also imports.
 */

import { mutate, query, useApi } from "@/lib/api";

/** Minor units, as the provider counts them, beside their currency.
 *
 *  Never rendered as a bare number anywhere in this lane: "500" is five dollars
 *  or five hundred yen and the difference is a hundredfold. The formatter is
 *  the same list of zero-decimal currencies the server keeps, because a screen
 *  that disagreed with the confirmation it just showed would be worse than
 *  either being wrong alone. */
const ZERO_DECIMAL = new Set([
  "BIF", "CLP", "DJF", "GNF", "JPY", "KMF", "KRW", "MGA", "PYG",
  "RWF", "UGX", "VND", "VUV", "XAF", "XOF", "XPF",
]);

export function money(minor: number | null | undefined, currency: string | null | undefined): string {
  if (minor === null || minor === undefined || !Number.isFinite(minor)) return "--";
  const code = (currency ?? "usd").toUpperCase();
  const value = ZERO_DECIMAL.has(code) ? minor : minor / 100;
  try {
    return value.toLocaleString(undefined, { style: "currency", currency: code });
  } catch {
    // A code Intl has never heard of. The number beside the code is worse
    // looking and still unambiguous, which is the property that matters on a
    // screen about somebody's money.
    return `${value} ${code}`;
  }
}

export interface AdminSubscription {
  id: string;
  status: string;
  priceId: string | null;
  quantity: number;
  currentPeriodEnd: string | null;
  cancelAtPeriodEnd: boolean;
}

export interface AdminInvoice {
  id: string;
  number: string | null;
  status: string;
  amountDue: number;
  amountPaid: number;
  currency: string;
  hostedInvoiceUrl: string | null;
  periodEnd: string | null;
}

export interface AdminCharge {
  id: string;
  amount: number;
  amountRefunded: number;
  currency: string;
  status: string;
  refunded: boolean;
  disputed: boolean;
  created: string | null;
  failureMessage: string | null;
}

export interface AdminOperation {
  idempotency_key: string;
  action: string;
  target_type: string;
  target_id: string | null;
  actor_label: string;
  reason: string;
  state: "in_flight" | "succeeded" | "failed";
  amount_minor: string | null;
  currency: string | null;
  provider_object_id: string | null;
  error_message: string | null;
  started_at: string;
  finished_at: string | null;
}

export interface AdminBilling {
  org: { id: string; slug: string; plan: string };
  takesPayment: boolean;
  customer: {
    id: string;
    email: string | null;
    /** Already flipped out of the provider's sign by the server, so no screen
     *  has to know that a negative balance means credit. */
    creditMinor: number;
    owedMinor: number;
    currency: string | null;
    delinquent: boolean;
    discountCoupon: string | null;
  } | null;
  subscriptions: AdminSubscription[];
  invoices: AdminInvoice[];
  charges: AdminCharge[];
  operations: AdminOperation[];
}

export function useAdminBilling(orgId: string | null) {
  return useApi<AdminBilling | null>(
    () => (orgId ? query<AdminBilling>("admin.billing.customer", { orgId }) : Promise.resolve(null)),
    [orgId],
  );
}

/**
 * One key per form, minted when the form OPENS.
 *
 * This is the double-click guarantee, and it has to be made here rather than
 * per press: a key generated at press time is a different key on the second
 * press, which is exactly the case it exists to collapse. The server derives
 * one from the parameters when this is absent, which also collapses a double
 * click, but only for two requests that are byte-identical. A key held by the
 * form collapses them even if the operator edited the reason between presses.
 */
export function newIdempotencyKey(): string {
  return `af-admin-${crypto.randomUUID()}`;
}

export interface MoneyResult {
  replayed: boolean;
  idempotencyKey: string;
  providerObjectId: string | null;
}

export async function moneyAction<T extends MoneyResult>(
  path: string,
  input: Record<string, unknown>,
  csrf: string,
): Promise<T> {
  return mutate<T>(path, input, csrf);
}
