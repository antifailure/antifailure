# fixed

The command the key rotation guide gave for its verification step could not
start the verification. It passed the check as a quoted `--command`, which the
Azure CLI reads as a list, so the container would have been asked to run one
program whose name contained spaces, under a container name that is not the
job's, with no image and none of the sealing keys. That step is the one an
operator runs before deleting the old key.

The guide now starts the check from the job's own definition, with its image,
its environment and both keys a rotation adds, and changes only the command. It
was run that way against staging, where the execution's template read the four
arguments, the container exited 0, and every row opened. A test runs the block
from the published page against a fake Azure CLI and refuses the old command, a
command without the check flag, and one without the job's environment.
