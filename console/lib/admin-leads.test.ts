// The seam between the enterprise leads queue the control plane serves and the
// operator page that reads it.
//
// WHY THIS FILE EXISTS. Both halves of this lane pass their own suites and that
// proves nothing about the join between them. web/apps/api/test/leads-admin.
// test.ts exercises the real routes against a real database and never loads a
// line of this console; the console typechecks and builds and never learns
// whether the procedure names in the strings it sends exist. A procedure path
// is a STRING here. Rename `handle` to `resolve` on the router and nothing in
// either suite goes red: the console still compiles, the page still renders, and
// the button answers 404 the first time an operator presses it. That is the
// crossing, and every assertion below reads the control plane's own source
// rather than a copy of what it says.
//
// WHAT IT CANNOT DO, said out loud. It does not render the page: `npm test` in
// console/ is `node --test lib/*.test.ts` with no loader, no DOM and no `@/`
// alias, so a module importing React cannot be executed at all. Whether the
// queue draws and whether the button reaches the network are facts about a
// browser. What is checkable without one is the agreement between the two
// sides, and that is what this is.

import { describe, test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../", import.meta.url));

/**
 * A file on the control plane side, or a failure that says where to look.
 *
 * MISSING IS A FAILURE AND NEVER A SKIP. A comparison that stands down when it
 * cannot find the other end reports an absent check as a pass, and this
 * repository has already paid for that once.
 */
function readOrFail(relative: string): string {
  try {
    return readFileSync(`${root}${relative}`, "utf8");
  } catch {
    throw new Error(
      `${relative} is not on this tree, so this console cannot be compared against the ` +
        `control plane. The assertions below read it BY PATH and a missing file makes every ` +
        `one of them vacuous.`,
    );
  }
}

const leadsRouter = readOrFail("web/apps/api/src/admin/leads.ts");
const administrationRouter = readOrFail("web/apps/api/src/admin/administration.ts");
const permissions = readOrFail("web/apps/api/src/admin/permissions.ts");
const consoleClient = readOrFail("console/lib/admin-leads.ts");
const consoleNav = readOrFail("console/lib/admin-nav.ts");

/**
 * The migration that grants the operator role UPDATE, found by CONTENT rather
 * than by filename. A parallel branch landing first renumbers this file, so a
 * reference keyed on the number has a live trigger that fires on the next
 * renumber. Anchoring on the grant survives it, and a missing grant still fails
 * loudly because the search returns nothing and this refuses to return nothing.
 */
function migrationGranting(pattern: RegExp): string {
  const dir = `${root}web/packages/db/migrations`;
  const found = readdirSync(dir)
    .filter((name) => name.endsWith(".sql"))
    .map((name) => readFileSync(`${dir}/${name}`, "utf8"))
    .filter((sql) => pattern.test(sql));
  assert.equal(
    found.length,
    1,
    `expected exactly one migration matching ${pattern} and found ${found.length}. Zero means ` +
      `the grant is not on this tree and the mark-handled write cannot work; more than one ` +
      `means two migrations declare it and this cannot say which is live.`,
  );
  return found[0]!;
}

/** Every procedure the console sends to, read out of the console's own source. */
function procedurePathsInConsole(): string[] {
  return [...consoleClient.matchAll(/"(admin\.[a-z.]+)"/g)].map((m) => m[1]!).sort();
}

describe("the operator leads page against the control plane", () => {
  test("the sources were all read, so nothing below passes over the wrong file", () => {
    // An anchor per file, not a byte count. A length is a proxy for "this is a
    // source file" and cannot distinguish the RIGHT one, which is the only
    // property that makes the assertions below mean anything.
    for (const [name, body, anchor] of [
      ["leads.ts", leadsRouter, "export const leadsRouter"],
      ["administration.ts", administrationRouter, "export const administrationRouter"],
      ["permissions.ts", permissions, "export const ADMIN_ROLE_PERMISSIONS"],
      ["admin-leads.ts", consoleClient, "export function useLeads"],
      ["admin-nav.ts", consoleNav, "export const ADMIN_NAV"],
    ] as const) {
      assert.ok(
        body.includes(anchor),
        `${name} was read as ${body.length} bytes and does not contain ${anchor}, so this file ` +
          `is reading something other than what it names and every assertion below it is vacuous`,
      );
    }
    assert.ok(
      procedurePathsInConsole().length > 0,
      "no procedure path was parsed out of the console client, so the crossing below is vacuous",
    );
  });

  test("every procedure the console calls exists on the control plane", () => {
    // The defect this is here for: a procedure path is a string, so a rename on
    // the router leaves the console compiling and answering 404 at runtime.
    const mounted = /leads:\s*leadsRouter/.test(administrationRouter);
    assert.ok(
      mounted,
      "administration.ts does not mount leadsRouter as `leads`, so every " +
        "admin.administration.leads.* path the console sends is a 404",
    );
    const paths = procedurePathsInConsole();
    assert.deepEqual(
      paths,
      ["admin.administration.leads.handle", "admin.administration.leads.list"],
      "the console sends procedure paths other than the two leads routes; this crossing is not " +
        "checking what it names",
    );
    for (const path of paths) {
      const procedure = path.replace("admin.administration.leads.", "");
      assert.notEqual(procedure, path, `${path} is not under admin.administration.leads`);
      assert.ok(
        new RegExp(`^\\s{2}${procedure}:`, "m").test(leadsRouter),
        `the console calls ${path} and leads.ts declares no \`${procedure}\` procedure`,
      );
    }
  });

  test("the console calls every procedure the router declares", () => {
    // The other direction: a procedure written, tested against the database, and
    // reachable from no screen is a dead shippable gap that looks like a feature.
    const declared = [...leadsRouter.matchAll(/^ {2}([a-z][A-Za-z]*):/gm)].map((m) => m[1]!);
    assert.deepEqual(declared.sort(), ["handle", "list"]);
    const called = procedurePathsInConsole();
    for (const procedure of declared) {
      assert.ok(
        called.includes(`admin.administration.leads.${procedure}`),
        `leads.ts declares \`${procedure}\` and no screen in this console calls it`,
      );
    }
  });

  test("the permissions this lane gates on are declared, and only owner holds them", () => {
    for (const permission of ["admin.leads.read", "admin.leads.write"]) {
      assert.ok(
        new RegExp(`^\\s*'${permission}',`, "m").test(permissions),
        `${permission} is not in ADMIN_PERMISSIONS, so the gate that names it cannot resolve`,
      );
    }
    // Lead data is not customer data and it is not incident context. Owner is
    // `[...ADMIN_PERMISSIONS]`, so it holds these; no other role's explicit list
    // may name them.
    const roleLists = permissions.slice(permissions.indexOf("ADMIN_ROLE_PERMISSIONS"));
    const named = [...roleLists.matchAll(/'admin\.leads\.[a-z]+'/g)];
    assert.deepEqual(
      named.map((m) => m[0]),
      [],
      "a role other than owner lists a leads permission explicitly. Lead personal data is " +
        "deliberately reachable by owner alone, through owner's [...ADMIN_PERMISSIONS].",
    );
  });

  test("the read route, the write route and the nav entry agree on the permissions", () => {
    assert.match(
      leadsRouter,
      /list: adminProcedure\('admin\.leads\.read'\)/,
      "the list procedure does not demand admin.leads.read, so the navigation shows the section " +
        "to a role the server will refuse",
    );
    assert.match(
      leadsRouter,
      /handle: adminProcedure\('admin\.leads\.write'\)/,
      "the handle procedure does not demand admin.leads.write, so the page's write gate and the " +
        "server's disagree about who may answer a lead",
    );
    const entry = /\{[^{}]*href: "\/admin\/administration\/leads"[^{}]*\}/s.exec(consoleNav);
    assert.ok(entry, "admin-nav.ts has no entry for /admin/administration/leads");
    assert.match(entry[0], /permission: "admin\.leads\.read"/);
  });

  test("a migration grants the operator role UPDATE so the handle write is real", () => {
    // The write the page offers is only real because migration 0047 grants
    // antifailure_admin UPDATE on the table. Without it the button would list a
    // lead, open it, and fail with 42501 the instant it was pressed.
    const grant = migrationGranting(/GRANT UPDATE ON enterprise_leads TO antifailure_admin/);
    // And the serving role's boundary is untouched: this migration must not
    // widen antifailure_app, which 0035 keeps INSERT-only.
    assert.ok(
      !/TO antifailure_app/.test(grant),
      "the UPDATE-grant migration also touches antifailure_app, which would risk the leak " +
        "boundary 0035 drew: the serving role stays INSERT-only",
    );
  });
});
