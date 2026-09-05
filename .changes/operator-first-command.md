# fixed

The first operator required a hand-built container job and a database URL on
the command line. `just operator-init production` now opens secure interactive
setup in the exact running deployment, using its existing credential. An
existing root operator is never replaced.
