// The seam between the recruitment queue the control plane serves and the
// operator page that reads it.
//
// WHY THIS FILE EXISTS. Both halves of this lane passed their own suites and
// that proved nothing about the join between them. web/apps/api/test/
// recruitment.test.ts exercises the real routes against a real database and
// never loads a line of this console; the console typechecks and builds and
// never learns whether the procedure names in the strings it sends exist. A
// procedure path is a STRING here. Rename `remove` to `delete` on the router
// and nothing in either suite goes red: the console still compiles, the page
// still renders, and the button answers 404 the first time an operator presses
// it. That is the crossing, and every assertion below reads the control plane's
// own source rather than a copy of what it says.
//
// It is a test of the CONSOLE, which is why it lives here rather than beside
// the API suite. The question it answers is "does this client agree with that
// server", and the client is the half that is wrong when the answer is no.
//
// WHAT IT CANNOT DO, said out loud so nobody reads more into a pass than is
// there. It does not render the page: `npm test` in console/ is
// `node --test lib/*.test.ts` with no loader, no DOM and no `@/` alias, so a
// module importing React cannot be executed at all. Whether the queue draws,
// whether the panel opens, and whether the two buttons reach the network are
// facts about a browser and were checked in one. What is checkable without a
// browser is the agreement between the two sides, and that is what this is.

