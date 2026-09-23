# added

`database.data_filesystem.size_bytes` gives the branch's data directory a filesystem of
its own, which is the layout a `disk_fill` fault can land on. Without it the
data directory sits on the container's writable layer, the fault is refused
before it acts because filling that would fill the machine, and the refusal
named a dedicated volume no manifest key provided. The fill now writes until
the declared headroom is left, Postgres meets a real `No space left on device`,
and the undo gives the space back.

# changed

`disk_fill` reads the mount at the data directory from the daemon and refuses
unless it is a volume this environment created with a size fixed at creation.
A mount of its own is no longer enough on its own: a plain named volume has its
own device number and is still a slice of the daemon's disk, so it would have
passed the old check and taken the machine down having satisfied the guard.
AF-CHS-005 now names the declaration that makes the fault possible.
