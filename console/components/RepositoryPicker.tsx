"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { query, useApi } from "@/lib/api";
import { Empty, ErrorState } from "@/components/ui";
import type { ReactNode } from "react";

export interface Repository {
  id: string;
  full_name: string;
  default_branch: string;
  private: boolean;
  archived_at: string | null;
  created_at: string;
}

/**
 * Masking rules and network policy are per repository, so these screens cannot
 * render at all until one is chosen.
 *
 * The choice lives in the query string rather than in component state, which
 * makes a specific repository's masking rules a link somebody can send. With a
 * static export there is no other place to put it: there are no dynamic route
 * segments to hold it.
 */
export function WithRepository({
  children,
}: {
  children: (repository: string, all: Repository[]) => ReactNode;
}) {
  const params = useSearchParams();
  const router = useRouter();
  const asked = params.get("repo");
  const state = useApi<Repository[]>(() => query("repositories.list", { includeArchived: false }), []);

  if (state.status === "loading") {
    return (
      <div className="h-9 w-[260px] animate-pulse rounded-[5px] bg-[rgba(16,16,16,0.07)]" role="status">
        <span className="sr-only">Loading repositories</span>
      </div>
    );
  }
  if (state.status === "error" && state.error) {
    return <ErrorState error={state.error} retry={state.reload} />;
  }

  const repos = state.data ?? [];
  if (repos.length === 0) {
    return (
      <Empty title="No repositories connected">
        Masking rules and network policy belong to a repository. One appears
        here when the GitHub App reports an installation that includes it.
      </Empty>
    );
  }

  const current = repos.find((r) => r.full_name === asked)?.full_name ?? repos[0]!.full_name;

  return (
    <>
      {repos.length > 1 ? (
        <div className="mb-5">
          <label className="block text-[12px] font-medium text-muted" htmlFor="repository">
            Repository
          </label>
          <select
            id="repository"
            value={current}
            onChange={(e) => {
              const next = new URLSearchParams(Array.from(params.entries()));
              next.set("repo", e.target.value);
              router.replace(`?${next.toString()}`);
            }}
            className="mt-1.5 h-9 w-full max-w-[380px] rounded-[5px] border border-rule bg-card px-2.5 text-[13px] text-ink outline-none focus:border-rule-strong"
          >
            {repos.map((r) => (
              <option key={r.id} value={r.full_name}>
                {r.full_name}
              </option>
            ))}
          </select>
        </div>
      ) : null}
      {children(current, repos)}
    </>
  );
}
