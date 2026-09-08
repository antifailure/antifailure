# fixed

A service asking for `resources.cpu` and `resources.memory` is now given them.

Both keys had been in the schema and in the reference since version one. The
Kubernetes runtime set no `ResourceRequirements` on any container and the local
runtime passed an empty `container.Resources`, so every environment this engine
ever placed asked for nothing. The release before this one closed the silence by
refusing both keys, which was the right answer for exactly as long as they did
nothing.

They do something now. On Kubernetes each value becomes the container's request
and its limit, which puts the pod in the Guaranteed quality of service class. On
the local runtime, where there is no scheduler to reserve anything, it is the
daemon's own cpu and memory constraint. A dimension the manifest did not name is
left out rather than set to zero, so an existing repository gets byte for byte
the Deployment and the container it got before.

Requests and limits are the same figure on purpose. The familiar shape, a small
request under a larger limit, is where a node gets oversubscribed: every
container is placed against its request and then grows into its limit, so a
machine that fits ten environments on paper runs eleven and the eleventh takes
memory from the others. The symptom is a workflow that reads as flaky, and a
twin whose failures belong to the machine rather than to the change under test is
worth less than no twin. One number also means environments per node is a
division rather than a guess, which is the thing nobody could do while every
environment asked for nothing: that was not a large number, it was an undefined
one.

Honouring the request creates a second problem, and **AF-RUN-047** is the answer
to it. A request larger than anything on the cluster is accepted by the API
server and then never scheduled: the pod sits `Pending` with an event nobody is
watching, and `af up` waits out its readiness timeout and reports a service that
did not start, which reads as a slow cluster. The sizes are now checked before
anything is created, against each schedulable node's allocatable minus what the
pods already on it requested, and a refusal names the shortfall. Two necessary
conditions and neither sufficient: every instance has to fit on some single node
and the total has to fit in what is free. Bin packing is still the scheduler's
job, and a cluster that will not let `af` list its nodes says so on the progress
channel rather than reporting a check it never made.

`af status` reports the size the runtime ACTUALLY applied, read back off the
running pod or off the daemon's record of the container. A runtime that accepts
a cap and emits none reports exactly what a correct one reports, which is why
the runtime conformance suite gained two behaviors rather than one: the size a
runtime says it applied, and the memory cap the service's own process is
subject to, read out of its cgroup from inside.

Refused rather than dropped: a quantity that will not parse, a CPU share finer
than a thousandth of a core, and a memory size with no unit, because Kubernetes
reads `memory: 512` as 512 bytes and nobody who writes it means that. There is
deliberately no ceiling in the manifest. A constant cannot know the machine, so
64Gi is absurd on a laptop and unremarkable on a cluster node, and "this cluster
does not have that" is a better answer than "the manifest may not say that".
