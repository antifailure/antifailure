# MCP management verification

This change is intended to land with the hosted MCP schema and transport. It
does not duplicate that migration. Local API verification applied the migration
as a fixture to a private UTF-8 Postgres database on a separate native server.

## Database assertions

Seven new tests pass. The existing developer platform suite also passes, for
25 tests total and no skips. Each mutation below failed its intended assertion,
then passed after the production line was restored. Mutations were applied one
at a time.

| Test | Independent production mutations caught |
| --- | --- |
| Counts only MCP credentials | Include an engine credential; exclude the exact expiry boundary |
| Configured endpoint | Return the wrong endpoint path |
| Credential metadata | Wrong client, wrong organization, missing scopes, missing last authentication, leaked hash field |
| Distinct standing | Report an expired credential as active |
| Revocation | Disable the database update; give the wrong replacement instruction |
| Searchable directory | Remove MCP from the accepted filter kinds |
| Bounded list | Return a fifty-first credential; suppress the truncation flag |
| Existing surface contract | Claim that no MCP records exist |

There are fifteen mutation cells. The existing credential tests additionally
prove the shared revocation route records the audit entry and stops an engine
credential authenticating. The hosted MCP authenticator is verified separately
in the transport change.

## Browser behavior

The actual built console was served by the actual API with a private database,
not a mock response page. The operator signed in through the normal form.

- Populated and empty states were inspected at desktop and phone widths.
- Final screenshots were inspected at 1440 pixels and 320 pixels, with both
  light and dark preferences. This product deliberately retains its light
  palette for either preference.
- The 320 and 390 pixel viewports had no page overflow.
- Revocation required the prefix and a reason. Submitting it closed the dialog,
  removed the action, reduced active credentials from one to zero, and increased
  revoked credentials from one to two.
- A separate cross-component probe minted a real hosted credential through the
  OAuth implementation and exercised the mounted MCP handler. Tools listing
  returned HTTP 200. After revoking that same credential in this page's browser
  dialog, replaying the same bearer returned HTTP 401. The raw credential stayed
  in process memory rather than appearing in a command argument or report.
- A network failure showed the shared error and a working retry action.
- A failed refresh after successful revocation retained the prior data with an
  explicit stale-answer warning. Retry recovered the updated credential state.
- A read-only operator had no action column.
- The page accessibility audit reported zero violations. It reported existing
  incomplete checks for shared time labels and duplicate navigation IDs; the
  navigation fix belongs to the separate operator accessibility change.

Console unit tests: 140 passed. Console build: 46 routes. API typecheck passed.
No production deployment or GitHub CI result is claimed by this local report.
