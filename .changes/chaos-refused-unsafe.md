# fixed

`af chaos` told the reader that a durability proof meant nothing, on the same
screen that showed it. A disk fill the injector refused as unsafe, because the
directory shared the daemon's own filesystem, was reported as
`chaos.fault.refused` with the sentence "Nothing measured after it means
anything." The crash proof that ran after it, against an environment the
refusal never touched, was the best evidence in the run.

A refusal as unsafe is decided before the fault acts, so it now has its own
finding, `chaos.fault.unsafe`. It carries the refusal, says what the fault was
declared to establish was not established, and says it changed nothing the
other faults measured. It is still a warning, because a declared claim that
was not established is not nothing to see. A fault that tried to go in and
failed keeps `chaos.fault.refused` and its sentence unchanged.

The same branch of code hid a worse case. A fault that went in and whose undo
failed was reported as "was not applied", and the finding that says the
environment is still broken never fired. It fires now, with the reason the
undo gave. The MCP tool's summary and its `faults_refused` count had both
defects and are corrected with it.

A read only fault on a directory owned by root now reports AF-CHS-004, applied
and changed nothing, rather than AF-CHS-005, which means refused before acting.
It changed the mode and put it back, so it acted.
