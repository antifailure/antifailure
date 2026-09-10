# security

An environment could carry HTTP and nothing else, and every other protocol
failed identically whether its host was allowed or blocked.

The sidecar terminates TLS and speaks HTTP. An application that talks AMQP to
Azure Service Bus, Kafka to Confluent Cloud, or the MongoDB wire protocol to
Atlas resolved the name, got the sidecar's address back from the environment's
resolver, and connected to a port nothing was listening on. On Docker the
kernel answered with a reset. On Kubernetes the NetworkPolicy permitted egress
to 3128, 80, 443 and 53 and nothing else, so the packet was dropped and the
client waited out its own connect timeout. Either way an allowed host and a
blocked host produced the same failure, which means the containment was real
and the product did not work.

A connection on a port that does not carry HTTP is now decided on the server
name in its TLS handshake, by the same policy engine every other path uses.
What the sidecar cannot do on that path is read inside, and that limit is
refused rather than absorbed: capture, mock, synth and sandbox all need a
request, so a rule using one of them on such a port is refused at validation
when the rule names the port and at the connection otherwise. Sandbox is the
one that would have been worst to accept, because forwarding without replacing
the credential sends the application's own key to the real provider.

The set of ports the sidecar opens is the manifest's, not the protocol table's.
The first version of this seeded the listeners from the table, so every
environment answered on all sixteen ports whether a manifest had asked for one
or not. That is not an idle listener. A connection accepted on this path is
decided on the name in its TLS handshake, and a rule that spells no port
matches every port, so a manifest that allowed one host for HTTP silently
carried that host's Redis and its mail as well, forwarded. The listeners are
now opened only for ports a rule names, which is the bargain the rest of the
manifest already makes.

A listener is shared by destinations, so selecting its ports was not sufficient.
A rule granting `broker.example.com:5671` also exposed that port to a portless
allow rule for `website.example.com`. The matching rule now has to name the
actual destination port. A real recording broker reproduced the unintended
connection and now receives none.

Path and method rules also cannot govern bytes the sidecar never reads. The
stream path refuses hosts whose applicable rules require inspection, rather
than matching them against an invented CONNECT request at `/`.

On Kubernetes the NetworkPolicy is the union of the table and the ports the
rules name, which is wider than the listeners on purpose. Permitting a port
there grants nothing, because the packet still arrives at the sidecar and the
sidecar still decides; a port permitted with nothing listening is refused in a
millisecond, while a port the policy omits is DROPPED and the application hangs
until its own connect timeout. Without the rules in that union, declaring a
broker on an unusual port would work on Docker and hang on a cluster.
