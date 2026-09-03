// The navigation contract, checked against the two things it claims about.
//
// lib/admin-nav.ts says three things that live somewhere else: that every
// section has a page at the route it names, that every permission it filters on
// exists in the server's catalog, and that every group has a router module. All
// three are true today and none of them is enforced by anything the compiler
// sees, because a route is a directory on disk, a permission is a string, and a
// module is a filename.
//
// That matters more here than it usually would. Twenty two sections are being
// built by six people at once, and the three ways this quietly breaks are all
// invisible in the diff that causes them:
//
//   somebody renames a directory and the rail gets a 404 in it, which teaches
//     the reader to distrust the rail rather than the one broken entry;
//   somebody filters on a permission the catalog does not have, and
//     adminRoleHas returns false for it, so the entry is hidden from EVERY
//     operator including the owner and it looks like a permissions bug;
//   a group is added to the navigation with no module behind it, so the lane
//     has nowhere to put its routes.
//
// The server's own half of this, that the six modules are actually mounted, is
// web/apps/api/test/admin-namespaces.test.ts.

// READ AS SOURCE RATHER THAN IMPORTED, and that is forced rather than chosen.
// admin-nav.ts binds each entry to its icon, the icons are JSX, and `node
// --test` runs this file with no bundler and no "@/" alias, so the module
// cannot be loaded here at all. Parsing it is what is available.
//
// A parser is only as good as its refusal to pass on nothing, which is the way
// a text scraping test usually fails: the pattern stops matching, zero entries
// come back, every assertion below passes over an empty list, and the suite
// reports green while checking nothing. So the first test asserts the COUNT,
// and every list this file builds is asserted non empty before it is used.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";

const here = new URL(".", import.meta.url);
const appDir = new URL("../app/", here);
const apiDir = new URL("../../web/apps/api/src/admin/", here);

const NAV_SOURCE = readFileSync(new URL("admin-nav.ts", here), "utf8");

interface Entry {
  label: string;
  href: string;
  permission: string;
}

/** Every entry, overview included, in the order they are declared. */
const ENTRIES: Entry[] = [
  ...NAV_SOURCE.matchAll(
    /label: "([^"]+)",\s*\n\s*href: "([^"]+)",\s*\n\s*Icon: \w+,\s*\n\s*permission: "([^"]+)",/g,
  ),
].map((m) => ({ label: m[1]!, href: m[2]!, permission: m[3]! }));

