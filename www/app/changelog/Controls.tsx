"use client";

import { useCallback, useEffect, useRef, useState } from "react";

/**
 * Search across the changelog, and a way to open all of it at once.
 *
 * It filters the page that is already there rather than owning the entries
 * itself. Passing 190 entries into a client component would put every
 * paragraph on the page twice, once as HTML and once as serialised props, on
 * the page that is already the largest on this site. So the entries stay
 * server rendered and this reads the DOM: `[data-entry]` is one entry, and the
 * text it searches is that entry's own `textContent`, which costs nothing to
 * ship because it is the entry. A collapsed entry is in the document, so the
 * search reaches every word of the changelog and not only the lines showing.
 *
 * Nothing renders until it has mounted. A search box that does nothing is
 * worse than no search box, and with JavaScript off the page is still the
 * whole changelog: every entry is in the HTML, grouped, headed by its own
 * opening sentence, and a `details` element opens on click without help from
 * anything here.
 *
 * There are no category filters, and there were. They filtered to exactly what
 * the category links in each release already scroll to, in a second row of
 * pills carrying the same four words and the same four numbers as the first.
 * Two controls that look alike and do different things is worse than one, so
 * the categories stay a server rendered index that works with JavaScript off,
 * and this owns the one thing an index cannot do, which is find a word.
 */
