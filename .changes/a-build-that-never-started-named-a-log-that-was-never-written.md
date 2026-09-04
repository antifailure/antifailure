# fixed

A build request that ended before Docker opened a log told you to read a log
that did not exist.

`af up` used AF-BLD-001 for every immediate endpoint failure, including a
permanent refusal, a lost Docker connection, a cancellation, and a temporary
capacity failure. AF-BLD-001 says to read the build log above, but Docker had
not opened the stream that could carry one.

Permanent endpoint refusals now use AF-BLD-006 and say to correct Docker's
redacted message. Cancellation, connection, capacity, and other immediate
failures use retryable AF-BLD-007 with guidance to run `af doctor` or try
`af up` again. Both say that no build log exists. A secret Antifailure has
loaded is removed before the detail reaches normal output, the wrapped cause,
JSON output, or an event.
