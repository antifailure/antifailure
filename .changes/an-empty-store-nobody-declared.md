# added

A store declared `empty`, `derived` or `topics_only` had a manifest key and no
behaviour. `af up` printed one line saying that this build brings up the golden
stance only, and then the environment either started a container because a
service happened to carry the same name or started nothing at all, and nothing
afterwards could tell those two apart. The fidelity report called every one of
them `unmeasured`, which holds a component out of the score in both directions,
so a twin of a product whose events live in Kafka scored exactly the same
whether its broker held the declared topics or was an empty container nobody
had touched.

The stance is the feature. Not everything should be cloned, and a plan that
copies a cache and calls it fidelity has misunderstood what fidelity is. So the
other three stances are now outcomes.

**`empty` is the store's own service and nothing else, said out loud.** That is
already the state the stance asks for. What was missing was never a container,
it was somebody being told that this store is empty because a person decided it
should be, and being told the declared reason. The run says it and the report
says it again. What is refused is a store declared `empty` that nothing in the
manifest starts, which is a manifest asking the environment to hold a store
while nothing brings one up.

**`derived` runs its declared `rebuild` command to completion inside the
environment, in the image and with the variables of the service it names,**
once every service is up. A non-zero exit fails the environment rather than
leaving an index nobody built. A search index cloned from production is stale
against the branch the moment the branch is masked, because the documents in it
name people who do not exist in the twin's Postgres. An index built from the
branch cannot be stale against it.

**`topics_only` creates the declared topics and consumer groups, with no
messages.** Three things a consumer needs, none of which exists in an empty
broker. The topic, because subscribing to a name that is not there reads
nothing and reports nothing, and the run goes green having tested a poll loop
against a name that will only ever exist in production. The partition count,
because ordering is per partition and a group with more members than partitions
leaves members idle, so a twin whose topic has one partition where production
has twelve cannot reproduce a reordering bug at all. The consumer group,
because a consumer joining a group nobody created reads from the END by
default, so the twin's own producers write, the consumer joins, and every one
of those messages is skipped.

The commands are the broker's own, run in the broker's own image, and they wait
for it to answer a metadata request first, because a broker declares no health
path in anybody's compose file and there is nothing for the readiness wait to
have waited on. A broker this build has no recipe for is REFUSED by name rather
than started with nothing in it, which would be the `empty` stance wearing a
different word. This build shapes Kafka.

Both runtimes run the jobs, after every service and before the environment is
called up, because a rebuild reads one store and writes another and a topic
creation needs a broker that has finished starting.

**The report shows all four as distinct positions.** `empty`, `derived` and
`topics_only` are `substituted` rather than `unmeasured`: not `reproduced`,
because none of the three is production's data and the argument for them is
that it should not be, and not held out of the number, because that is what
made an honest position and an accident score the same. The score goes down for
declaring a cache empty. It should, and the declared reason is carried through
as written beside it. A store whose service is not running is `absent` whatever
the manifest says, and a store that is running for which this environment's own
run recorded no such job stays `unmeasured` and is told to run `af up` again:
an environment brought up by an older build runs the same services from the
same file with a broker that has nothing in it, and the run journal is the only
thing that records what a particular run actually did.
