# fixed

`af test --watch` showed no frames on Windows.

The engine opened the live view on a unix socket file and handed its path to
the runner, whose Node net.connect maps a path on Windows to a named pipe and
refuses anything else with EACCES. The runner reads an unreachable watcher as
nobody watching, so a Windows user got an empty live view and no reason. On
Windows the engine now listens on a named pipe with a random name, created
first by the engine so nobody can claim the name ahead of it, refusing remote
clients, and carrying a security descriptor that admits only the user running
the engine. That last part matters because a pipe has no directory to hide in,
and the default pipe security admits Everyone to read. Everywhere else the
socket and its private directory are unchanged, and a live view that cannot
listen now says so at once and names the endpoint.
