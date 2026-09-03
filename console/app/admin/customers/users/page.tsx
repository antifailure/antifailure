"use client";

import { useState } from "react";
import { Card, Loaded, TableSkeleton, When } from "@/components/ui";
import { More } from "@/components/pagination";
import {
  AdminPage,
  DataTable,
  EmptyList,
  FilterBar,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { useTenants, type Tenant } from "@/lib/admin";

/**
 * Every organization on the installation.
 *
 * The portal's first list, and the one every other section copies, so it is
 * built out of the shared primitives rather than out of a table of its own.
 * That is not tidiness: the version this replaced fired a fresh cross tenant
 * query on every KEYSTROKE in the search box, because the field was wired
 * straight to the fetch. `FilterBar` submits, so an operator gets to finish
 * typing an account name before the table moves under them, and the
 * installation gets one query instead of one per character.
 */

const columns: Column<Tenant>[] = [
  {
    key: "organization",
    header: "Organization",
    cell: (t) => (
      <>
        <span className="block truncate font-medium text-ink">{t.name}</span>
        <span className="block truncate font-mono text-[12px] text-muted">{t.slug}</span>
      </>
    ),
  },
  { key: "plan", header: "Plan", cell: (t) => t.plan },
  // numeric is the console's right align plus tabular figures, so the digits
  // line up down the column and two accounts can be compared without reading
  // them one at a time.
  { key: "members", header: "Members", numeric: true, cell: (t) => t.members.toLocaleString() },
  {
    key: "environments",
    header: "Environments",
    numeric: true,
    cell: (t) => t.environments.toLocaleString(),
  },
  { key: "created", header: "Created", cell: (t) => <When value={t.createdAt} /> },
  {
    key: "state",
    header: "State",
    cell: (t) => (
      <>
        <StatusChip value={t.suspended ? "suspended" : "active"} />
        {t.suspendedReason ? (
          // break-words because a reason is whatever an operator pasted, and an
          // unbreakable token here would widen the whole table rather than this
          // one cell.
          <span className="mt-1 block max-w-[36ch] break-words text-[12px] text-muted">
            {t.suspendedReason}
          </span>
        ) : null}
      </>
    ),
  },
];

export default function AdminTenantsPage() {
  const [search, setSearch] = useState("");
  const state = useTenants(search);

  return (
    <AdminPage
      href="/admin/customers/users"
      lede="Every organization on this installation. Counts are live rather than billed figures."
    >
      <Card>
        <FilterBar
          search={{
            value: search,
            onChange: setSearch,
            label: "Search organizations by name or slug",
            placeholder: "Search by name or slug",
          }}
        />
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={5} />}>
          {(rows) => (
            <DataTable
              columns={columns}
              rows={rows}
              keyOf={(t) => t.id}
              // The link is on the row's NAME rather than the whole row: a row
              // that navigates on click has no keyboard equivalent and no
              // address to copy.
              href={(t) => `/admin/customers/users/organization?org=${encodeURIComponent(t.slug)}`}
              empty={
                <EmptyList title={search ? "No organization matches that" : "No organizations yet"}>
                  {search
                    ? "Nothing on this installation has that name or slug. Clear the search to see every organization."
                    : "Nobody has created an organization on this installation. The first sign-in that creates one will show up here."}
                </EmptyList>
              }
              // Rendered in BOTH states. "All 24 organizations." is the only
              // place this screen ever says the list is complete, and a footer
              // that hid itself at the end could only say the opposite.
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
    </AdminPage>
  );
}
