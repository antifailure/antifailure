# security

The egress sidecar enforced the whole policy on two of its three request paths.
A plain HTTP request through the explicit proxy port, which is what every client
that reads `http_proxy` sends, skipped the live credential tripwire, forwarded
the application's own credential in `sandbox` mode, and refused `capture`,
`mock` and `synth` instead of serving them. All three paths now run the same
decision. The sidecar also refuses to open a loopback, link local, private or
carrier grade address on the environment's behalf unless a rule names it, which
closes the instance metadata endpoint under `default: allow`; it answers
non-address DNS queries for external names itself rather than forwarding them
out; it takes a transparent connection's port from the listener rather than from
the client's `Host` header; and `egress.allow_ipv6`, which nothing in the
sidecar read, is now enforced.
