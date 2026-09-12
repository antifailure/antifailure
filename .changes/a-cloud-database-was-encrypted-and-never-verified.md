# fixed

A managed cloud database was reached with `sslmode=require`, which encrypts the
connection and checks nothing about who answers it. Anything on the path could
present a certificate and read a copy of production. The three cloud providers
now hand out `verify-full` or `verify-ca` connection strings with a pinned
public trust bundle: AWS's published RDS roots, Microsoft's published Azure
roots, and the Cloud SQL instance's own CA fetched through the authenticated
Admin API. `require` is refused, and plaintext is limited to a loopback test
fixture.

The same bundle now reaches the application. The engine installs it inside
every service and migration container as its own file, separate from the egress
proxy's HTTP inspection authority, because a proxy that can sign any hostname
must not be able to vouch for a database.

A cloud branch also sat outside the environment's contained network, so a
service either could not reach it or reached it around the proxy. The sidecar
now relays each branch through a fixed listener bound to one resolved and
validated address. PostgreSQL begins TLS with an SSLRequest that carries no
hostname, so the relay copies bytes rather than inspecting them, and nothing a
client sends can choose another destination. Metadata, loopback and platform
service addresses are refused before any listener opens, and on Kubernetes only
the sidecar receives a network policy that reaches the database.

A running container or Deployment was reused whenever its image matched, so a
second `af up` kept the previous database URL and trust file. Both runtimes now
compare a fingerprint of the whole configuration.

A branch that failed after its provider created a resource lost the reference,
so teardown could not find what it had to remove. The reference is now
journalled before the error returns.

The Aurora provider had the same gaps the other two were fixed for. A clone now
disables every inherited login and ends its sessions before masking and again
before publication, keeps the source's subnet group and security groups, turns
IAM database authentication off, and scopes every resource to the source
cluster's ARN.

None of this has run against a real cloud. The TLS checks use a real PostgreSQL
SSLRequest and handshake against certificate authorities the tests generate, and
the relay is proved end to end in Docker against a real PostgreSQL TLS server.
No connection has met a certificate issued by AWS, Google or Microsoft.
