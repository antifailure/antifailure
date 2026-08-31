# fixed

The installer decided `af` was not on your PATH, printed the export line that
would fix that, and then printed three bare `af` commands to run next anyway.
So the first three things a new user was told to do were `af doctor`, `af runner
install` and `af init`, and all three answered `command not found`. The export
it printed was session only besides, so a reader who pasted it lost `af` again
on closing the terminal, with nothing having said that would happen.

Every command the installer prints is now reachable. Where its bin directory is
not on PATH, the next steps are numbered after the step that puts it there, the
full path is offered for anyone who would rather not touch their PATH, and the
line that survives closing the terminal comes first and names the file the login
shell actually reads: `.zshrc` under `ZDOTDIR` for zsh, `.bash_profile` on macOS
and `.bashrc` on Linux for bash, `fish_add_path` for fish, and a shell it does
not recognise is told so rather than having a file guessed for it.

No shell profile is written without being asked, because a script piped into
`sh` has no stdin left to ask on. `AF_ADD_TO_PATH=1` is how to say yes in
advance, and it appends once however often the installer runs. In GitHub Actions
the installer writes to `GITHUB_PATH`, which the documented workflow needed and
did not have: every step gets a fresh PATH, so `af ci` in the step after the
install was never going to be found.
