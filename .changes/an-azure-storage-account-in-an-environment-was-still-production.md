# added

An Azure storage account named in an environment was still the production one.

An application reaching Azure Blob, Queue or Table Storage from an environment
had two options and both were bad. Leave the host alone and the request went to
the real storage account, where an object written from a preview environment is
indistinguishable from production data afterwards. Point it at an emulator and
the application had to carry a `BlobEndpoint=` that only exists outside
production, so the code under test stopped being the code that ships.

Azurite now answers for `*.blob.core.windows.net`, `*.queue.core.windows.net`
and `*.table.core.windows.net` inside the environment, with nothing in the
application naming it. The account travels in the first label of the hostname,
so the sidecar preserving the Host header is what lets Azurite serve the
account the application already asks for.

Service Bus and Cosmos DB are named as outside that surface rather than left
for somebody to discover in a failure. The Service Bus emulator needs a SQL
Server container beside it, which an emulator declaration cannot yet express,
and it refuses to start until a person accepts a EULA, which is not an
acceptance a build makes on somebody's behalf.
