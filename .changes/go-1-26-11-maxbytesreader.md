# security

The Go toolchain moves from go1.26.10 to go1.26.11 in the engine, the
enterprise engine and the tools. GO-2026-6859, published on 2026-11-11, is a
race on the request headers in `net/http`'s `MaxBytesReader` that can crash an
HTTP/1 server, and it was reachable from the binary that ships. The required
`known vulnerabilities` check had been red on every pull request since.
