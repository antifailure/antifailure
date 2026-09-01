# fixed

A port that was free when the local runtime reserved it and taken by the time
the daemon bound it failed the whole of `af up` with a message about a bind. The
allocator's comment had described the retry that closes that race since it was
written, and only the database provider ever had one; the forwarder that
publishes a service now retries on a fresh port too, up to three times, and
still reports a permission refusal or an unreachable daemon on the first
attempt rather than retrying it.

`AF_PORT_RANGE_START` moves the range both allocators start from. `af doctor`
had named that variable in its remediation since it was written and nothing
anywhere read it, so a machine that had run out of ports had no way to move
them. `af doctor` also probes the range services are published on rather than
only the range databases use, and `AF-RUN-009` no longer suggests a manifest
field that does not exist and would fail validation.
