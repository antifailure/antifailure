"use client";

import { useState } from "react";
import { mutate, query, useApi, usePages } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import { More } from "@/components/pagination";
import {
  Badge,
  Bar,
  Button,
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

interface Entry {
  seq: string;
  actor_label: string;
  action: string;
  target_type: string;
  target_id: string | null;
  origin: string;
  detail: unknown;
  occurred_at: string;
}

interface Chain {
  ok: boolean;
  entries: number;
  head: string | null;
  problems: { seq: number; kind: string; detail: string }[];
}

const MAY_EXPORT = new Set(["owner", "admin"]);

/** One page of the log. The route allows 500 and this asks for a fifth of
 *  that, because the page it fills is read top down and the cost of being
 *  wrong about how many somebody wants is now one button rather than a list
 *  that stops without saying so. */
const AUDIT_PAGE = 100;

/**
 * The chain check.
 *
 * Shown as its own statement rather than a green tick beside the table,
 * because "these entries are in the order they were written and none has been
 * altered" is the only claim the log makes that a database dump cannot.
 */
function Integrity() {
  const state = useApi<Chain>(() => query("audit.verify"), []);
  return (
    <Loaded state={state} skeleton={<Bar className="h-5 w-56" />}>
      {(chain) => (
        <div className="flex flex-wrap items-center gap-3">
          <Badge tone={chain.ok ? "pass" : "fail"}>{chain.ok ? "chain intact" : "chain broken"}</Badge>
          <span className="text-[12.5px] text-muted">
            {chain.entries.toLocaleString()} entries
            {chain.head ? (
              <>
                {" "}
                &middot; head <span className="font-mono">{chain.head.slice(0, 12)}</span>
              </>
            ) : null}
          </span>
          {chain.problems.length > 0 ? (
            <span className="text-[12.5px] text-fail">
              {chain.problems[0]!.kind} at {chain.problems[0]!.seq}: {chain.problems[0]!.detail}
            </span>
          ) : null}
        </div>
      )}
    </Loaded>
  );
}

function Exporter() {
  const session = useSessionContext();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const csrf = session.data?.csrfToken ?? "";

  return (
    <div className="flex items-center gap-3">
      {error ? (
        <span role="alert" className="text-[12.5px] text-fail">
          {error}
        </span>
      ) : null}
      <Button
        busy={busy}
        onClick={async () => {
          setBusy(true);
          setError(null);
          try {
            const out = await mutate<{ format: string; entries: number; rows: unknown[] }>(
              "audit.export",
              { format: "json" },
              csrf,
            );
            // Held in memory and handed to the browser, rather than fetched
            // from a URL: an export endpoint that answers a GET is a link
            // somebody can be tricked into following.
            const blob = new Blob([JSON.stringify(out.rows, null, 2)], {
              type: "application/json",
            });
            const url = URL.createObjectURL(blob);
            const a = document.createElement("a");
            a.href = url;
            a.download = `audit-${new Date().toISOString().slice(0, 10)}.json`;
            a.click();
            URL.revokeObjectURL(url);
          } catch (e) {
            setError(e instanceof Error ? e.message : "The export failed.");
          } finally {
            setBusy(false);
          }
        }}
      >
        {busy ? "Exporting" : "Export as JSON"}
      </Button>
    </div>
  );
}

function Audit() {
  const session = useSessionContext();
  const [action, setAction] = useState("");
  // `audit.list` is the odd one of the three: it pages by `seq` under the name
  // `before`, wants a number where the row hands back a string, and returns a
  // bare array. So a full page is the only signal that there is another, which
  // is why the cursor is dropped when a page comes back short.
  const state = usePages<Entry>(
    async (cursor) => {
      const rows = await query<Entry[]>("audit.list", {
        limit: AUDIT_PAGE,
        ...(action ? { action } : {}),
        ...(cursor === null ? {} : { before: Number(cursor) }),
      });
      const last = rows[rows.length - 1];
      return {
        rows,
        next: rows.length === AUDIT_PAGE && last ? String(last.seq) : null,
      };
    },
    [action],
  );
  const mayExport = MAY_EXPORT.has(session.data?.role ?? "");

  return (
    <Page
      title="Audit"
      lede="Every action this organization has taken, hash-chained so a later edit to an earlier entry is detectable."
      actions={mayExport ? <Exporter /> : null}
    >
      {mayExport ? <div className="mb-5">{<Integrity />}</div> : null}

      <Card
        title="Entries"
        note="Newest first."
        actions={
          <input
            aria-label="Filter by action"
            placeholder="filter by action"
            value={action}
            onChange={(e) => setAction(e.target.value.trim())}
            className={`${inputClass} mt-0 w-full sm:w-[190px]`}
          />
        }
      >
        <Loaded state={state} skeleton={<TableSkeleton rows={8} cols={5} />}>
          {(rows) =>
            rows.length === 0 ? (
              <Empty title={action ? "No entries with that action" : "Nothing recorded yet"}>
                {action
                  ? "Clear the filter to see everything the log holds."
                  : "The log fills as people and machines act on this organization."}
              </Empty>
            ) : (
              <>
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th numeric>Seq</Th>
                      <Th>Action</Th>
                      <Th>Actor</Th>
                      <Th>Target</Th>
                      <Th>Origin</Th>
                      <Th>When</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((e) => (
                      <Row key={e.seq}>
                        <Td label="Seq" numeric mono>{e.seq}</Td>
                        <Td mono>{e.action}</Td>
                        <Td label="Actor">{e.actor_label}</Td>
                        <Td label="Target" className="max-w-[28ch] truncate">
                          {e.target_type}
                          {e.target_id ? (
                            <span className="text-dim"> {e.target_id}</span>
                          ) : null}
                        </Td>
                        <Td label="Origin">
                          <Badge>{e.origin}</Badge>
                        </Td>
                        <Td label="When">
                          <When value={e.occurred_at} />
                        </Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
              </TableWrap>
              <More
                shown={rows.length}
                noun={{ one: "entry", many: "entries" }}
                hasMore={state.hasMore}
                busy={state.busy}
                error={state.moreError}
                onMore={state.more}
              />
              </>
            )
          }
        </Loaded>
      </Card>
    </Page>
  );
}

export default function AuditPage() {
  return <Audit />;
}
