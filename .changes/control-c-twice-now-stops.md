# fixed

Pressing control C twice did nothing.

Both binaries set up signal handling by calling `WithSignals`, whose comment
said in the present tense that the second interrupt forces an exit with the
journal intact. Both then discarded the return value that said a second one had
arrived: `ctx, _, stop := cli.WithSignals(...)`. The function computed the
answer, closed the channel, and no line in either binary ever asked. A user
holding control C on a pull the size of a database got exactly as far as the
first interrupt did, which for a command that does not check its context is
nowhere.

The shape was the reason. `WithSignals` returned a function reporting whether a
second signal had been seen, and a poll can only be answered by code that is
still running. A second interrupt matters in precisely the case where the
command is not coming back to ask anything. It is a channel now, and `cli.Run`
waits on it: the exit code is produced out from under a command that is still
running, and stopping there is safe because every resource is journaled before
it is created, so `af down` still knows what to remove.

`afcli.Run` takes the same channel, so the enterprise binary, which carried an
identical comment and an identical discarded value, stops too.
