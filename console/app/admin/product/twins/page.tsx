"use client";

import { Suspense, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Badge, Button, Card, LinkButton, Loaded, Page, TableSkeleton, When } from "@/components/ui";
import { AdminPage, DataTable, EmptyList, FilterBar, StatusChip } from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import { useTwins, type Twin, type TwinScope } from "@/lib/admin-product";
import { expiryPhrase } from "@/lib/productshapes";

/**
 * Every production twin on the installation.
 *
 * THE QUESTION THIS PAGE ANSWERS is "what is running right now, who owns it,
 * and which of them should not still be up". So the default is `live` and the
 * second filter is `overdue`, which is the only one of the three that is a
 * finding: a twin past the lifetime it was created with, not torn down, and
 * costing somebody money for a branch nobody is looking at any more.
 *
 * `all` exists and is not the default, because a fleet view that includes every
 * environment ever created grows forever and answers nothing.
 *
 * THE LIST IS PAGED, and the footer renders in both states. Three screens in
 * this console read one page of a cursored route and told an operator the list
 * was complete when it was showing a third of it. "All 24 twins." is the only
 * sentence that ever claims a list is finished here, and it is only printed
 * when the server said there was no next cursor.
 */
export default function ProductTwinsPage() {
  return (
    <Suspense
      fallback={
        <Page title="Production Twins">
          <TableSkeleton rows={6} cols={7} />
        </Page>
      }
    >
      <TwinsView />
    </Suspense>
  );
}

