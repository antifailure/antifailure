"use client";

import { useState } from "react";
import {
  Badge,
  Card,
  Empty,
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
} from "@/components/ui";
import { useTenants } from "@/lib/admin";

/**
 * Every organization on the installation.
 *
 * This is the portal's front page because it is the question support work
 * actually starts from: somebody names an account, and everything else is
 * reached from there.
 */
export default function AdminTenantsPage() {
  const [search, setSearch] = useState("");
  const state = useTenants(search);

  return (
    <Page
      title="Tenants"
      lede="Every organization on this installation. Counts are live rather than billed figures."
      actions={
        <label className="flex min-w-0 items-center gap-2">
          {/* A real label rather than a placeholder standing in for one, so the
              field still says what it is once somebody has typed in it. */}
          <span className="sr-only">Search tenants</span>
          <input
            className={`${inputClass} w-[min(280px,60vw)]`}
            type="search"
            value={search}
            placeholder="Search by name or slug"
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
      }
    >
      <Card>
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={5} />}>
          {(page) =>
            page.rows.length === 0 ? (
              <Empty title={search ? "No tenant matches that" : "No organizations yet"}>
                {search
                  ? "Nothing on this installation has that name or slug. Clear the search to see every tenant."
                  : "Nobody has created an organization on this installation. The first sign-in that creates one will show up here."}
              </Empty>
            ) : (
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Organization</Th>
                      <Th>Plan</Th>
                      {/* numeric, which is the console's own right-align plus
                          tabular figures, so the digits line up down the column
                          and two accounts can be compared without reading them. */}
                      <Th numeric>Members</Th>
                      <Th numeric>Environments</Th>
                      <Th>Created</Th>
                      <Th>State</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {page.rows.map((t) => (
                      <Row key={t.id}>
                        {/* No label on this one: it names the row, and leads
                            the stacked record on a phone by itself. */}
                        <Td>
                          <span className="block truncate font-medium text-ink">{t.name}</span>
                          <span className="block truncate font-mono text-[12px] text-muted">
                            {t.slug}
                          </span>
                        </Td>
                        <Td label="Plan">{t.plan}</Td>
                        <Td label="Members" numeric>
                          {t.members.toLocaleString()}
                        </Td>
                        <Td label="Environments" numeric>
                          {t.environments.toLocaleString()}
                        </Td>
                        <Td label="Created">
                          <When value={t.createdAt} />
                        </Td>
                        <Td label="State">
                          {t.suspended ? (
                            <Badge tone="fail">suspended</Badge>
                          ) : (
                            <Badge tone="pass">active</Badge>
                          )}
                          {t.suspendedReason ? (
                            <span className="mt-1 block max-w-[36ch] text-[12px] text-muted">
                              {t.suspendedReason}
                            </span>
                          ) : null}
                        </Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
              </TableWrap>
            )
          }
        </Loaded>
      </Card>
    </Page>
  );
}
