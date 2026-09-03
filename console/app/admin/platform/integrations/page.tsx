"use client";

import { useState } from "react";
import { Badge, Card, Loaded, TableSkeleton, When } from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  FilterBar,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import {
  deliveryStanding,
  useDeliveries,
  useInstallations,
  type AdminDelivery,
  type AdminInstallation,
} from "@/lib/admin-platform";

/**
 * What arrives here from GitHub and from Stripe, and whether it was handled.
 *
 * INBOUND ONLY, AND SAID SO ON THE PAGE. This product has no outbound webhook
 * subscription table, no delivery attempt table for one and no signing secret
 * store. What it has is two inbound ledgers. A screen titled "Webhooks" that
 * drew an outbound delivery log over an inbound table would answer the wrong
 * question confidently, which on an incident call is worse than answering none.
 *
 * WHAT AN OPERATOR OPENS THIS FOR: a customer says their pull request check
 * never appeared, or their plan did not change after they paid. Both are
 * usually a delivery that never arrived or one that arrived and was not
 * handled, and the difference between those two is the first thing to
 * establish.
 */
export default function PlatformIntegrationsPage() {
  return (
    <AdminPage
      href="/admin/platform/integrations"
      lede="The GitHub App installations on this instance, and every delivery that arrived from GitHub or Stripe."
    >
      <div className="space-y-5">
        <Installations />
        <Deliveries />
      </div>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Installations
 * ---------------------------------------------------------------------- */

function Installations() {
  const [search, setSearch] = useState("");
  const state = useInstallations(search);

  const columns: Column<AdminInstallation>[] = [
    {
      key: "account",
      header: "GitHub account",
      cell: (r) => (
        <>
          <span className="block truncate font-medium text-ink">{r.accountLogin}</span>
          <span className="block truncate text-[12px] text-muted">{r.accountType}</span>
        </>
      ),
    },
    {
      key: "org",
      header: "Organization",
      cell: (r) => <span className="font-mono text-[12px]">{r.orgSlug}</span>,
    },
    {
      key: "repositories",
      header: "Repositories",
      numeric: true,
      cell: (r) => r.repositories.toLocaleString(),
    },
    {
      key: "lastDelivery",
      header: "Last delivery",
      cell: (r) =>
        r.lastDeliveryAt === null ? (
          // The single most useful cell on this card. An installation that has
          // never delivered anything is an installation whose events are not
          // reaching us at all, which is a different problem from one whose
          // deliveries arrive and fail.
          <span className="text-warn">Nothing has ever arrived</span>
        ) : (
          <When value={r.lastDeliveryAt} />
        ),
    },
    {
      key: "standing",
      header: "Standing",
      cell: (r) =>
        r.suspended ? <Badge tone="fail">suspended</Badge> : <Badge tone="pass">installed</Badge>,
    },
  ];

  return (
    <Card
      title="GitHub App installations"
      note="An organization with no installation receives nothing, whatever else is configured."
    >
      <FilterBar
        search={{
          value: search,
          onChange: setSearch,
          label: "Search installations by GitHub account or organization",
          placeholder: "Account or organization",
        }}
      />
      <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={5} />}>
        {(rows) => (
          <DataTable
            columns={columns}
            rows={rows}
            keyOf={(r) => r.id}
            empty={
              <EmptyList
                title={search ? "No installation matches that" : "The app is not installed anywhere"}
              >
                {search
                  ? "No GitHub account or organization on this installation matches that. Clear the search to see every one."
                  : "No customer has installed the GitHub App yet, so no pull request event can reach this control plane. Installing it is the customer's action, taken from their own console."}
              </EmptyList>
            }
            footer={
              <More
                shown={rows.length}
                noun={{ one: "installation", many: "installations" }}
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
 * Deliveries
 * ---------------------------------------------------------------------- */

function Deliveries() {
  const [source, setSource] = useState("github");
  const [unhandled, setUnhandled] = useState("");
  const [search, setSearch] = useState("");
  const state = useDeliveries(source, unhandled === "unhandled", search);

  const columns: Column<AdminDelivery>[] = [
    {
      key: "event",
      header: "Event",
      cell: (r) => (
        <>
          <span className="block truncate font-medium text-ink">{r.event}</span>
          {r.action ? (
            <span className="block truncate text-[12px] text-muted">{r.action}</span>
          ) : null}
        </>
      ),
    },
    {
      key: "org",
      header: "Organization",
      cell: (r) =>
        r.orgSlug === null ? (
          // Deliberately kept and deliberately worded. The column is nullable
          // because a delivery about an account this installation has never
          // seen resolves to nobody, and those are exactly the rows worth
          // reading when a customer says their events go nowhere.
          <span className="text-warn">Matched no organization</span>
        ) : (
          <span className="font-mono text-[12px]">{r.orgSlug}</span>
        ),
    },
    {
      key: "account",
      header: source === "github" ? "Account" : "Stripe customer",
      mono: true,
      cell: (r) => (r.account ? <span className="truncate">{r.account}</span> : <span className="text-dim">Not recorded</span>),
    },
    {
      key: "received",
      header: "Received",
      cell: (r) => <When value={r.receivedAt} />,
    },
    {
      key: "standing",
      header: "Outcome",
      cell: (r) => {
        const standing = deliveryStanding(r);
        return <StatusChip value={standing.label} tone={standing.tone} />;
      },
    },
  ];

  return (
    <Card
      title="Deliveries"
      note="Inbound only. This product registers no outbound webhooks, so there is no outbound delivery log to show and none is drawn here."
    >
      <FilterBar
        search={{
          value: search,
          onChange: setSearch,
          label: "Search deliveries by event type, account or organization",
          placeholder: "Event, account or organization",
        }}
        filters={[
          {
            label: "Sender",
            value: source,
            onChange: (next) => {
              setSource(next);
              // The two ledgers use different words for an outcome, so a filter
              // set for one is a filter that means something else against the
              // other. Cleared rather than carried across.
              setUnhandled("");
            },
            options: [
              { value: "github", label: "GitHub" },
              { value: "stripe", label: "Stripe" },
            ],
          },
          {
            label: "Showing",
            value: unhandled,
            onChange: setUnhandled,
            options: [
              { value: "", label: "Every delivery" },
              {
                value: "unhandled",
                label: source === "github" ? "Never handled" : "Unresolved",
              },
            ],
          },
        ]}
      />
      <Loaded state={state} skeleton={<TableSkeleton rows={8} cols={5} />}>
        {(rows) => (
          <DataTable
            columns={columns}
            rows={rows}
            keyOf={(r) => r.id}
            empty={
              <EmptyList
                title={
                  unhandled
                    ? "Everything that arrived was handled"
                    : search
                      ? "No delivery matches that"
                      : "Nothing has arrived yet"
                }
              >
                {unhandled
                  ? "Nothing in this ledger is waiting. That is an answer rather than an empty screen: every delivery that reached this control plane was decided."
                  : search
                    ? "No delivery in this ledger matches that. Clear the search to see every one."
                    : source === "github"
                      ? "No GitHub delivery has reached this control plane. If a customer expects one, check that the app is installed for their account in the card above."
                      : "No Stripe event has reached this control plane. On an installation with no Stripe configuration that is the correct state."}
              </EmptyList>
            }
            footer={
              <More
                shown={rows.length}
                noun={{ one: "delivery", many: "deliveries" }}
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