/** The groups, each with the labels of the entries declared inside it. */
const GROUPS: { label: string; slug: string; items: Entry[] }[] = [
  ...NAV_SOURCE.matchAll(/\n    label: "([^"]+)",\n    slug: "(\w+)",\n    items: \[/g),
].map((m, i, all) => {
  const from = m.index! + m[0]!.length;
  const to = i + 1 < all.length ? all[i + 1]!.index! : NAV_SOURCE.length;
  const body = NAV_SOURCE.slice(from, to);
  const hrefs = new Set([...body.matchAll(/href: "([^"]+)"/g)].map((h) => h[1]!));
  return {
    label: m[1]!,
    slug: m[2]!,
    items: ENTRIES.filter((e) => hrefs.has(e.href)),
  };
});

describe("the operator portal's navigation", () => {
  test("the declaration was read, so nothing below passes over an empty list", () => {
    // The guard the rest of this file rests on. Twenty two sections in six
    // groups, plus the overview above them, is the information architecture;
    // any other number here means the parser has stopped seeing the file and
    // every assertion after it is vacuous.
    assert.equal(ENTRIES.length, 23, "expected the overview plus twenty two sections");
    assert.equal(GROUPS.length, 6);
    for (const g of GROUPS) {
      assert.ok(g.items.length > 0, `no entries were read out of the ${g.label} group`);
    }
  });

  test("every entry opens a page that exists", () => {
    // The assertion that would have caught every rename. A route in this list
    // with no page under it is a dead entry in the rail, and a dead entry in a
    // rail of twenty three is worse than a missing one: the reader cannot tell
    // which of the other twenty two are also lying.
    const missing = ENTRIES.filter(
      (item) => !existsSync(new URL(`.${item.href}/page.tsx`, appDir)),
    ).map((item) => `${item.label} -> ${item.href}`);
    assert.deepEqual(
      missing,
      [],
      `these navigation entries have no page, so they 404:\n  ${missing.join("\n  ")}`,
    );
  });

  test("no two entries claim the same route", () => {
    // Two entries on one route is two labels for one page, and which one wins
    // depends on which the reader clicked, which is not a thing a reader can
    // know.
    const seen = new Map<string, string>();
    const clashes: string[] = [];
    for (const item of ENTRIES) {
      const first = seen.get(item.href);
      if (first) clashes.push(`${item.href} is claimed by "${first}" and "${item.label}"`);
      else seen.set(item.href, item.label);
    }
    assert.deepEqual(clashes, [], clashes.join("\n  "));
  });

  test("every permission it filters on exists in the platform catalog", () => {
    // A permission that is not in the catalog does not fail loudly. adminRoleHas
    // returns false for it, so the entry is hidden from every operator
    // including the owner, and it reads as a permissions bug rather than as the
    // spelling mistake it is.
    const catalog = readFileSync(new URL("permissions.ts", apiDir), "utf8");
    const declared = new Set(
      [...catalog.matchAll(/^ {2}'(admin\.[a-z.]+)',$/gm)].map((m) => m[1]),
    );
    assert.ok(
      declared.size > 10,
      `only ${declared.size} permissions were read out of admin/permissions.ts, so this test is ` +
        "matching the wrong shape and is about to pass on anything.",
    );
    const unknown = ENTRIES.filter((item) => !declared.has(item.permission)).map(
      (item) => `${item.label} filters on ${item.permission}`,
    );
    assert.deepEqual(
      unknown,
      [],
      `these permissions are not in web/apps/api/src/admin/permissions.ts:\n  ${unknown.join("\n  ")}`,
    );
  });

  test("every group has the router module its slug names", () => {
    const missing = GROUPS.filter(
      (group) => !existsSync(new URL(`${group.slug}.ts`, apiDir)),
    ).map((group) => `${group.label} -> src/admin/${group.slug}.ts`);
    assert.deepEqual(
      missing,
      [],
      `these navigation groups have nowhere to put their routes:\n  ${missing.join("\n  ")}`,
    );
  });

  test("every group's routes sit under the group's own slug", () => {
    // The whole anti collision arrangement in one line: an agent owns
    // /admin/<slug> in the console and src/admin/<slug>.ts on the server. A
    // section filed under another group's prefix puts two people in one
    // directory, which is the thing the arrangement exists to prevent.
    const strays = GROUPS.flatMap((group) =>
      group.items
        .filter((item) => !item.href.startsWith(`/admin/${group.slug}/`))
        .map((item) => `${item.label} is in ${group.label} and routes to ${item.href}`),
    );
    assert.deepEqual(strays, [], strays.join("\n  "));
  });

  test("the overview is not inside a group, and every group has entries", () => {
    assert.equal(ENTRIES[0]!.href, "/admin", "the overview is not the first entry declared");
    assert.ok(
      !GROUPS.some((g) => g.items.some((i) => i.href === "/admin")),
      "the overview is listed inside a group as well as above them, so it renders twice",
    );
    const empty = GROUPS.filter((g) => g.items.length === 0).map((g) => g.label);
    // A heading over nothing reads as a section that failed to load.
    assert.deepEqual(empty, [], `these groups have no entries: ${empty.join(", ")}`);
  });

  test("the six groups are the ones the information architecture names, in order", () => {
    // Order is the product owner's, not ours, and it is the kind of thing a
    // refactor reorders without noticing because nothing breaks when it does.
    assert.deepEqual(
      GROUPS.map((g) => g.label),
      [
        "Customers",
        "Product",
        "Developer Platform",
        "Operations",
        "Security & Governance",
        "Administration",
      ],
    );
    assert.equal(
      GROUPS.reduce((n, g) => n + g.items.length, 0),
      22,
      "the information architecture has twenty two sections in six groups",
    );
  });
});