import { describe, test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { APPLICATION_ROLES, roleLabel } from "./recruitment-roles.ts";

const root = fileURLToPath(new URL("../../", import.meta.url));

/**
 * A file on the control plane side, or a failure that says where to look.
 *
 * MISSING IS A FAILURE AND NEVER A SKIP. A comparison that stands down when it
 * cannot find the other end reports an absent check as a pass, and this
 * repository has already paid for that once with a published-document gate that
 * was dark for the length of a queue without anybody being able to see it.
 */
function readOrFail(relative: string): string {
  try {
    return readFileSync(`${root}${relative}`, "utf8");
  } catch {
    throw new Error(
      `${relative} is not on this tree, so this console cannot be compared against the ` +
        `control plane. If that file moved, this reference has to move with it: the ` +
        `assertions below read it BY PATH and a missing file makes every one of them vacuous.`,
    );
  }
}

const recruitmentRouter = readOrFail("web/apps/api/src/admin/recruitment.ts");
const administrationRouter = readOrFail("web/apps/api/src/admin/administration.ts");
const permissions = readOrFail("web/apps/api/src/admin/permissions.ts");
const consoleClient = readOrFail("console/lib/admin-recruitment.ts");
const consoleNav = readOrFail("console/lib/admin-nav.ts");

/**
 * The migration that declares the table, found by CONTENT rather than by name.
 *
 * By content on purpose. Every migration's number is its filename, and a
 * parallel branch landing first renumbers this one: it was written as 0037 and
 * is 0038 because PR 229 got there first. A reference keyed on the filename has
 * a live trigger that fires on the next renumber, and it fires as a thrown
 * error in a test nobody expected to touch. Anchoring on the CREATE TABLE means
 * the reference survives the renumber, and a missing table still fails loudly
 * because the search returns nothing and this refuses to return nothing.
 */
function migrationDeclaring(table: string): string {
  const dir = `${root}web/packages/db/migrations`;
  const found = readdirSync(dir)
    .filter((name) => name.endsWith(".sql"))
    .map((name) => readFileSync(`${dir}/${name}`, "utf8"))
    .filter((sql) => new RegExp(`CREATE TABLE ${table}\\b`).test(sql));
  assert.equal(
    found.length,
    1,
    `expected exactly one migration to CREATE TABLE ${table} and found ${found.length}. ` +
      `Zero means the table is not on this tree and every check below it is vacuous; ` +
      `more than one means two migrations declare it and this cannot say which is live.`,
  );
  return found[0]!;
}

const applicationsSql = migrationDeclaring("recruitment_applications");

/** The values a column's CHECK ... IN (...) constraint allows. */
function checkedValues(sql: string, column: string): string[] {
  const match = new RegExp(`${column}[^,]*CHECK \\(${column} IN \\(([^)]*)\\)\\)`).exec(sql);
  assert.ok(
    match,
    `no CHECK (${column} IN (...)) on ${column} in the migration, so this test is reading the ` +
      `wrong constraint and would pass over any vocabulary at all`,
  );
  const values = [...match[1]!.matchAll(/'([a-z_]+)'/g)].map((m) => m[1]!);
  assert.ok(values.length > 0, `the CHECK on ${column} parsed to no values, which is an instrument fault`);
  return values.sort();
}

/** Every procedure the console sends to, read out of the console's own source. */
function procedurePathsInConsole(): string[] {
  return [...consoleClient.matchAll(/"(admin\.[a-z.]+)"/g)].map((m) => m[1]!).sort();
}

describe("the operator applications page against the control plane", () => {
  test("the sources were all read, so nothing below passes over the wrong file", () => {
    // The guard the rest of this file rests on. Every later assertion searches
    // one of these strings for a substring, and a regex over the wrong file
    // finds nothing and reports the absence as agreement.
    //
    // AN ANCHOR PER FILE, NOT A BYTE COUNT, and that correction is the reason
    // this comment is here. The first version of this guard asserted only
    // `length > 200`, and its own mutation cell walked straight through it:
    // pointing one of the six reads at a 221 byte page component left the guard
    // green while the file it was meant to be checking was not being read at
    // all. A length is a proxy for "this is a source file" and it cannot
    // distinguish the RIGHT source file, which is the only property that makes
    // the assertions below mean anything. Each anchor is a string that exists
    // in exactly one of these files.
    for (const [name, body, anchor] of [
      ["recruitment.ts", recruitmentRouter, "export const recruitmentRouter"],
      ["administration.ts", administrationRouter, "export const administrationRouter"],
      ["permissions.ts", permissions, "export const ADMIN_ROLE_PERMISSIONS"],
      ["admin-recruitment.ts", consoleClient, "export function useApplications"],
      ["admin-nav.ts", consoleNav, "export const ADMIN_NAV"],
      ["the applications migration", applicationsSql, "CREATE TABLE recruitment_applications"],
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
    const mounted = /applications:\s*recruitmentRouter/.test(administrationRouter);
    assert.ok(
      mounted,
      "administration.ts does not mount recruitmentRouter as `applications`, so every " +
        "admin.administration.applications.* path the console sends is a 404",
    );
    for (const path of procedurePathsInConsole()) {
      const procedure = path.replace("admin.administration.applications.", "");
      assert.notEqual(
        procedure,
        path,
        `${path} is not under admin.administration.applications, so this file is not the ` +
          `client that was meant to be checked here`,
      );
      assert.ok(
        new RegExp(`^\\s{2}${procedure}:`, "m").test(recruitmentRouter),
        `the console calls ${path} and recruitment.ts declares no \`${procedure}\` procedure`,
      );
    }
  });

  test("the console calls every procedure the router declares", () => {
    // The other direction, which catches the opposite mistake: a procedure
    // written, tested against the database, and reachable from no screen. A
    // route with no caller is a dead shippable gap that looks like a feature.
    const declared = [...recruitmentRouter.matchAll(/^ {2}([a-z][A-Za-z]*):/gm)].map((m) => m[1]!);
    assert.deepEqual(declared.sort(), ["list", "remove", "review"]);
    const called = procedurePathsInConsole();
    for (const procedure of declared) {
      assert.ok(
        called.includes(`admin.administration.applications.${procedure}`),
        `recruitment.ts declares \`${procedure}\` and no screen in this console calls it`,
      );
    }
  });

  test("the console knows exactly the roles the database will accept", () => {
    // Both directions. A value the schema allows and the console does not
    // renders as an unrecognized role; a value the console knows and the schema
    // refuses is a filter that can never match anything.
    assert.deepEqual(Object.keys(APPLICATION_ROLES).sort(), checkedValues(applicationsSql, "role"));
  });

  test("an unrecognized role shows the value rather than a blank", () => {
    // An absence displayed where there is a value is the worst direction for
    // this to be wrong in, because nobody investigates a blank.
    assert.equal(roleLabel("founding_engineer"), "Founding engineer");
    assert.equal(roleLabel("founding_growth"), "Founding growth");
    assert.match(roleLabel("founding_designer"), /founding_designer/);
  });

  test("the permissions this lane gates on are declared, and only owner holds them", () => {
    // The navigation hides the entry and the page hides the write buttons on
    // these two strings. A permission the server does not declare is a gate
    // that is open, or a nav entry that is hidden from everybody.
    for (const permission of ["admin.recruitment.read", "admin.recruitment.write"]) {
      assert.ok(
        new RegExp(`^\\s*'${permission}',`, "m").test(permissions),
        `${permission} is not in ADMIN_PERMISSIONS, so the gate that names it cannot resolve`,
      );
    }
    // Applicant data is not customer data and it is not incident context. Owner
    // is `[...ADMIN_PERMISSIONS]`, so it holds these; no other role's explicit
    // list may name them.
    const roleLists = permissions.slice(permissions.indexOf("ADMIN_ROLE_PERMISSIONS"));
    const named = [...roleLists.matchAll(/'admin\.recruitment\.[a-z]+'/g)];
    assert.deepEqual(
      named.map((m) => m[0]),
      [],
      "a role other than owner lists a recruitment permission explicitly. Applicant personal " +
        "data is deliberately reachable by owner alone, through owner's [...ADMIN_PERMISSIONS].",
    );
  });

  test("the navigation entry and the route it opens agree with the read permission", () => {
    // Three strings that have to be the same and live in three files: the nav
    // entry's href, the exported page's directory, and the permission the list
    // procedure demands. A drift in any one is a rail entry that opens nothing,
    // or an entry visible to a role the server then refuses.
    const entry = /\{[^{}]*href: "\/admin\/administration\/applications"[^{}]*\}/s.exec(consoleNav);
    assert.ok(entry, "admin-nav.ts has no entry for /admin/administration/applications");
    assert.match(entry[0], /permission: "admin\.recruitment\.read"/);
    assert.match(
      recruitmentRouter,
      /list: adminProcedure\('admin\.recruitment\.read'\)/,
      "the list procedure does not demand admin.recruitment.read, so the navigation shows the " +
        "section to a role the server will refuse",
    );
    for (const write of ["review", "remove"]) {
      assert.ok(
        new RegExp(`${write}: adminProcedure\\('admin\\.recruitment\\.write'\\)`).test(recruitmentRouter),
        `${write} does not demand admin.recruitment.write, so the page's write gate and the ` +
          `server's disagree about who may change an application`,
      );
    }
  });
});
