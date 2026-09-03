"use client";

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
} from "@/components/ui";
import { useOperators } from "@/lib/admin";

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
    <Page
      title="Operators"
      lede="Everybody who can sign in to this portal. An operator account can read every tenant, so this list is the blast radius of the platform's own credentials."
    >
      <Card>
        <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={5} />}>
          {(operators) =>
            operators.length === 0 ? (
              // Reachable in principle and alarming in practice: you are
              // reading this page, so at least one operator exists. Saying so
              // is more useful than an empty table that reads like a bug.
              <Empty title="No operator accounts">
                This installation has no operator accounts, which cannot be true if you are
                reading this page. Check that the portal is pointed at the database you expect.
              </Empty>
            ) : (
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Operator</Th>
                      <Th>Role</Th>
                      <Th>Can sign in</Th>
                      <Th>Last signed in</Th>
                      <Th>State</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {operators.map((o) => (
                      <Row key={o.id}>
                        <Td>
                          <span className="block truncate font-medium text-ink">{o.name}</span>
                          <span className="block truncate text-[12px] text-muted">{o.email}</span>
                        </Td>
                        <Td label="Role">
                          {/* Underscores are a database convention and not a
                              word. The role reads as English here and the value
                              is unchanged underneath. */}
                          {o.role.replace(/_/g, " ")}
                          {o.isRoot ? (
                            <span className="mt-1 block text-[12px] text-muted">
                              The root operator, which cannot be deleted, demoted or suspended
                            </span>
                          ) : null}
                        </Td>
                        <Td label="Can sign in">
                          {o.provisioned ? (
                            "Yes"
                          ) : (
                            <span className="text-muted">Not provisioned</span>
                          )}
                        </Td>
                        <Td label="Last signed in">
                          {o.lastSignedInAt ? (
                            <When value={o.lastSignedInAt} />
                          ) : (
                            <span className="text-muted">Never</span>
                          )}
                        </Td>
                        <Td label="State">
                          {o.suspended ? (
                            <Badge tone="fail">suspended</Badge>
                          ) : (
                            <Badge tone="pass">active</Badge>
                          )}
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
