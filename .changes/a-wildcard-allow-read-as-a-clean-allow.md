# security

A rule of `host: "*.zapier.com"` in `allow` was accepted and `af net explain`
answered a Zapier catch hook with a clean ALLOW and the sentence an exact host
gets. The matcher was right, because that rule does let out every name under
`zapier.com` at any depth. Nothing said so, and a catch hook is somebody's live
automation posting to real contacts, which is the thing a rehearsal exists not
to reach.

Every place a rule is explained now says how far one that lets requests out
reaches when it names no host: `af net explain`, `af net policy`, the network
table `af init` prints, the MCP egress tool's rules and probes, and the
fidelity report. The JSON forms carry it as `caution`. A wildcard over a suffix
a platform hands its customers says that it reaches every customer's names and
not only yours, which is true of the catalogue's own `*.supabase.co`, and
`af init` no longer prints "Nothing reaches the internet by accident" under a
table holding one.

Two shapes of the same reach are refused. A star standing where the owner's
name goes, as in `*.com`, `*.co.uk` or `hooks.*.com`, is refused outside
`block`, because it reaches names registered by anybody, which is what a bare
`*` does and is refused for. And `egress.default: sandbox` is refused as
`default: allow` already was: a default names no credential, and a sandbox
request with nothing to substitute leaves exactly as the application wrote it,
so it reached every host on the internet.

`af net explain` and the MCP probe refuse a URL whose host is a pattern, which
`af net policy` used to suggest and which was answered ALLOW for a host no
request can carry.
