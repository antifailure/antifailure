---
title: Desktop workflows
description: Driving a native macOS or Electron application through its accessibility tree, from the same manifest and the same run as the browser workflows.
sidebar:
  order: 27
---

A desktop workflow is one thing a person does in an application on their
machine, written exactly the way a [browser workflow](/docs/guides/workflows)
is: a goal, who does it, and what proves it happened.

```yaml
desktop:
  kind: electron
  application: ./node_modules/electron/dist/Electron.app/Contents/MacOS/Electron
  args: ["./desktop"]

workflows:
  - name: sign-in
    surface: desktop
    persona: ada
    description: >
      Sign in to the ledger with the account's address and password, accept
      the terms, and confirm you land on the signed in screen.
    expect:
      - "Welcome back"
```

Two blocks, because they answer two questions. `surface: desktop` on a
workflow says what it drives. `desktop` says what the application is, once,
because a manifest describes one product. A workflow that names the surface
without the block is refused while the manifest is read, before an environment
is built for a run that could never open anything.

They run inside `af test`, against the same environment the browser workflows
run against, and their results are counted in the same verdict. A desktop
workflow that fails is a failed check, exactly as a browser one is.

## The accessibility tree is what is driven

The application is read through its accessibility tree, the same thing a screen
reader reads: the roles, the names, the labels and the values a person would be
told about. Nothing in a workflow names a coordinate, a window position or a
control's internal id, so a workflow survives a layout being redesigned and
stops working only when the application stops saying what its controls are.

That is why a desktop workflow looks like a browser one rather than like a
macro. Underneath, the planner, the expectations, the retries and the verdict
are the browser's, with a different tree under them.

It also means an application that is hard for a screen reader to use is hard
for Antifailure to drive, and the symptom is honest: a control with no
accessible name is counted and reported as one nothing can reach.

## `kind`

`electron` covers anything built on Electron, which is most of the desktop
software a team would want rehearsed. Underneath one is Chromium, so it
publishes the same accessibility tree a web page does. `application` is the
Electron binary itself: inside a packaged application that is the executable in
`Contents/MacOS`, and in a project under development it is the one in
`node_modules`. `args` is what it is given, usually the directory holding the
project's `package.json`.

`macos` covers a native application, read through the platform's own
accessibility API. `application` is the `.app` bundle.

```yaml
desktop:
  kind: macos
  application: /Applications/Ledger.app
  process: Ledger
```

`process` is what macOS calls the running application when that is not the
bundle's own name: Visual Studio Code.app runs as Code. It defaults to the
bundle's name without `.app`, which is right for most applications, and `af
explain` prints the name that will actually be looked for. It exists because
opening a bundle returns before the application is ready, so the process still
has to be found afterwards. It belongs to a native application only, and an
Electron one carrying it is refused rather than quietly ignored.

The kind is stated rather than guessed from the path, because a wrong guess
means an application driven the wrong way reports as an application that does
not work.

A native application needs the macOS Accessibility permission, which a person
grants in System Settings and which nothing in software can grant for them. A
run without it is reported as blocked, with that step named, and never as an
application with no controls on it. A locked screen is the same answer for the
same reason: macOS withholds every accessibility tree while the screen is
locked, so the run says the screen was locked rather than guessing.

## Signing in is a workflow

There is no address bar to open and no cookie to set, so a desktop application
is not signed into before the workflow starts. Signing in is itself a workflow:
it types into the fields the application shows and presses what it says, the
way a person does.

The persona still names who is acting, so a report says which account a run was
about and a manifest reads the same on both surfaces.

## What to expect

`expect` is judged against the accessible text of the window: the headings,
labels and static text a screen reader would announce. A quoted sentence is
required on screen character for character.

It is not judged against what the agent typed. A field's own value is left out
of that text deliberately, because an expectation a workflow can satisfy by
filling a box with its own answer is a check that cannot say no. An expectation
naming an answer is still worth writing: with the value excluded it can only be
met when the application rendered those words, which is exactly what a
confirmation screen reading back an address is evidence of.

## Loading screens

An application that fetches its data after its window opens is not judged on
its loading screen. The runner cannot see that fetch, because it often runs in
Electron's main process, so it watches the accessibility tree instead. The
first screen is read again until it stops changing. Before any verdict that is
not a pass, the screen gets up to ten seconds to change. A screen that changes
goes back to the planner, and a screen that holds still is judged as it is.

A screen marked `aria-busy`, or `AXElementBusy` on macOS, never counts as still.
Marking a loading region busy is the most direct way to tell the runner, and a
screen reader, that it is not finished.

A screen that finishes loading without the expectation still fails, and the
verdict quotes the loaded screen. A screen that never stops changing is judged
on its last read, and the verdict says it was still changing. Phone workflows
are judged the same way.

## Budget

The browser's own, because a desktop workflow is planned rather than written
down: something decides what to press next, and a plan that never finishes has
to be stopped by a count as well as by a clock.

```yaml
budget:
  steps: 12
```

A workflow that runs out of steps is judged on the screen it reached, and
blocked if that screen shows nothing either way, because running out of steps
is not the application failing.

## One run drives one surface

The runner starts one driver and hands it the whole list, so the workflows in
one manifest name one surface between them. A manifest whose workflows
disagree is refused, naming both, rather than driving them all as whichever one
won. Terminal workflows are the exception and live in their own list, because
nothing is opened for them.

`af test --only sign-in` selects by name across every list, and names are
unique across all of them for that reason.

## What the report shows

Each step is a step, in the order the agent took it, so the run's own report
carries what was pressed and what was typed, and `af watch` prints them as they
happen.

There is no live video frame for this surface, and that is a decision rather
than an omission. Recording a window on macOS goes through ScreenCaptureKit,
whose stop path can lose the index a player needs and write a file that will
not open. Shipping a recorder that sometimes produces an unplayable artifact is
worse than shipping none, so the steps are the live cast here, exactly as they
are for a [terminal workflow](/docs/guides/terminal).

Related: [workflows](/docs/guides/workflows), [terminal
workflows](/docs/guides/terminal), [personas](/docs/guides/personas).
