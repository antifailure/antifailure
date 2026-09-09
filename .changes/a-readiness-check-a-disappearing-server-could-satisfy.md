# fixed

The enterprise image proof asked the wrong server whether the database was
ready, and got a yes from one that was about to shut down.

`enterprise-image-proof.sh` waited with `docker exec pg_isready` and no host,
which asks over the Unix socket. The Postgres image's own entrypoint runs a
TEMPORARY server to apply initdb, started with `listen_addresses=''`, so it
answers the socket and nothing else. The wait loop therefore broke on the
bootstrap server, and the confirming probe landed in the gap while that server
was shutting down and the real one had not yet started.

On f065f64c that is exactly what happened: the loop broke and
`FAIL: the database never became ready` was printed 1.4 seconds later, with the
container's own log showing `received fast shutdown request` 9 milliseconds
before the probe. The window is about 150 milliseconds against a one second
poll, which is why this passed on the pull request and on the two mains before
it and failed once.

Both probes now ask over TCP, which the temporary server does not listen on, so
the first yes is the server the rest of the proof talks to. Measured rather than
reasoned: polling both every 400ms against this digest, the socket answered a
full interval before TCP did, `socket=UP tcp=down` at one sample and both up at
the next.

Worth recording that pinning the image in the previous change is likely what
made this fire. A digest reference is not served from a cached `postgres:17-alpine`,
so the pull ran fresh and moved where the poll landed. The race was already
there; the pin changed the timing that hid it.
