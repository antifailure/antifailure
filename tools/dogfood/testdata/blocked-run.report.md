<!-- antifailure:report -->
### Antifailure: 6 workflows could not be carried through. Nothing here counts against the change.

Environment `antifailure-default-d29eb0` is at http://127.0.0.1:46000

| Workflow | Result | Detail |
| --- | --- | --- |
| `sign-in-with-a-link` | blocked | locator.fill: Timeout 10000ms exceeded. Call log:   - waiting for getByLabel(/^(email\|email address\|e-mail\|username … |
| `view-the-environment-matrix` | blocked | locator.fill: Timeout 10000ms exceeded. Call log:   - waiting for getByLabel(/^(email\|email address\|e-mail\|username … |
| `open-a-run` | blocked | locator.fill: Timeout 10000ms exceeded. Call log:   - waiting for getByLabel(/^(email\|email address\|e-mail\|username … |
| `edit-a-network-policy` | blocked | locator.fill: Timeout 10000ms exceeded. Call log:   - waiting for getByLabel(/^(email\|email address\|e-mail\|username … |
| `read-the-audit-log` | blocked | locator.fill: Timeout 10000ms exceeded. Call log:   - waiting for getByLabel(/^(email\|email address\|e-mail\|username … |
| `a-viewer-cannot-edit-policy` | blocked | locator.fill: Timeout 10000ms exceeded. Call log:   - waiting for getByLabel(/^(email\|email address\|e-mail\|username … |

<details><summary>How to see <code>sign-in-with-a-link</code> yourself</summary>

Bring the environment up with af up, then follow these:
Expected: Send a sign-in link Sign out
Got: locator.fill: Timeout 10000ms exceeded.
Call log:
  - waiting for getByLabel(/^(email|email address|e-mail|username or email)$/i).first()


Trace: `/home/runner/work/antifailure/antifailure/.antifailure/artifacts/antifailure-default-d29eb0/sign-in-with-a-link-2.trace.zip`

</details>

<details><summary>How to see <code>view-the-environment-matrix</code> yourself</summary>

Bring the environment up with af up, then follow these:
Expected: Repository Branch State
Got: locator.fill: Timeout 10000ms exceeded.
Call log:
  - waiting for getByLabel(/^(email|email address|e-mail|username or email)$/i).first()


Trace: `/home/runner/work/antifailure/antifailure/.antifailure/artifacts/antifailure-default-d29eb0/view-the-environment-matrix-2.trace.zip`

</details>

<details><summary>How to see <code>open-a-run</code> yourself</summary>

Bring the environment up with af up, then follow these:
Expected: Recent runs Environment Verdicts
Got: locator.fill: Timeout 10000ms exceeded.
Call log:
  - waiting for getByLabel(/^(email|email address|e-mail|username or email)$/i).first()


Trace: `/home/runner/work/antifailure/antifailure/.antifailure/artifacts/antifailure-default-d29eb0/open-a-run-2.trace.zip`

</details>

<details><summary>How to see <code>edit-a-network-policy</code> yourself</summary>

Bring the environment up with af up, then follow these:
Expected: Effective policy Host Explain a request
Got: locator.fill: Timeout 10000ms exceeded.
Call log:
  - waiting for getByLabel(/^(email|email address|e-mail|username or email)$/i).first()


Trace: `/home/runner/work/antifailure/antifailure/.antifailure/artifacts/antifailure-default-d29eb0/edit-a-network-policy-2.trace.zip`

</details>

<details><summary>How to see <code>read-the-audit-log</code> yourself</summary>

Bring the environment up with af up, then follow these:
Expected: Action Actor Filter by action
Got: locator.fill: Timeout 10000ms exceeded.
Call log:
  - waiting for getByLabel(/^(email|email address|e-mail|username or email)$/i).first()


Trace: `/home/runner/work/antifailure/antifailure/.antifailure/artifacts/antifailure-default-d29eb0/read-the-audit-log-2.trace.zip`

</details>

<details><summary>How to see <code>a-viewer-cannot-edit-policy</code> yourself</summary>

Bring the environment up with af up, then follow these:
Expected: Effective policy Host
Got: locator.fill: Timeout 10000ms exceeded.
Call log:
  - waiting for getByLabel(/^(email|email address|e-mail|username or email)$/i).first()


Trace: `/home/runner/work/antifailure/antifailure/.antifailure/artifacts/antifailure-default-d29eb0/a-viewer-cannot-edit-policy-2.trace.zip`

</details>

Invariants: 4 invariants held.

Migrations: the migrations were not rehearsed: no migration tool was recognised in this repository

Migrations: query statistics need the pg_stat_statements extension, which is not available here: ERROR: relation "pg_stat_statement…

Masking verified: 127 columns read back, 6000 rows sampled, nothing that still parses as real.

Insights could not look: query statistics need the pg_stat_statements extension, which is not available here: ERROR: relation "pg_stat_statements" does not exist (SQLSTATE 42P01).

Torn down: 13 resources removed, nothing left behind.

Not measured: no baseline, so query counts are not compared. Save one on the base branch with 'af ci --save-baseline baseline.json' a…

Blocked means the environment or the runner could not carry a workflow through. It is not counted against this change. [What blocked means](https://antifailure.dev/docs/concepts/verdicts)

<sub>www/teams-sage-wells at `04b588e` in 4m3s, from golden `gv_20260831015712_ec97f941`. <a href="https://antifailure.dev/docs">Docs</a></sub>
