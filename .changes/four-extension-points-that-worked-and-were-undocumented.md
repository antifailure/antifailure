# added

Four of the five extension points worked and only one was documented.

The engine declares five sockets a build outside the repository can implement:
database providers, datastore providers, runtimes, golden stores and
emulators. All five are reachable, validated, and refused by name when a
manifest asks for something that is not there. The Providers section described
databases and nothing else, so the other four were the quietest kind of
missing feature: they work, and nobody outside can find out that they do.

There are now five pages, one per point, with what ships for each, the
mechanism underneath, the capabilities the conformance suite tests against,
and the editions rule that says which of them are MIT and why. The check that
holds it there reads the socket constants and the schema's provider names out
of the code, so a sixth extension point, or a new provider name, reds the
build until somebody writes it down.

Two new things ship beside it.

`gcs` is a golden store, MIT and in the engine beside `s3` and `azure_blob`
because those are its peers and anything with an MIT peer in the engine stays
MIT. A fleet on Google Cloud previously had two choices: give a runner an AWS
or Azure credential it had no other use for, or keep every golden on the one
machine that made it and stop being a fleet. It authenticates through the
metadata server where there is one and through a service account key where
there is not.

`cloud_database` and `cloud_runtime` are two licensed features covering the
managed cloud providers, gated per call rather than at registration, so a
licence that lapses while the process is running stops enforcement without a
restart. The gate refuses anything that creates a cloud resource and never
refuses a teardown, an inventory or a status: a lapsed licence that stopped
somebody removing a database cluster would leave them paying for it.
