# added

Fault injection and the crash recovery proof were reachable from `af chaos` and
from nowhere else. `grep -rn RunChaos engine/internal/mcp` returned nothing at
all, for as long as both existed.

`run_chaos_faults` puts them on the MCP server. An agent asks for the faults the
manifest's `chaos` block declares to be injected into the running environment,
one at a time and each undone before the next begins, and reads back what the
system did about each one: the process that was killed and with what signal, the
container that was stopped, frozen or detached, the data directory that was made
read only, the filesystem that was filled. Around a fault aimed at the database
it reads back the durability proof as well: how many commits the client was told
were committed, how many are gone, how many rows are there that no client ever
wrote, where recovery started and where it reached, and what the index verifier
said about the heap.

Every other tool on that server rehearses a change against a system that works.
A migration runs, traffic is sent, a browser is driven, an invariant is checked,
and each of them measures a healthy environment doing what it does. None of them
could ask what the system does when it stops working, which is the whole
question for a change to a storage parameter, a checkpoint or fsync setting or a
replication option.

The call cannot choose what gets broken. Its arguments carry no fault kind, no
target, no process name, no signal and no duration, so the faults are the ones a
person wrote into the repository and committed. That is the rule the rest of the
server runs on, and it matters most on the one tool whose job is to break
something.

HELD and VERIFIED stay two answers, decided by the same classifier the command
line calls rather than by a second copy of it, so a run that fails at a terminal
cannot pass through an agent. A run that could not establish its claim is
`INCONCLUSIVE` and never a pass, and so is a project that declares no `chaos`
block and a declared block that injected nothing.
