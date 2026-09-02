"use client";

import { Card, Loaded, TableSkeleton, When } from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { useOperators, type Operator } from "@/lib/admin";

/**
 * Who can reach this portal, and with what role.
 *
 * The most sensitive list in the product: an operator account is cross-tenant
 * read of the entire customer base, so this page exists to make the answer to
 * "who has that" a thing somebody can look at rather than infer from a table
 * nobody reads.
 *
 * Read only for now. Creating an operator, changing a role and suspending one
 * are admin.operators.write and have no route yet; when they arrive they belong
 * here rather than on a second screen.
 */
export default function AdminOperatorsPage() {
  const state = useOperators();

  return (
    <AdminPage
      href="/admin/administration/admins"
      lede="Everybody who can sign in to this portal. An operator account can read every tenant, so this list is the blast radius of the platform's own credentials."
    >
      <Card>
        <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={5} />}>
          {(operators) => (
            <DataTable
              columns={columns}
              rows={operators}
              keyOf={(o) => o.id}
              empty={
                // Reachable in principle and alarming in practice: you are
                // reading this page, so at least one operator exists. Saying so
                // is more useful than an empty table that reads like a bug.
                <EmptyList title="No operator accounts">
                  This installation has no operator accounts, which cannot be true if you are
                  reading this page. Check that the portal is pointed at the database you expect.
                </EmptyList>
              }
            />
          )}
        </Loaded>
      </Card>
    </AdminPage>
  );
}

const columns: Column<Operator>[] = [
  {
    key: "operator",
    header: "Operator",
    cell: (o) => (
      <>
        <span className="block truncate font-medium text-ink">{o.name}</span>
        <span className="block truncate text-[12px] text-muted">{o.email}</span>
      </>
    ),
  },
  {
    key: "role",
    header: "Role",
    cell: (o) => (
      <>
        {/* Underscores are a database convention and not a word. The role reads
            as English here and the value is unchanged underneath. */}
        {o.role.replace(/_/g, " ")}
        {o.isRoot ? (
          <span className="mt-1 block text-[12px] text-muted">
            The root operator, which cannot be deleted, demoted or suspended
          </span>
        ) : null}
      </>
    ),
  },
  {
    key: "provisioned",
    header: "Can sign in",
    cell: (o) => (o.provisioned ? "Yes" : <span className="text-muted">Not provisioned</span>),
  },
  {
    key: "lastSignedIn",
    header: "Last signed in",
    cell: (o) =>
      o.lastSignedInAt ? <When value={o.lastSignedInAt} /> : <span className="text-muted">Never</span>,
  },
  {
    key: "state",
    header: "State",
    cell: (o) => <StatusChip value={o.suspended ? "suspended" : "active"} />,
  },
];
