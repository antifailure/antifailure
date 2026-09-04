# fixed

The npm advisory gate now preserves the reason an audit endpoint refused a
request. It reports npm's root message, its structured error fields, the process
error, and stderr. When npm leaves its structured fields empty, the gate says so
instead of printing an empty sentence after `npm audit refused`.

This is a clearer failure rather than a second whole-audit retry. npm already
retries registry fetches, and one unreachable request can consume its five
minute fetch timeout. The gate also fails if its report cannot be written.
