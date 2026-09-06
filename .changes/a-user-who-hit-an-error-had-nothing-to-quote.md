# fixed

A person who hit an error in the console had nothing to quote, and a
cross-site refusal left no trace on either side.

The control plane mints an id per request, echoes it on x-request-id and writes
it on the log line and into the body of a 500 on its raw routes. The console
does not use the raw routes. It talks to /trpc for almost everything, and the
tRPC error formatter and the onError log line both dropped the id, so the
error card showed a fixed sentence pointing at the logs and nothing to search
the logs by. Every tRPC error now carries the id under `error.data.requestId`,
the same id as the header, the onError line carries it beside the code and the
path, and the console shows it under the message as "Reference: <id>", the same
line the render boundary shows for a page that threw. The fixed sentence for an
internal failure is unchanged; the id is the only thing added.

The six cross-site refusals in the control plane answered 403 with a sentence
naming a header, no log line and no id, in a file where every other refusal
logs. The two cross-site defects found on launch night were found by somebody
pasting screenshots of that sentence. They now go through one helper that
writes a structured line, which gate, why, the request id and no token, and
answers the same fixed shape the other refusals answer, with the request id
in it, under a new code, AF-CP-004.
