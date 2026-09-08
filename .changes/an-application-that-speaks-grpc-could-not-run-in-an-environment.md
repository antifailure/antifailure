# fixed

An application that speaks gRPC could not run inside an environment at all.

The sidecar terminated TLS on its inspected path with no ALPN offered and then
read HTTP/1.1 out of whatever was inside. gRPC is defined over HTTP/2, and
HTTP/2 over TLS is negotiated with ALPN, so a gRPC client closed the connection
in the handshake with "missing selected ALPN property" before it wrote a single
request. Nothing in the decision log explained it, because nothing had been
decided yet: the refusal came from the application's own client library. Five
of the six Google Cloud services an SDK reaches by default are gRPC, which is
how this surfaced, but the reach of it was every gRPC client anywhere.

Cleartext had the same hole by a different route. A client built with insecure
credentials, which is what every emulator's documented setup uses, opens with
the HTTP/2 connection preface, and the plain listener's HTTP/1.1 reader turned
that into a request whose method was PRI and which carried no Host header, then
refused it for naming no host.

Both connections are now read as the protocol they are, and every decision the
sidecar makes for an HTTP/1.1 request is made for an HTTP/2 one: the host is
taken from the authority the client wrote, the live credential tripwire runs
before the mode is acted on, a blocked host is still blocked, and the sandbox
substitution still happens. Bodies stream in both directions, because a gRPC
call is long lived and a bounded read on it would end the stream early and
leave the client reporting a status the server never sent.
