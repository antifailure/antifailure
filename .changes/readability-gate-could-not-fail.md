# fixed

The prose readability gate had never been able to fail. Both `just readability`
and the CI step passed the path before the flag, and Go's flag package stops
parsing at the first argument that is not a flag, so the threshold was never
read and the enforcement branch never ran. The command now refuses an argument
that appears after the path instead of measuring and passing, so the next
person gets an error rather than a green run.
