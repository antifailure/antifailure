# fixed

Engine verification runs the Docker database package separately from the other
engine packages. Its copy-on-write measurement was competing with unrelated
database creation and copying on the same daemon. The package inventory is
discovered from Go, each package runs exactly once, and all existing race,
timeout and copy-on-write thresholds are retained.
