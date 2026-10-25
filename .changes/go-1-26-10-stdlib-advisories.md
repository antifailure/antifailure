# security

The Go toolchain moves from go1.26.8 to go1.26.10, and `golang.org/x/net` from
v0.59.0 to v0.60.0, in the engine, the enterprise engine and the tools.

Ten advisories published after main last moved were reachable from the binary
that ships. Nine of them, published on 2026-10-08, are HTTP/2 and net/http
faults in the standard library, eight of which are also present in the copy of
the HTTP/2 code that `x/net` carries. The tenth, GO-2026-6809, published on
2026-10-21, distrusts compromised intermediate certificates in `crypto/x509` and
is fixed only in go1.26.10.
