"use client";

import { Suspense } from "react";
import { WithRepository } from "@/components/RepositoryPicker";
import { bytes } from "@/lib/format";
import { query, useApi } from "@/lib/api";
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

interface Rule {
  table_name: string;
  column_name: string;
  transform: string;
  link: string | null;
  reason: string | null;
  confirmed: boolean;
}

interface Attestation {
  version: string;
  verified: boolean;
  attestation: {
    report?: { tables?: number; columns?: number; masked?: number; rows?: number };
    scanner?: string;
  } | null;
  created_at: string;
  size_bytes: string | number | null;
}

function Masking({ repository }: { repository: string }) {
  const rules = useApi<Rule[]>(() => query("masking.rules", { repository }), [repository]);
  const goldens = useApi<Attestation[]>(
    () => query("masking.attestations", { repository }),
    [repository],
  );

  return (
    <div className="space-y-6">
      <Card
        title="Rules"
        note="What is transformed before a golden leaves the customer boundary. An unconfirmed rule was proposed by the scanner and has not been approved."
      >
        <Loaded state={rules} skeleton={<TableSkeleton rows={5} cols={4} />}>
          {(rows) =>
            rows.length === 0 ? (
              <Empty title="No masking rules">
                The scanner proposes rules the first time it reads this
                repository&apos;s schema. None here means it has not run yet.
              </Empty>
            ) : (
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Table</Th>
                      <Th>Column</Th>
                      <Th>Transform</Th>
                      <Th>Link</Th>
                      <Th>Reason</Th>
                      <Th>State</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((r) => (
                      <Row key={`${r.table_name}.${r.column_name}`}>
                        <Td mono>{r.table_name}</Td>
                        <Td label="Column" mono>{r.column_name}</Td>
                        <Td label="Transform">{r.transform}</Td>
                        <Td label="Link" mono>{r.link ?? "--"}</Td>
                        <Td label="Reason" className="max-w-[34ch]">{r.reason ?? "--"}</Td>
                        <Td label="State">
                          <Badge tone={r.confirmed ? "pass" : "warn"}>
                            {r.confirmed ? "confirmed" : "proposed"}
                          </Badge>
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

      <Card
        title="Attestations"
        note="One per golden version. Verified means the scan's own report was checked against the rules that were in force."
      >
        <Loaded state={goldens} skeleton={<TableSkeleton rows={4} cols={5} />}>
          {(rows) =>
            rows.length === 0 ? (
              <Empty title="No goldens yet">
                A golden is built from a masked snapshot. The attestation that
                proves what was masked arrives with it.
              </Empty>
            ) : (
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Version</Th>
                      <Th>Verified</Th>
                      <Th numeric>Tables</Th>
                      <Th numeric>Columns</Th>
                      <Th numeric>Masked</Th>
                      <Th numeric>Size</Th>
                      <Th>Built</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((g) => {
                      const report = g.attestation?.report;
                      return (
                        <Row key={g.version}>
                          <Td mono>{g.version}</Td>
                          <Td label="Verified">
                            <Badge tone={g.verified ? "pass" : "warn"}>
                              {g.verified ? "verified" : "unverified"}
                            </Badge>
                          </Td>
                          <Td label="Tables" numeric>{report?.tables ?? "--"}</Td>
                          <Td label="Columns" numeric>{report?.columns ?? "--"}</Td>
                          <Td label="Masked" numeric>{report?.masked ?? "--"}</Td>
                          <Td label="Size" numeric>{bytes(g.size_bytes)}</Td>
                          <Td label="Built">
                            <When value={g.created_at} />
                          </Td>
                        </Row>
                      );
                    })}
                  </tbody>
                </Table>
              </TableWrap>
            )
          }
        </Loaded>
      </Card>
    </div>
  );
}

export default function MaskingPage() {
  return (
    <Suspense
      fallback={
        <Page title="Masking">
          <Card title="Rules">
            <TableSkeleton rows={5} cols={5} />
          </Card>
        </Page>
      }
    >
      <Page
        title="Masking"
        lede="Which columns are transformed on the way out, and what each golden attests it did."
      >
        <WithRepository>{(repository) => <Masking repository={repository} />}</WithRepository>
      </Page>
    </Suspense>
  );
}
