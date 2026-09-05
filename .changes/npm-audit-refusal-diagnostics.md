# fixed

The npm advisory gate now preserves the reason an audit endpoint refused a
request. It reports npm's root message, its structured error fields, the process
error, and stderr. When npm leaves its structured fields empty, the gate says so
instead of printing an empty sentence after `npm audit refused`.

Audits run with four workers, a five minute deadline per project and an eleven
minute deadline for the scan. Each completed or inconclusive project is named
with elapsed time before the Security job can exhaust its fifteen minute
budget. Unfinished projects fail the gate and never count as audited.

There is no second whole-audit retry. npm already
retries registry fetches, and one unreachable request can consume its five
minute fetch timeout. The gate also fails if its report cannot be written.
