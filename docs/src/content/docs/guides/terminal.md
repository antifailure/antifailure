---
title: Terminal workflows
description: Driving a command line program, including a full screen one, from the same manifest and the same run as the browser workflows.
sidebar:
  order: 26
---

A terminal workflow is one thing a person does at a command line, written the
same way a [browser workflow](/docs/guides/workflows) is: a goal, what they
type, and what the terminal must show afterwards.

```yaml
terminal_workflows:
  - name: deploy-plan
    description: >
      Run the deploy command in plan mode. It prints the changes it would make
      and asks before applying them. Answer yes and confirm it reports what it
      applied rather than an error.
    command: ./bin/deploy
    args: ["--plan"]
    input: ["y", "<enter>"]
    expect:
      - '"Applied 3 changes"'
```

They run inside `af test`, against the same environment the browser workflows
run against, and their results are counted in the same verdict. A terminal
workflow that fails is a failed check, exactly as a browser one is.

## The screen is what decides how the program is driven

Two kinds of program live at a command line and they need opposite things.

A program that reads a line and prints lines is driven through a pipe. What it
printed is the evidence, all of it, from the first line to the last. Leave
`screen` out and that is what you get.

A program that takes over the screen is different in every way that matters. It
will not start without a terminal. It reads raw keystrokes rather than lines.
And what it "printed" is a stream of cursor moves, erases and scroll regions
whose only meaning is the grid of cells they leave behind: a menu row that was
drawn, erased, and redrawn one line up appears three times in that stream and
once on the screen, and the row a person would name is in neither. Declare a
`screen` and the program is given a real pseudo terminal of that size, and the
expectations are judged against what it drew.

```yaml
terminal_workflows:
  - name: inbox
    description: >
      Open the inbox. Move down to the published posts with the arrow keys and
      press Enter. The detail for that row should appear at the bottom.
    command: ./bin/inbox
    screen:
      rows: 24
      cols: 80
    input: ["<down>", "<down>", "<enter>", "q"]
    expect:
      - '"Eleven posts are live."'
```

That is the whole choice, and it is not a preference. A pseudo terminal echoes
what is typed into it, so a program that has not turned echo off shows the
driver's own keystrokes on its screen. An expectation naming something the
workflow types would then be satisfied by the workflow rather than by the
program, which is why the manifest is refused with AF-MAN-002 rather than
merely warned about: a check that its own input can pass is worse than no
check, because it looks like one. `af doctor` revalidates.

## Keys

Without a screen, each `input` entry is a line written to standard input.

With a screen, each entry is keystrokes. Text is typed as written, and a name
in angle brackets becomes the bytes a keyboard sends for that key:

`<enter>` `<tab>` `<esc>` `<space>` `<backspace>` `<delete>` `<insert>` `<up>`
`<down>` `<left>` `<right>` `<home>` `<end>` `<pageup>` `<pagedown>`
`<backtab>`, `<f1>` through `<f12>`, and `<ctrl-a>` through `<ctrl-z>`.

Anything else between angle brackets is typed literally, so a workflow that
types `<html>` into a field gets `<html>` and there is no escape syntax to
learn.

Text and keys mix inside one entry, so `"<esc>:wq<enter>"` is one step.

Arrow keys have two encodings, and which one is correct is decided by the
program rather than by you: a program that has asked for application cursor
keys, which most full screen programs do while they own the screen, ignores the
other encoding in complete silence. Antifailure reads the mode the program set
and sends the encoding it asked for, so an arrow in a workflow is the arrow the
program is waiting for.

On Windows the program's terminal is ConPTY, which keeps that request to itself.
There Antifailure sends an arrow as a key press and release, the way a Windows
terminal does, and the console chooses the bytes the program receives. It
usually chooses the encoding the program asked for, but not always: measured on
a heavily loaded Windows machine, it sent the normal encoding to a program that
had asked for the other, every time. So on Windows Antifailure promises that the
arrow arrives, not which encoding it arrives in. A program that accepts arrows
in both encodings, as most libraries do, is unaffected.

After every entry, Antifailure waits for the program to redraw and then reads
the screen. Expectations are judged against every screen the program showed,
not only the last one, so a workflow can name something that was on screen in
the middle of it.

## Expectations

The rules are the browser ones, with one piece of advice that matters more
here. A quoted sentence is required on the screen character for character:

```yaml
expect:
  - '"Eleven posts are live."'
```

Prefer that form for a terminal. An unquoted expectation is judged by how many
of its meaningful words appear, and a screen is eighty columns of dense text
whose words repeat, so the sense of a sentence is matched far more easily there
than on a page.

At least one expectation is required, which is stricter than a browser
workflow. A terminal workflow with nothing to expect can only ever report that
nothing confirmed or contradicted it, and that is blocked, so a workflow
without one could never pass.

