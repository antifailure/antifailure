# fixed

Every route in the console had the same document title.

All ten rendered `<title>Antifailure</title>` while their headings read
Environments, Runs, Audit, Provider keys, Masking, Members, Network, Plan and
Approve a terminal. This is a tool people keep open in several tabs while a run
goes, so every tab, every history entry and every tab search result was the
identical word.

Each route now carries its own name in a `layout.tsx` beside its page, and the
root supplies the suffix. A layout rather than the page, because every page in
the console is a client component and cannot export metadata; static metadata
rather than a title written after hydration, because the console is a static
export and the title belongs in the HTML.
