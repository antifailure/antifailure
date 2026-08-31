# fixed

The installer decided `af` was not on your PATH, printed the export line that
would fix that, and then printed three bare `af` commands to run next anyway.
So the first three things a new user was told to do were `af doctor`, `af runner
install` and `af init`, and all three answered `command not found`. The export
it printed was session only besides, so a reader who pasted it lost `af` again
on closing the terminal, with nothing having said that would happen.

The installer now puts `af` on the PATH rather than explaining how to. It
appends one line to the startup file the login shell actually reads, prints that
line and names the file, so the change is visible and deleting the line undoes
it. `AF_NO_MODIFY_PATH=1` declines it in advance, which is the only way to ask
when a script piped into `sh` has no stdin left to prompt on. Running the
installer again does not add the line twice.

The terminal that ran the installer cannot see a file written a second ago, so
it ends with one line to paste that puts `af` on that shell's PATH and runs the
first of the next steps. Every branch ends in commands that work: bare where
PATH was set up, and the full path where it was declined, could not be written,
or the login shell is one the installer cannot name a startup file for.

zsh gets `.zshrc` under `ZDOTDIR`, bash gets `.bash_profile` on macOS and
`.bashrc` on Linux, fish gets `fish_add_path`, and an unrecognised shell is told
so rather than having a file guessed for it.

**`af runner install`, the second command the installer prints, could not
succeed on any machine installed this way.** It searches for a runner source
beside the binary, at `$PREFIX/bin/runner` and
`$PREFIX/share/antifailure/runner`. The installer put it at `$PREFIX/runner`,
which is where `af` looks for an *installed* runner, so the command answered
AF-AGT-004 and advised running itself. The same placement made `af runner check`
report `ok runner` on a tree with no `node_modules`, so the real breakage
surfaced later inside `af test`. The source now lands where the engine was
already looking, and a half installed tree left by an earlier installer is
cleaned up. The installer also names node and the version it needs when node is
missing, rather than leaving that for `af runner install` to discover.

`af runner check` reported `ok runner` on that tree, because it stat'd
`src/main.ts` and stopped there. It now reads the runner's own `package.json`
and reports the source, every declared dependency against `node_modules`, the
node version against the `engines.node` range, and the browser, each with a
remedy that fits it rather than one "run af runner install" printed under every
failure including a missing node. It still does not claim the runner executes,
because knowing that means starting node and launching a browser, and anything
it cannot determine reports as not checked rather than as ok.

In GitHub Actions the installer writes to `GITHUB_PATH` and touches no profile.
The documented workflow needed that and did not have it: every step gets a fresh
PATH, so `af ci` in the step after the install was never going to be found.

The gate protecting all of this could go green without running, and it took a
deliberate break to find out.

`just test-tools` is `go test ./...` with no `-count=1`. Go's test cache keys on
the test binary, its arguments, the environment it reads, and the files it opens
**under its own module**. `tools/installsh` executes `install.sh` through `sh`,
so nothing in the package ever opens it, and `install.sh` lives at the repository
root, outside `tools/`, so it would be invisible to the cache even if something
did. Adding an `os.ReadFile` of the script to the test does not fix it, which was
the first thing tried: the read is outside the module and the cache ignores it.

The observed failure: `install.sh` was edited so it never wrote a shell profile
at all, and `just test-tools` reported `ok (cached)` for `tools/installsh`. A
gate that goes green on a broken subject is worse than no gate, because it
actively certifies the thing it cannot see.

`-count=1` in the recipe and in the CI step that has to match it. **Any test that
shells out to a file outside its own module needs `-count=1`, or it will cache
against a subject it never watched change.** There is no way to express that
dependency to the cache from inside the test.

Shell coverage, so nobody reads the test names and concludes more than was
proven: zsh, bash, ksh and an empty `SHELL` are proven by running the installer
and then starting a genuinely new shell of that kind. **fish is the one shell not
proven by execution**, because fish is not installed on the machine this was
built on. Its branch is proven by running the installer under `SHELL=fish` and
asserting that `~/.config/fish/config.fish` is written with the `fish_add_path`
line, which is a weaker claim than the others on this list.
