package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A console that calls a procedure four ways: a literal, a const beside the
// call, a path forwarded from a parameter, and a path this command cannot read.
const consoleFixture = `
import { query, mutate } from "../lib/api";

const EXITS_READ = "account.context";

export async function read() {
  return query<Exits>(EXITS_READ, {});
}

export async function write(slug: string) {
  return mutate("account.close", { confirm: slug });
}

export async function forwarded(path: string) {
  // The transport layer, which names no procedure of its own.
  return query(path, {});
}

export async function computed(kind: string) {
  return query(` + "`account.${kind}`" + `, {});
}

export function notAPath() {
  // query() here is a local helper and "select" is not a dotted path.
  return query("select");
}
`

func TestTheConsoleCallSitesAreReadFourWays(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "console/app/exits/page.tsx", consoleFixture)

	calls, unresolved, forwarders, err := FindConsoleCalls(filepath.Join(dir, "console"))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, c := range calls {
		paths = append(paths, c.Path)
	}
	want := map[string]bool{"account.context": true, "account.close": true}
	if len(paths) != 2 || !want[paths[0]] || !want[paths[1]] {
		t.Errorf("the paths read were %v, and the const and the literal are the two that name a procedure", paths)
	}
	if len(forwarders) != 1 {
		t.Errorf("%d forwarder(s) read, want 1: a path handed in by a caller is read at that caller, not here", len(forwarders))
	}
	if len(unresolved) != 1 {
		t.Fatalf("%d unreadable call(s), want 1: the template literal cannot be resolved and must be reported rather than skipped", len(unresolved))
	}
	if !strings.Contains(unresolved[0].Text, "account.$") {
		t.Errorf("the unreadable call reported was %q, want the computed one", unresolved[0].Text)
	}
	if unresolved[0].Line == 0 || !strings.HasSuffix(unresolved[0].File, "page.tsx") {
		t.Errorf("an unreadable call must name its file and line, got %s:%d", unresolved[0].File, unresolved[0].Line)
	}
}

func TestAPathInACommentIsNotACallSite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "console/app/x/page.tsx", `
// Once upon a time this called query("account.exits") and nothing noticed.
/* query("account.alsoNotReal") in a block comment. */
export const nothing = 1;
`)
	calls, unresolved, _, err := FindConsoleCalls(filepath.Join(dir, "console"))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 || len(unresolved) != 0 {
		t.Errorf("comments named %d call(s) and %d unreadable call(s), want none: a rule that trips on prose is one people route around by rewording the prose", len(calls), len(unresolved))
	}
}

// A control plane shaped like the real one: a mount table, a router in another
// file, a router written in place, a router nested two deep, and a spread.
const serverFixture = `
import { adminRouter } from '../admin/router.ts'

export const accountRouter = router({
  context: orgProcedure('account.close').query(async ({ ctx }) => {
    return { ok: true }
  }),
  close: orgProcedure('account.close').mutation(async ({ ctx }) => {
    return { ok: true }
  }),
})

export const organizationSettings = {
  rename: orgProcedure('organization.settings').mutation(async () => ({})),
}

const orgRouter = router({
  status: orgProcedure('environments.view').query(async () => ({})),
  ...organizationSettings,
})

export const appRouter = router({
  account: accountRouter,
  org: orgRouter,
  admin: adminRouter,
})
`

const adminFixture = `
export const auditChainRoutes = {
  verify: adminProcedure('admin.audit.export').query(async () => ({})),
}

export const adminRouter = router({
  me: adminProcedure('admin.portal.access').query(() => ({})),
  audit: router({
    list: adminProcedure('admin.audit.read').query(async () => ({})),
    ...auditChainRoutes,
  }),
  customers: router({
    notes: router({
      list: adminProcedure('admin.customers.read').query(async () => ({})),
    }),
  }),
})
`

func TestTheMountTableIsFollowedThroughFilesNestingAndSpreads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routers/index.ts", serverFixture)
	writeFile(t, dir, "admin/router.ts", adminFixture)

	procedures, mounts, err := ParseServerProcedures(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"account.context",            // a router in the same file
		"account.close",              // its second procedure, after a .query( block
		"org.status",                 // a router declared without export
		"org.rename",                 // reached only through a spread
		"admin.me",                   // a router in another directory
		"admin.audit.list",           // a router written in place
		"admin.audit.verify",         // a spread inside that nested router
		"admin.customers.notes.list", // nested two deep
	} {
		if !procedures[want] {
			t.Errorf("%s was not read as a registered procedure, so the console calling it would be refused while it works", want)
		}
	}
	if procedures["account.exits"] {
		t.Error("account.exits was read as registered, and the whole point is that it is not")
	}
	for _, want := range []string{"account", "org", "admin", "admin.audit", "admin.customers.notes"} {
		if !mounts[want] {
			t.Errorf("%s was not read as a mount, and an unknown mount is how a dead path gets ignored", want)
		}
	}
}

func TestAServerWithNoMountTableIsRefusedRatherThanRead(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routers/index.ts", `export const accountRouter = router({
  context: orgProcedure('account.close').query(async () => ({})),
})
`)
	if _, _, err := ParseServerProcedures(dir); err == nil {
		t.Fatal("a tree with no appRouter was read as valid. Every console path would then pass, which is the silence this check exists to remove")
	}
}

func TestADeadConsolePathIsRefusedAndNamed(t *testing.T) {
	procedures := map[string]bool{"account.context": true, "account.close": true}
	mounts := map[string]bool{"account": true}
	calls := []ConsoleCall{
		{File: "console/app/(app)/exits/page.tsx", Line: 81, Path: "account.exits", Text: `query(EXITS_READ, {})`},
		{File: "console/app/(app)/exits/page.tsx", Line: 90, Path: "account.close", Text: `mutate("account.close", {})`},
		{File: "console/lib/other.ts", Line: 7, Path: "stripe.webhook", Text: `query("stripe.webhook")`},
	}
	err := CheckConsoleCalls(calls, nil, procedures, mounts)
	if err == nil {
		t.Fatal("a console path with no procedure behind it was accepted, which is the 404 this check exists to refuse")
	}
	msg := err.Error()
	for _, want := range []string{"account.exits", "exits/page.tsx", "81", "registers no exits"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not contain %q, and a refusal that does not name the call site leaves somebody hunting it:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "account.close") {
		t.Error("a registered procedure was reported as dead")
	}
	if strings.Contains(msg, "stripe.webhook") {
		t.Error("a path whose first segment is not a control plane mount was reported, and this command cannot know what that is")
	}
}

func TestAnUnreadablePathIsAFailureAndNotASilence(t *testing.T) {
	err := CheckConsoleCalls(nil, []ConsoleCall{{File: "console/lib/api.ts", Line: 12, Text: "query(`account.${kind}`)"}},
		map[string]bool{"account.context": true}, map[string]bool{"account": true})
	if err == nil {
		t.Fatal("a call site whose path could not be read was passed over. A gate that skips what it cannot parse reports a clean run over the one call site that was going to break")
	}
	if !strings.Contains(err.Error(), "api.ts:12") {
		t.Errorf("the refusal must name the file and line, got:\n%s", err.Error())
	}
}

func TestAConsoleThatNamesOnlyRealProceduresPasses(t *testing.T) {
	err := CheckConsoleCalls(
		[]ConsoleCall{{File: "console/app/x/page.tsx", Line: 3, Path: "account.context"}},
		nil,
		map[string]bool{"account.context": true},
		map[string]bool{"account": true},
	)
	if err != nil {
		t.Fatalf("a console naming a registered procedure was refused:\n%v", err)
	}
}