export function ChangelogControls({ total }: { total: number }) {
  const [ready, setReady] = useState(false);
  const [query, setQuery] = useState("");
  const [shown, setShown] = useState(total);
  const [expanded, setExpanded] = useState(false);
  const rows = useRef<{ el: HTMLDetailsElement; text: string }[] | null>(null);

  // Read once and kept. Every entry's text is fixed at build time, and
  // recomputing it on each keystroke is 190 tree walks per character typed.
  const index = useCallback(() => {
    if (rows.current) return rows.current;
    rows.current = [...document.querySelectorAll<HTMLDetailsElement>("[data-entry]")].map((el) => ({
      el,
      text: (el.textContent ?? "").toLowerCase(),
    }));
    return rows.current;
  }, []);

  useEffect(() => setReady(true), []);

  // An entry linked to by name opens itself. Browsers disagree about whether a
  // fragment reaches inside a closed `details`, and a permalink that lands a
  // reader on a collapsed row they then have to find and click is a link that
  // half worked.
  useEffect(() => {
    const open = () => {
      const id = decodeURIComponent(window.location.hash.replace(/^#/, ""));
      if (!id) return;
      const details = document.getElementById(id)?.closest("details");
      if (details instanceof HTMLDetailsElement && !details.open) {
        details.open = true;
        details.scrollIntoView({ block: "start" });
      }
    };
    open();
    window.addEventListener("hashchange", open);
    return () => window.removeEventListener("hashchange", open);
  }, []);

  useEffect(() => {
    if (!ready) return;
    const needle = query.trim().toLowerCase();

    let visible = 0;
    for (const row of index()) {
      const matches = needle === "" || row.text.includes(needle);
      row.el.hidden = !matches;
      if (matches) visible++;
    }

    // Every count on the page counts what is actually on it. A heading over
    // eight results reading 113, or an index chip offering to scroll to a
    // category the search has emptied, is the page telling a reader something
    // it can see is untrue.
    for (const group of document.querySelectorAll<HTMLElement>("[data-group]")) {
      const inside = [...group.querySelectorAll<HTMLDetailsElement>("[data-entry]")];
      const count = inside.filter((entry) => !entry.hidden).length;
      group.hidden = count === 0;
      const badge = group.querySelector("[data-group-count]");
      if (badge) badge.textContent = String(count);

      const anchor = group.querySelector("h3")?.id;
      const chip = anchor ? document.querySelector<HTMLElement>(`[data-chip="${anchor}"]`) : null;
      if (chip) {
        chip.hidden = count === 0;
        const chipCount = chip.querySelector("[data-chip-count]");
        if (chipCount) chipCount.textContent = String(count);
      }
    }

    // A release whose every entry is filtered out is hidden with them, rather
    // than left as a heading over nothing. The two that predate the convention
    // carry no entries at all and hide as soon as anything is being searched
    // for, because they cannot be the answer to it.
    for (const release of document.querySelectorAll<HTMLElement>("[data-release]")) {
      const inside = [...release.querySelectorAll<HTMLDetailsElement>("[data-entry]")];
      const count = inside.filter((entry) => !entry.hidden).length;
      release.hidden = needle !== "" && count === 0;
      const badge = release.querySelector("[data-release-count]");
      if (badge) badge.textContent = String(count);
    }

    setShown(visible);
  }, [ready, query, index]);

  if (!ready) return null;

  // Only what is showing, so expanding after a search opens the answer and not
  // the hundred and eighty entries the search just ruled out.
  const setAllOpen = (open: boolean) => {
    for (const row of index()) if (!row.el.hidden) row.el.open = open;
    setExpanded(open);
  };

  return (
    <div className="border-t border-black/12 pt-6">
      <div className="flex flex-wrap items-center gap-x-10 gap-y-5">
        <label
          htmlFor="changelog-search"
          className="flex min-w-0 flex-1 items-center gap-x-3 border-b border-black/20 pb-2.5 focus-within:border-black max-lg:max-w-none max-md:w-full max-md:flex-none max-w-[560px]"
        >
          <span className="font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-gray-new-40">
            Find
          </span>
          {/* 16px, not smaller: iOS Safari zooms the page when a field under
              16px takes focus, and the zoom does not come back. */}
          <input
            id="changelog-search"
            type="search"
            value={query}
            autoComplete="off"
            onChange={(event) => setQuery(event.target.value)}
            placeholder={`a word in any of the ${total} entries`}
            // The browser's own clear button comes off. Chrome draws it in the
            // accent blue, which is the only saturated colour anywhere on this
            // page, and the Clear beside the result count does the same job in
            // the site's own type and is reachable from the keyboard.
            className="min-w-0 flex-1 bg-transparent text-[16px] tracking-extra-tight text-black outline-none [&::-webkit-search-cancel-button]:appearance-none placeholder:text-gray-new-50"
          />
        </label>

        <button
          type="button"
          onClick={() => setAllOpen(!expanded)}
          className="inline-flex min-h-11 cursor-pointer items-center text-[15px] tracking-extra-tight text-black underline decoration-black/25 underline-offset-4 hover:decoration-black focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-black/60"
        >
          {expanded ? "Collapse all" : "Expand all"}
        </button>
      </div>

      {/* Empty until a search narrows something, because the release below
          already says how many entries there are and printing the same number
          twice in four inches teaches a reader to stop reading both. The
          element stays in the tree either way, and takes no room while it is
          empty: a live region announces a change to its own contents, and one
          created at the moment of the change announces nothing. The line
          appearing moves the page by its own height, which is a shift inside
          the half second after a keystroke and so is not one the layout
          stability measure counts, nor one a person typing reads as the page
          moving under them. */}
      <p
        aria-live="polite"
        className="font-mono text-[11px] uppercase tracking-[0.12em] text-gray-new-40 empty:hidden [&:not(:empty)]:mt-4"
      >
        {query.trim() === "" ? null : (
          <>
            {shown === 0 ? "Nothing matches" : `${shown} of ${total} entries`}
            {" · "}
            <button
              type="button"
              onClick={() => setQuery("")}
              className="-my-3 cursor-pointer py-3 uppercase underline decoration-black/25 underline-offset-4 hover:text-black hover:decoration-black focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-black/60"
            >
              Clear
            </button>
          </>
        )}
      </p>

      {shown === 0 ? (
        <p className="mt-6 max-w-[720px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40 max-md:text-[16px]">
          Nothing here contains that. The search reads every word of every entry, the collapsed ones
          included, so a term it cannot find was not written.
        </p>
      ) : null}
    </div>
  );
}
