# fixed

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
