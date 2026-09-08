# Five questions for the Goliath Data call

Goliath Data publishes no compose file, no chart and no self hosting guide.
There is nothing to convert, so there is no rehearsal, and a twin built from a
guessed stack would be a fabrication that happens to compile. The manifest
beside this file is a SHAPE and not a twin: it is what everything readable
about the company from the outside describes, and every field in it is one of
the questions below waiting for an answer.

Each question turns one block of that manifest into a real one in an afternoon.

1. **What is the primary record store, and which major version?**
   `database.provider` and `database.version`. If it is managed Postgres, which
   service, because that decides whether branching is a native clone or a
   snapshot restore, and those differ by minutes per branch rather than by
   percentages.

2. **What holds the event and scoring data, and is it a second store or the
   same Postgres?**
   `datastores[]`. If it is a second store, its engine decides whether this
   build can provide it at all: today the engine provides `clickhouse` and
   refuses every other name by name.

3. **What is the queue between ingestion and inference?**
   Kafka, SQS, Redis, Postgres itself. There is no manifest key for a queue
   that is not a datastore, and no provider for one that is not ClickHouse, so
   this answer decides whether the middle of their pipeline can be rehearsed or
   has to be declared absent and said so in the fidelity report.

4. **Where does the county feed enter, and is it a poll, a push or a file
   drop?**
   A poll is an `egress` rule and a schedule. A push is an inbound webhook and
   needs `webhook_path` on the rule that names the sender. A file drop is
   neither and is the case with no manifest key at all.

5. **Which third party endpoints does the application call, by hostname, and
   which of them does it call at startup?**
   This is the one that decides whether a rehearsal is honest, and asking it
   properly needs the hostname rather than the vendor. A rule has to NAME the
   host. Under a rule written as `*.zapier.com` the sidecar answers a 403,
   because this build has no Zapier handler, while both instruments a person
   would check said the delivery was being captured: `af net explain` reported
   CAPTURE and the fidelity report reported a substitution with the provider's
   own success shape. F0 and F14 in the README beside this file are those two,
   and one of them is fixed.

   The startup half is a separate question with a separate cost. A call made
   before the first request turns a policy gap into a service that never becomes
   ready, and a service that publishes no port is reported ready as soon as its
   container is running, so it reads as healthy until it exits.

   Worth knowing before the call: even a NAMED Zapier host is answered with an
   empty object rather than Zapier's `{"status": "success", ...}`, because there
   is no Zapier handler in this build. If their delivery code checks `status`,
   that is a real gap and it is a small one to close.

Nobody has contacted Goliath Data about any of this. The call is Vir's.
