"use client";

import { useState } from "react";
import Link from "next/link";
import { Badge, Button, Card, Loaded, TableSkeleton, When } from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  FilterBar,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import { useBranches, type BranchRow, type BranchScope } from "@/lib/admin-product";

/**
 * Environments grouped by the branch they belong to.
 *
 * A BRANCH IS NOT A TABLE. It is a column on `environments`, so this screen is
 * a grouping rather than a list, and the grouping is what makes it worth a
 * screen: one branch holds several twins over its life, and the question an
 * operator has is about the branch rather than about any one of them.
 *
 * THE FINDING THIS PAGE EXISTS FOR is `orphaned`: a branch with a live twin
 * whose pull request is closed or merged. Nothing is wrong with the twin, no
 * alarm fired, and the data is not corrupt. It is simply still running, and
 * still costing money, for a change that landed a fortnight ago. That is the
 * default filter, because it is the only one of the three that is a finding.
 */
export default function ProductBranchesPage() {
  const [scope, setScope] = useState<BranchScope>("orphaned");
  const [search, setSearch] = useState("");
  const state = useBranches({ scope, search });

  return (
    <AdminPage href="/admin/product/branches">
      <Card>
        <FilterBar
          search={{
            value: search,
            onChange: setSearch,
            label: "Search branches by name or repository",
            placeholder: "Branch or repository",
          }}
          filters={[
            {
              label: "Show",
              value: scope,
              onChange: (next) => setScope(next as BranchScope),
              options: [
                { value: "orphaned", label: "Holding a twin after the pull request closed" },
                { value: "live", label: "Holding a twin" },
                { value: "all", label: "Every branch ever" },
              ],
            },
          ]}
        />
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={6} />}>
          {(rows) => (
            <DataTable
              columns={COLUMNS}
              rows={rows}
              keyOf={(b) => `${b.repositoryId}:${b.branch}`}
              empty={<BranchesEmpty scope={scope} search={search} onClear={() => setSearch("")} />}
              footer={
                <More
                  shown={rows.length}
                  noun={{ one: "branch", many: "branches" }}
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

function BranchesEmpty({
  scope,
  search,
  onClear,
}: {
  scope: BranchScope;
  search: string;
  onClear: () => void;
}) {
  if (search) {
    return (
      <EmptyList
        title="No branch matches that"
        action={<Button onClick={onClear}>Clear the search</Button>}
      >
        Nothing has that branch name or repository under the filter above. A branch with no
        environment has never appeared on this installation at all, because a branch here is a
        column on an environment rather than a row of its own.
      </EmptyList>
    );
  }
  if (scope === "orphaned") {
    return (
      <EmptyList title="Nothing is being held open after its pull request closed">
        Every branch with a live twin still has an open pull request behind it. This is the answer
        you want: nothing is running for a change that already landed.
      </EmptyList>
    );
  }
  if (scope === "live") {
    return (
      <EmptyList title="No branch is holding a twin">
        Nothing is running on this installation right now, so no branch has an environment against
        it. Switch to every branch to see what has run before.
      </EmptyList>
    );
  }
  return (
    <EmptyList title="No branch has ever had an environment">
      Nobody has created an environment on this installation, so there is nothing to group. The
      first pull request the GitHub app sees will make one.
    </EmptyList>
  );
}

/**
 * Six columns, and the count is the design.
 *
 * The first draft had eight, and at a desktop width the table's auto layout
 * gave the Holding cell about eight characters: the finding this whole page
 * exists for came out one word per line, and the last column was cut off by the
 * card. Live and ever are one fact read together, and the newest twin's state
 * belongs beside them rather than in a column of its own.
 */
const COLUMNS: Column<BranchRow>[] = [
  {
    key: "branch",
    header: "Branch",
    cell: (b) => (
      <span className="block min-w-0">
        <span className="block break-words font-mono text-[12px] font-medium text-ink">
          {b.branch}
        </span>
        <span className="block truncate text-[12px] text-muted">{b.repository}</span>
      </span>
    ),
  },
  {
    key: "org",
    header: "Organization",
    cell: (b) => (
      <Link
        href={`/admin/customers/users/organization?org=${encodeURIComponent(b.orgSlug)}`}
        className="inline-flex min-h-11 items-center truncate font-mono text-[12px] underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
      >
        {b.orgSlug}
      </Link>
    ),
  },
  {
    key: "pr",
    header: "Pull request",
    cell: (b) =>
      b.pullRequest === null ? (
        // A branch with twins and no pull request is ordinary: somebody ran the
        // engine against a branch directly. Saying so beats an empty cell that
        // reads like missing data.
        <span className="block min-w-[16ch] text-muted">none, run against the branch</span>
      ) : (
        <span className="block min-w-[18ch]">
          <span className="flex flex-wrap items-center gap-1.5">
            <span className="whitespace-nowrap font-medium">Number {b.pullRequest}</span>
            <StatusChip value={b.pullRequestState} />
            {b.pullRequestDraft ? <Badge tone="neutral">draft</Badge> : null}
            {b.pullRequestFromFork ? <Badge tone="warn">fork</Badge> : null}
          </span>
          {b.pullRequestTitle ? (
            <span className="mt-0.5 block max-w-[40ch] truncate text-[12px] text-muted">
              {b.pullRequestTitle}
            </span>
          ) : null}
        </span>
      ),
  },
  {
    key: "holding",
    header: "Holding",
    cell: (b) => (
      // The floor is what keeps the finding readable. Without it the auto
      // layout gave this cell eight characters and the sentence below came out
      // one word per line.
      <span className="block min-w-[22ch] max-w-[34ch]">
        <span className="flex flex-wrap items-center gap-1.5">
          {/* A badge is a word, not a sentence. The word is the chip and the
              explanation is the line under it, at a size somebody can read. */}
          {b.orphaned ? (
            <Badge tone="fail">still up</Badge>
          ) : b.live > 0 ? (
            <Badge tone="pass">expected</Badge>
          ) : (
            <Badge tone="neutral">nothing running</Badge>
          )}
          {b.overdue > 0 ? <Badge tone="warn">{b.overdue} past expiry</Badge> : null}
        </span>
        {b.orphaned ? (
          <span className="mt-1 block text-[12px] leading-5 text-muted">
            The pull request is {b.pullRequestState} and a twin is still running.
          </span>
        ) : null}
      </span>
    ),
  },
  {
    key: "twins",
    header: "Twins",
    numeric: true,
    cell: (b) => (
      <span className="block">
        <Link
          // The organization travels with the branch name. Two tenants can
          // both have a branch called main, and a link carrying only the name
          // showed somebody else's environments beside the ones clicked on.
          href={
            `/admin/product/twins?org=${encodeURIComponent(b.orgId)}` +
            `&slug=${encodeURIComponent(b.orgSlug)}&q=${encodeURIComponent(b.branch)}`
          }
          className="inline-flex min-h-11 items-center justify-end underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
        >
          <span className="whitespace-nowrap">
            {b.live.toLocaleString()} live of {b.twins.toLocaleString()}
          </span>
        </Link>
        <span className="block whitespace-nowrap text-[12px] font-normal text-muted">
          newest is {b.latestState.replace(/_/g, " ")}
        </span>
      </span>
    ),
  },
  {
    key: "activity",
    header: "Last event",
    cell: (b) => (
      <span className="block min-w-0">
        <When value={b.lastActivity} />
        {b.pullRequestClosedAt ? (
          <span className="block text-[12px] text-muted">
            Pull request closed <When value={b.pullRequestClosedAt} />
          </span>
        ) : null}
      </span>
    ),
  },
];