function TwinsView() {
  // `q` seeds the search, because the branches page links here with a branch
  // name in it. Seeded rather than controlled by the address: once the reader
  // types, the box is theirs, and a filter that snaps back to what the last
  // link said is a filter that fights whoever is using it.
  //
  // `org` is NOT seeded, it is applied. Two tenants can both have a branch
  // called main, so a link that carried only the name showed somebody else's
  // environments beside the ones that were clicked on. The organization is a
  // fact about where the reader came from rather than a filter they chose, so
  // it stays until they leave, and the strip below says it is on.
  const params = useSearchParams();
  const orgId = params.get("org");
  const orgSlug = params.get("slug");
  const [search, setSearch] = useState(params.get("q") ?? "");
  const [scope, setScope] = useState<TwinScope>(params.get("q") ? "all" : "live");
  const state = useTwins({ scope, search, orgId });

  return (
    <AdminPage href="/admin/product/twins">
      {orgId ? (
        <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-rule bg-card px-4 py-3">
          <p className="text-[12.5px] leading-5 text-ink">
            Showing one organization only. Everything below belongs to{" "}
            <span className="font-mono text-[12px]">{orgSlug ?? orgId.slice(0, 8)}</span>.
          </p>
          <LinkButton href="/admin/product/twins" variant="secondary">
            Show every twin
          </LinkButton>
        </div>
      ) : null}
      <Card>
        <FilterBar
          search={{
            value: search,
            onChange: setSearch,
            label: "Search twins by environment, repository or branch",
            placeholder: "Environment, repository or branch",
          }}
          filters={[
            {
              label: "Show",
              value: scope,
              onChange: (next) => setScope(next as TwinScope),
              options: [
                { value: "live", label: "Running now" },
                { value: "overdue", label: "Past their expiry" },
                { value: "all", label: "Every twin ever" },
              ],
            },
          ]}
        />
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={7} />}>
          {(rows) => (
            <DataTable
              columns={COLUMNS}
              rows={rows}
              keyOf={(t) => t.id}
              href={(t) => `/admin/product/twins/detail?id=${encodeURIComponent(t.id)}`}
              empty={<TwinsEmpty scope={scope} search={search} onClear={() => setSearch("")} />}
              footer={
                <More
                  shown={rows.length}
                  noun={{ one: "twin", many: "twins" }}
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

/**
 * Why the list is empty, in the words of the filter that emptied it.
 *
 * Three different reasons, and they are not interchangeable: nothing matched
 * the search, nothing is overdue, or nothing has ever run here. The third is a
 * fact about the installation and the first two are facts about the filters,
 * and an operator who cannot tell them apart during an incident assumes the
 * worst one.
 */
function TwinsEmpty({
  scope,
  search,
  onClear,
}: {
  scope: TwinScope;
  search: string;
  onClear: () => void;
}) {
  if (search) {
    return (
      <EmptyList
        title="No twin matches that"
        action={<Button onClick={onClear}>Clear the search</Button>}
      >
        Nothing on this installation has that environment id, repository or branch. The search runs
        over the filter above it, so a twin that is torn down will not appear while this is set to
        running.
      </EmptyList>
    );
  }
  if (scope === "overdue") {
    return (
      <EmptyList title="Nothing is past its expiry">
        Every running twin is still inside the lifetime it was created with. This is the answer you
        want: nothing is being paid for after the branch it belonged to stopped mattering.
      </EmptyList>
    );
  }
  if (scope === "live") {
    return (
      <EmptyList title="No twin is running">
        Nothing is up on this installation right now. A twin is created when a pull request opens or
        when somebody runs the engine against a branch, so this is what a quiet weekend looks like.
        Switch the filter to every twin to see what has run before.
      </EmptyList>
    );
  }
  return (
    <EmptyList title="No twin has ever run here">
      Nobody has created an environment on this installation. The first pull request the GitHub app
      sees will make one, and it will appear here.
    </EmptyList>
  );
}

const COLUMNS = [
  {
    key: "twin",
    header: "Twin",
    cell: (t: Twin) => (
      <span className="block min-w-0">
        {/* break-words rather than truncate. An environment id is what an
            operator pastes into a log search, and half of one is worse than
            a row that is one line taller. */}
        <span className="block break-words font-mono text-[12px] font-medium text-ink">{t.envId}</span>
        <span className="block truncate text-[12px] text-muted">{t.repository}</span>
      </span>
    ),
  },
  {
    key: "org",
    header: "Organization",
    cell: (t: Twin) => (
      // A link out of the row rather than plain text: the next question after
      // "whose twin is this" is always about the account, and copying a slug
      // into a search box is the step this removes.
      <Link
        href={`/admin/customers/users/organization?org=${encodeURIComponent(t.orgSlug)}`}
        className="inline-flex min-h-11 items-center underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
      >
        <span className="truncate font-mono text-[12px]">{t.orgSlug}</span>
      </Link>
    ),
  },
  {
    key: "branch",
    header: "Branch",
    cell: (t: Twin) => (
      <span className="block min-w-0">
        <span className="block break-words font-mono text-[12px]">{t.branch}</span>
        {t.pullRequest === null ? null : (
          <span className="block text-[12px] text-muted">Pull request {t.pullRequest}</span>
        )}
      </span>
    ),
  },
  {
    key: "state",
    header: "State",
    cell: (t: Twin) => (
      <span className="flex flex-wrap items-center gap-1.5">
        <StatusChip value={t.state} />
        {/* Two separate facts, and both matter. Overdue says it should have
            gone; teardown pending says somebody already asked. An operator who
            cannot see the second asks for it again. */}
        {t.overdue ? <Badge tone="warn">overdue</Badge> : null}
        {t.teardownPending ? <Badge tone="neutral">teardown asked</Badge> : null}
      </span>
    ),
  },
  {
    key: "golden",
    header: "Golden data",
    cell: (t: Twin) =>
      t.goldenVersion === null ? (
        <span className="text-dim">None named</span>
      ) : (
        <span className="flex flex-wrap items-center gap-1.5">
          <span className="break-words font-mono text-[12px]">{t.goldenVersion}</span>
          {t.goldenVerified === true ? <Badge tone="pass">verified</Badge> : null}
          {t.goldenVerified === false ? <Badge tone="warn">unverified</Badge> : null}
          {/* Null is a third answer: the twin names a version this control
              plane has no row for. Saying "unverified" there would be a
              guess about somebody's data. */}
          {t.goldenVerified === null ? <Badge tone="neutral">no record</Badge> : null}
        </span>
      ),
  },
  {
    key: "runs",
    header: "Runs",
    numeric: true,
    cell: (t: Twin) => t.runs.toLocaleString(),
  },
  {
    key: "expires",
    header: "Expiry",
    cell: (t: Twin) => (
      <span className="block min-w-0">
        <span className={`block text-[12.5px] ${t.overdue ? "text-warn" : "text-ink"}`}>
          {expiryPhrase(t.expiresAt)}
        </span>
        <span className="block text-[12px] text-muted">
          Created <When value={t.createdAt} />
        </span>
      </span>
    ),
  },
];
