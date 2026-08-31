# added

A test in `internal/events` parses the engine for emit call sites and compares
them to the event catalog. Of fifty-two documented types the engine emitted
nineteen, and nothing said so: every unemitted constant is referenced by the
map that documents it, by a display that switches on it, or by a sink that
translates it, so a linter sees them all as used. A type with no emitter is now
allowed only with a written reason.
