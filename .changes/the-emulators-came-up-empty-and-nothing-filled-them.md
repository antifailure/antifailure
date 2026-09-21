# changed

An environment's emulators came up empty and nothing put production's cloud
resources into them. The engine knew how to create a bucket, a queue, a table,
a topic, a stream, a secret and a parameter inside an emulator, and it knew
when to do it: before any service starts, because the service is who the
missing bucket is served to. What it did not have was anyone to tell it WHICH
resources production has. The list was filled by two end to end tests and by
nothing else, so an application that reads its own bucket on startup still met
an emulator that had none.

It is filled from the infrastructure as code now. The manifest's
`infrastructure` section says which directories declare production, those are
read without running anything, and every bucket, queue, topic, table, stream,
secret and parameter they declare is created in the twin before the first
service starts.

Three things are deliberately not created, and each is said out loud rather
than dropped. A resource whose name cannot be resolved without running
Terraform, because a bucket created under a guessed name is one the
application will never ask for. A resource the configuration declares and does
not deploy. And any value the reader withheld because it is a credential,
which never reaches a seeding request at all.

A resource whose presence depends on something only a plan decides IS created,
because a missing resource breaks an application that reads it where an unused
one costs nothing, and the run says which ones those were.
