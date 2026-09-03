"use client";

import { useState } from "react";
import Link from "next/link";
import { Badge, Button, Card, Loaded, TableSkeleton, When } from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  FilterBar,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import { bytes } from "@/lib/format";
import {
  useGoldenVersions,
  useMaskingRules,
  type GoldenVersion,
  type MaskingRule,
} from "@/lib/admin-product";

/**
 * What this installation can honestly say about a customer's test data.
 *
 * TWO TABLES BACK THIS PAGE AND NOTHING ELSE DOES. `golden_versions` records
 * that a scan produced a copy, whether it was verified and how big it was.
 * `masking_rules` records which column gets which transform and whether a human
 * confirmed it. There is no table describing a customer's live database
 * connection, no snapshot ledger and no restore history anywhere in the schema,
 * so this page cannot answer "which database was cloned", "when was this
 * restored" or "how old is the copy". It says so at the bottom, in those words.
 *
 * WHY THE MASKING RULES COME FIRST. Because an unconfirmed rule is the only
 * finding on this page. It means a scan believes a column holds personal data,
 * nobody has agreed, and therefore the column is not being transformed on a
 * copy somebody is running tests against. A verified golden version needs
 * nobody's attention; an unconfirmed rule needs somebody's today.
 */
export default function ProductSafeStatePage() {
  return (
    // No lede override. The navigation's summary was rewritten when this section
    // was built, so the rail entry and the page heading say the same true thing
    // from one line, which is the whole reason AdminPage takes an href.
    <AdminPage href="/admin/product/safe-state">
      <div className="space-y-6">
        <MaskingRules />
        <GoldenVersions />
        <NotWired />
      </div>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Masking rules
 * ---------------------------------------------------------------------- */

type MaskingScope = "all" | "confirmed" | "unconfirmed";

function MaskingRules() {
  const [scope, setScope] = useState<MaskingScope>("unconfirmed");
  const [search, setSearch] = useState("");
  const state = useMaskingRules({ scope, search });

  return (
    <Card
      title="Masking rules"
      note="A rule nobody confirmed is a column that is not being transformed."
    >
      <FilterBar
        search={{
          value: search,
          onChange: setSearch,
          label: "Search masking rules by repository, table, column or transform",
          placeholder: "Repository, table, column or transform",
        }}
        filters={[
          {
            label: "Show",
            value: scope,
            onChange: (next) => setScope(next as MaskingScope),
            options: [
              { value: "unconfirmed", label: "Suggested, not confirmed" },
              { value: "confirmed", label: "In effect" },
              { value: "all", label: "Every rule" },
            ],
          },
        ]}
      />
      <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={5} />}>
        {(rows) => (
          <DataTable
            columns={MASKING_COLUMNS}
            rows={rows}
            keyOf={(r) => r.id}
            empty={
              search ? (
                <EmptyList
                  title="No rule matches that"
                  action={<Button onClick={() => setSearch("")}>Clear the search</Button>}
                >
                  Nothing has that repository, table, column or transform under the filter above.
                </EmptyList>
              ) : scope === "unconfirmed" ? (
                <EmptyList title="Every suggested rule has been confirmed">
                  No column on this installation is waiting for a human to agree with the scanner.
                  This is the answer you want: nothing a scan flagged is going into a twin
                  untransformed because nobody looked at it.
                </EmptyList>
              ) : scope === "confirmed" ? (
                <EmptyList title="No rule is in effect">
                  Nothing on this installation has a confirmed masking rule. If there are golden
                  versions below, they were produced without one, which means no column is being
                  transformed on any copy.
                </EmptyList>
              ) : (
                <EmptyList title="No masking rule exists">
                  Nothing has been scanned yet, or every scan found nothing to mask. A rule appears
                  here when the classifier proposes one, which happens on the first scan of a
                  repository.
                </EmptyList>
              )
            }
            footer={
              <More
                shown={rows.length}
                noun={{ one: "rule", many: "rules" }}
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

const MASKING_COLUMNS: Column<MaskingRule>[] = [
  {
    key: "column",
    header: "Column",
    cell: (r) => (
      <span className="block min-w-0">
        <span className="block break-words font-mono text-[12px] font-medium text-ink">
          {r.table}.{r.column}
        </span>
        <span className="block truncate text-[12px] text-muted">{r.repository}</span>
      </span>
    ),
  },
  {
    key: "org",
    header: "Organization",
    cell: (r) => (
      <Link
        href={`/admin/customers/users/organization?org=${encodeURIComponent(r.orgSlug)}`}
        className="inline-flex min-h-11 items-center truncate font-mono text-[12px] underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
      >
        {r.orgSlug}
      </Link>
    ),
  },
  {
    key: "transform",
    header: "Transform",
    cell: (r) => (
      <span className="block min-w-0">
        <span className="block break-words font-mono text-[12px]">{r.transform}</span>
        {r.link ? (
          // A linked column is kept consistent with another one, so a masked
          // foreign key still joins. Worth showing: a rule with a link is not
          // independent of the rest of the schema.
          <span className="block truncate text-[12px] text-muted">kept in step with {r.link}</span>
        ) : null}
      </span>
    ),
  },
  {
    key: "confirmed",
    header: "Standing",
    cell: (r) =>
      r.confirmed ? (
        <Badge tone="pass">in effect</Badge>
      ) : (
        <span className="flex flex-wrap items-center gap-1.5">
          <Badge tone="warn">not confirmed</Badge>
          <span className="text-[12px] text-muted">the column is not transformed</span>
        </span>
      ),
  },
  {
    key: "reason",
    header: "Why",
    cell: (r) =>
      r.reason ? (
        <span className="block max-w-[44ch] break-words text-[12.5px] leading-5">{r.reason}</span>
      ) : (
        <span className="text-dim">--</span>
      ),
  },
  {
    key: "created",
    header: "Proposed",
    cell: (r) => <When value={r.createdAt} />,
  },
];

/* -------------------------------------------------------------------------
 * Golden versions
 * ---------------------------------------------------------------------- */

type GoldenScope = "all" | "verified" | "unverified";

function GoldenVersions() {
  const [scope, setScope] = useState<GoldenScope>("unverified");
  const [search, setSearch] = useState("");
  const state = useGoldenVersions({ scope, search });

  return (
    <Card
      title="Golden data versions"
      note="The scanned copies a twin can be built from, and whether anything attested to them."
    >
      <FilterBar
        search={{
          value: search,
          onChange: setSearch,
          label: "Search golden versions by repository or version",
          placeholder: "Repository or version",
        }}
        filters={[
          {
            label: "Show",
            value: scope,
            onChange: (next) => setScope(next as GoldenScope),
            options: [
              { value: "unverified", label: "Never verified" },
              { value: "verified", label: "Verified" },
              { value: "all", label: "Every version" },
            ],
          },
        ]}
      />
      <Loaded state={state} skeleton={<TableSkeleton rows={5} cols={6} />}>
        {(rows) => (
          <DataTable
            columns={GOLDEN_COLUMNS}
            rows={rows}
            keyOf={(g) => g.id}
            empty={
              search ? (
                <EmptyList
                  title="No version matches that"
                  action={<Button onClick={() => setSearch("")}>Clear the search</Button>}
                >
                  Nothing has that repository or version under the filter above.
                </EmptyList>
              ) : scope === "unverified" ? (
                <EmptyList title="Every golden version was verified">
                  Every scanned copy on this installation carries an attestation. Nothing is being
                  cloned into a twin from data nobody signed off.
                </EmptyList>
              ) : scope === "verified" ? (
                <EmptyList title="No version has been verified">
                  Nothing on this installation carries an attestation. Every twin built from these
                  copies was built from data nobody proved the shape of.
                </EmptyList>
              ) : (
                <EmptyList title="Nothing has been scanned">
                  No repository on this installation has produced a golden version. The first scan
                  the engine runs will make one, and every twin after it names the version it was
                  built from.
                </EmptyList>
              )
            }
            footer={
              <More
                shown={rows.length}
                noun={{ one: "version", many: "versions" }}
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

const GOLDEN_COLUMNS: Column<GoldenVersion>[] = [
  {
    key: "version",
    header: "Version",
    cell: (g) => (
      <span className="block min-w-0">
        <span className="block break-words font-mono text-[12px] font-medium text-ink">
          {g.version}
        </span>
        <span className="block truncate text-[12px] text-muted">{g.repository}</span>
      </span>
    ),
  },
  {
    key: "org",
    header: "Organization",
    cell: (g) => (
      <Link
        href={`/admin/customers/users/organization?org=${encodeURIComponent(g.orgSlug)}`}
        className="inline-flex min-h-11 items-center truncate font-mono text-[12px] underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
      >
        {g.orgSlug}
      </Link>
    ),
  },
  {
    key: "verified",
    header: "Attestation",
    cell: (g) => (g.verified ? <Badge tone="pass">verified</Badge> : <Badge tone="warn">none</Badge>),
  },
  {
    key: "twins",
    header: "Live twins",
    numeric: true,
    cell: (g) => (
      // The number that turns an unverified version from history into a
      // finding. An unverified copy with no twin on it is a row; one with four
      // is four environments running on unattested data right now.
      <span className={g.twins > 0 && !g.verified ? "text-warn" : undefined}>
        {g.twins.toLocaleString()}
      </span>
    ),
  },
  {
    key: "size",
    header: "Size",
    numeric: true,
    cell: (g) => bytes(g.sizeBytes),
  },
  {
    key: "digest",
    header: "Source digest",
    mono: true,
    cell: (g) =>
      g.sourceDigest ? g.sourceDigest.slice(0, 12) : <span className="text-dim">--</span>,
  },
  {
    key: "created",
    header: "Scanned",
    cell: (g) => <When value={g.createdAt} />,
  },
];

/* -------------------------------------------------------------------------
 * What is not wired
 * ---------------------------------------------------------------------- */

/**
 * The absences, named.
 *
 * NOT an apology and not a roadmap. An operator who opens this page during an
 * incident needs to know within one screen whether the answer they came for can
 * exist here at all, because the alternative is twenty minutes of searching for
 * a panel that was never built. Naming the table each answer would need is what
 * makes this a statement rather than a shrug.
 */
function NotWired() {
  return (
    <Card title="What this page cannot tell you">
      <div className="space-y-3 px-4 py-4 text-[13px] leading-6 text-muted">
        <p>
          Everything above is read from two tables: the golden versions a scan produced, and the
          masking rules it proposed. Nothing else about a customer's data exists in this control
          plane, so three questions have no answer here and will not get one from a filter.
        </p>
        <ul className="space-y-2">
          <li>
            <span className="font-medium text-ink">Which database was cloned.</span> There is no
            record of a customer&apos;s live database connection anywhere in the schema. The engine
            reads it from the customer&apos;s own manifest and this control plane never sees it,
            which is deliberate: a credential it does not hold is a credential it cannot leak.
          </li>
          <li>
            <span className="font-medium text-ink">When a twin was restored, and to what.</span>{" "}
            There is no snapshot ledger and no restore history. A twin names the golden version it
            was built from and nothing records the act of building it, so the closest answer is the
            twin&apos;s own creation time.
          </li>
          <li>
            <span className="font-medium text-ink">How old the copy is.</span> A golden version
            carries the moment the row was written, not the moment the source data was read. On a
            scan that took an hour those are an hour apart, and this page will not present one as
            the other.
          </li>
        </ul>
        <p>
          What would enable all three is one table recording each scan and each restore as an event
          with its source, its duration and its outcome, written by the engine on the same path that
          writes the golden version today.
        </p>
      </div>
    </Card>
  );
}
