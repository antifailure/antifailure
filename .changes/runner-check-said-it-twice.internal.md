# fixed

`af runner check` printed the same remedy twice, one line apart, on the
commonest failure there is: a machine with no runner on it. The blocker carries
its own remedy, and the closing hint said the same sentence unconditionally.
Found by running the command against a home directory with nothing in it, which
is the state every new machine is in.