## What must never show

An expectation is met the moment its words are on screen, and a full screen
program that goes quiet with them there is accepted without being waited on to
exit. That is right for almost every workflow and wrong for one kind: a program
that prints the right thing and then contradicts it.

```yaml
expect:
  - '"Applied 3 changes"'
never:
  - "rollback started"
  - "warning: rows dropped"
```

`never` names what the program must not show at any point. One appearing fails
the workflow even when every expectation was met. Each entry is matched as a
string, ignoring case and runs of whitespace, with the quotes optional; never by
its sense, because a sense match leans towards finding things and here a false
find fails a correct program.

Declaring `never` changes how long the program is watched. A met expectation is
no longer the end, since the contradiction comes after it, so the program is
watched until it exits or its budget is spent. A full screen program that never
exits is therefore watched for its whole budget, and the pass says how long it
was watched; set `budget.duration` to the window you mean. A forbidden string
ends the watch as soon as it appears, because nothing printed afterwards could
take it back.

It is judged against every byte the program wrote as well as every screen it
drew, so a warning drawn and erased between two snapshots is still caught, and
so is one the screen had not finished drawing when the budget ran out. Each
screen is judged on its own, so the end of one screen and the start of the next
never read as one phrase.

If the budget runs out with keys still to send, the workflow is blocked rather
than passed, even with every expectation met: those keys are exactly where a
forbidden string could have come from.

On Windows a workflow with `never` that saw nothing forbidden is blocked rather
than passed. ConPTY hands Antifailure the screen as it was drawn, not every
byte the program wrote, so a line the program printed and then overwrote in
place never arrives: measured on a Windows machine, a warning overwritten on
its own line was missed in every one of fifteen runs. A forbidden string that
does arrive still fails the workflow, so `never` there can catch a
contradiction but cannot promise there was none. Run the workflow on Linux or
macOS, or under WSL, for that promise.

Two entries are refused before anything runs, because each decides the verdict
by itself. One that a quoted expectation contains, since meeting the
expectation shows it. And on a screen, one the workflow types, since a terminal
echoes typed text and the workflow would show it itself. Through a pipe nothing
echoes, so forbidding what was typed is allowed and is how you say a program
must not print a secret it was given back out.

## Where the program runs, and what it can reach

`cwd` is where the program runs, relative to the directory holding the
manifest, and it defaults to that directory.

Every terminal workflow is started with `AF_BASE_URL` set to the address of the
environment this run is rehearsing. A command line tool under test reads it and
talks to the rehearsal environment rather than to whatever the shell it
inherited happens to point at.

## Budget

```yaml
budget:
  duration: 45s
```

Thirty seconds by default. Past it the program is stopped and the workflow is
reported as blocked with the budget named, never judged on a half drawn screen.

There is no step budget and no cost ceiling, because neither exists here: the
keys are written down rather than decided by an agent, and no model is asked
anything.

A full screen program is not expected to exit, and not exiting is not a spent
budget. The workflow is over once its keys have been sent and the screen has
settled; Antifailure judges what it sees and then stops the program. The budget
is only spent when the clock runs out with keys still to send, or, for a
workflow that declares [`never`](#what-must-never-show), as the window it is
watched for.

The budget also bounds the reading, not only the running. A program that exits
having written more than its budget can draw is reported as blocked, with the
bytes it wrote and how many of them were never drawn, rather than judged on the
part that was.

## What the report shows

Each rendered screen is a step, so the run's own report carries the screens the
program drew in the order it drew them, and `af watch` prints them as they
happen. A screen identical to the one before it is recorded once.

The pull request comment shows something narrower for a workflow that failed:
the invocation, the size of the terminal it was given, and one line per key
that was pressed, so that a reader can run the same thing at their own
terminal. The screens are not in it, because that comment is markdown and
markdown collapses the runs of spaces that hold a screen's columns together.

## Running one

`af test --only deploy-plan` selects by name, and names are shared between
`workflows` and `terminal_workflows` for exactly that reason. Two workflows
answering to one name is refused.

## What this does not do

Antifailure drives the program you name. It does not give it a shell, so
`args` are passed as written and nothing in them is expanded, and a pipeline or
a redirection belongs in a script you name as the `command`.

Android is declared in the surface abstraction and is not built. A manifest may
still name it, in a `workflows` entry's
[`surface`](/docs/guides/workflows): it is refused by name, against the
surfaces this build does carry, rather than returning a green verdict that
tested nothing. [Desktop](/docs/guides/desktop) and iOS are built and
driveable, so naming either one runs it; a desktop workflow also needs a
`desktop` block saying which application it is driven in.
