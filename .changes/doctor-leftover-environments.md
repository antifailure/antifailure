# added

`af doctor` now counts the environments this machine is still holding and names
`af env prune` when any of them are older than a day. The command existed and
the only thing that named it was `af env list`, which nobody is pointed at
either, so in practice the way to learn that leftovers accumulate was to read
the whole command reference. An environment somebody is working in is not
reported as a problem; only ones past the cutoff `af env prune` itself uses.
