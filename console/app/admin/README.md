# Adding a section to the operator portal

Six navigation groups, twenty two sections, built in parallel. The arrangement
below is what keeps two people out of one file. Read it once; it is shorter than
the first merge conflict it prevents.

## The three files you touch, and the one you do not

| You own | Path |
| --- | --- |
| your page | `console/app/admin/<group>/<section>/page.tsx` |
| your routes | `web/apps/api/src/admin/<group>.ts` |
| nothing else | |

`<group>` is one of `customers`, `product`, `platform`, `operations`,
`security`, `administration`. Your group's module is already written, already
mounted in `web/apps/api/src/admin/router.ts`, and already walked by the route
matrix. Add your procedures to it and they are served at `admin.<group>.<name>`.

**Do not edit `console/lib/admin-nav.ts`.** It declares every label, route, icon
and permission once, and every screen reads them from it. The single exception
is the `permission` on your own section: when your routes land, change that one
line to the permission they actually declare, because until then it names the
nearest capability that exists.

**Do not add a mount to `router.ts`.** Yours is there. A second `admin:` key, or
a second key for your group, is a clean merge that silently discards somebody's
routes, which is why `test/admin-namespaces.test.ts` counts them.

## Writing the page

Replace the body of your `page.tsx`. It currently reads:

```tsx
export default function ProductTwinsPage() {
  return <PlannedSection href="/admin/product/twins" />;
}
```

Build the real one out of `@/components/admin/primitives` and
`@/components/ui`. Take the heading from `AdminPage href="..."` rather than
passing a title string, so the page and the rail entry cannot drift.

```tsx
<AdminPage href="/admin/product/twins" actions={<Button>Refresh</Button>}>
  <Card>
    <FilterBar search={{ value: q, onChange: setQ, label: "Search twins" }} />
    <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={5} />}>
      {(rows) => (
        <DataTable
          columns={columns}
          rows={rows}
          keyOf={(t) => t.id}
          href={(t) => `/admin/product/twins/detail?id=${t.id}`}
          empty={<EmptyList title="No twins">Nothing is running here yet.</EmptyList>}
          footer={<More shown={rows.length} noun={{ one: "twin", many: "twins" }} {...page} />}
        />
      )}
    </Loaded>
  </Card>
</AdminPage>
```

What each primitive is for:

- `AdminPage` heading, description and actions, titled from the navigation.
- `DataTable` rows, right aligned tabular numerics, per page selection, and
  server side sorting. Pass `onSort` **only** when the route can actually order
  the whole list. There is no client side sort in it on purpose: reordering the
  fifty rows you fetched and presenting them as the top of the list is a
  confident wrong answer.
- `FilterBar` search and filters. The search submits rather than firing on every
  keystroke, because each one is a cross tenant query.
- `Drawer` one record beside the list it came from. A native `dialog`, so focus
  containment, Escape and the inert background come with it.
- `Facts` the fields of one record, as a description list.
- `StatusChip` a state, coloured by what the word means.
- `Metric` and `MetricRow` numbers, which say "Not measured" rather than
  printing a zero they were not given.
- `Loaded`, `TableSkeleton`, `EmptyList`, `ErrorState` from `ui.tsx` are the
  three states. Build all three. An empty state says why it is empty and gives
  the one action that fills it; an error state says what happened in words and
  offers a retry that works.

The console is a **static export**, so there are no dynamic route segments. A
detail page is a sibling route reading a query string: see
`customers/users/organization/page.tsx`.

Column headings can be as long as they need to be. Below the phone breakpoint a
table becomes a stacked record and the heading sits beside its value, and the
heading column is a subgrid sized to the longest heading in that record, capped
at 45% of the width, wrapping past that. It used to be a fixed 10.5ch track that
a longer heading ran straight into, so `ENVIRONMENTS` collided with its own
value at 320px. Do not shorten a heading to work around that; it is fixed.

## Writing the routes

```ts
export const productRouter = router({
  list: adminProcedure('admin.infra.read')
    .input(z.object({ limit: z.number().int().min(1).max(200).default(50) }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb((db) => db.execute(sql`...`))
    }),
})
```

- `adminProcedure(permission)` is the only way to build one, so declaring the
  permission and creating the route are one act.
- Every read goes through `ctx.adminDb`, which is the operator pool. `ctx.pool`
  cannot see across tenants and should not be able to.
- A mutation records what it changed with `adminAudit`, in the same transaction
  as the change. Reads are audited for you, per request.
- A new permission goes in `src/admin/permissions.ts` under **your lane's
  prefix**, with a description and at least one role that holds it.

## Before you say it is done

```
cd web/apps/api && npm run typecheck && npm test
cd console        && npm run typecheck && npx next build
go run ./tools/prosecheck .
```

`npm test` in the api needs Postgres; the catalog, matrix and namespace suites
run without it. Then look at the page: at 320 pixels and at desktop, in the
light theme, which is the only theme (`docs/astro.config.mjs` holds that
decision). Grep your own diff for `animate-pulse` and `animate-ping` before you
push. A hit is a bug, including on a live status indicator.
