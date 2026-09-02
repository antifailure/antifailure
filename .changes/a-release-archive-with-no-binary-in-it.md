# fixed

`tools/release/build.sh` packaged a release archive with no `af` in it, and
exited 0.

Both callers pass the output directories relatively. `just build-release` and
`.github/workflows/release.yml` each end in `dist stage`, resolved against the
repository root. The script builds the binary from `$root/engine`, in a
subshell, and handed the linker the same relative path, so `-o` resolved
against `engine/` rather than against the directory the script had just made.
The binary landed in `engine/stage/<name>/af`, the staged directory was
archived without it, and the archive, its checksum and the script's exit code
were all exactly what a working build produces. Four platforms of that is the
whole release: `LICENSE`, `README.md` and the runner's source, and nothing to
run.

`tools/relpack` exists to assert what is inside the archive rather than to
treat it as an opaque blob, and it could not see this, because it passed
absolute paths. That is the one shape neither caller uses, and an absolute path
makes the defect impossible. It builds from a directory inside the tree now,
named relatively, the way both callers do. A temporary directory outside the
tree does not reproduce it either: the relative path back out resolves to the
same place from `engine/` as from the root whenever the two sit at the same
depth below it, which on macOS they do, so the obvious version of this fix went
on passing over the broken script.

The script resolves both directories to absolute paths before anything uses
them. `install.sh` would have refused the result rather than installing half of
it, since it checks the archive for `af` before placing anything, so what a
reader would have met is an installer that cannot install rather than a broken
`af` on their PATH.
