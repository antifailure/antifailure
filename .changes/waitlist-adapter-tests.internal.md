# added

`api/waitlist/index.js` had no tests. Splitting the decisions into
`api/shared/waitlist.js` made them testable and left the adapter behind, and the
adapter is the file Static Web Apps actually invokes. Six tests now cover the
four things only it can do: a deployment with no connection string answers 503
rather than telling somebody their good address was wrong, a surprise from
anywhere inside becomes our JSON rather than the host's error body on a public
path, the caller is the first entry of `x-forwarded-for` rather than the last,
and the table client is built once per cold start. No network and no clock: the
connection string is well formed and points nowhere, and `join` is replaced
through the module cache.

`normaliseEmail` also gains the boundary its length test was missing. The
existing case reaches the limit from far above it, so `>=` in place of `>` would
have passed every test in the file while turning away the longest address RFC
5321 allows.

Every one of the seven was proved able to fail. The first version of the
missing-connection-string test could not: returning null instead of throwing let
the request sail past the guard, fail deeper in, and be caught by the outer
handler, which answers with the same 503 and the same single log line. The
response cannot tell those two apart. It asserts now that the request never
reaches `join` and that the log says which of the two happened.

One local hazard worth knowing, because it is not this change and it will look
like it is. `npm run build` in `www` rewrites `www/tsconfig.json` as a side
effect, including `jsx` from `preserve` to `react-jsx` and an added
`.next/dev/types` include. Anybody who builds the site locally will find that
file modified and think they have uncommitted work. They do not; discard it.
