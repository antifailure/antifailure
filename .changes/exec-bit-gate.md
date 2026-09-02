# fixed

A shell script that a workflow or a recipe runs by path is now refused unless
git records it as executable. `tools/site/check-tls.sh` was committed at mode
100644 and both of the places that run it name it as a bare relative path, the
certificate step in the deploy workflow and the `check-tls` recipe, so the
kernel refused to exec it and the step died with "Permission denied" and status
126. It had never run anywhere: the only job that calls it fires on a push to
main and not on a pull request, so the first push that reached the deploy job
was also the first time anybody learned that the certificate check had never
checked a certificate.

The new gate reads the mode from the git index rather than from the disk, which
is what makes it a gate rather than a check on whoever ran it. A `chmod +x`
without a `git update-index --chmod=+x` leaves a working tree that runs the
script and a commit that does not, so a check of the filesystem answers yes on
the machine where the mistake was made and CI, which checks the index out into
a fresh tree, answers no.

Two rules, because either alone leaves a hole. Every tracked file named `*.sh`
that opens with a shebang carries the bit, which catches a script committed
wrong before anything runs it. And every path that a `run:` block or a justfile
recipe puts in command position, and that names a tracked file, carries the
bit, which catches a program whose name does not end in `.sh`. Both refuse to
report green over an empty result, because a pattern that has stopped matching
is silent and silence here reads exactly like a clean repository.
