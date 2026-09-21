# added

A workflow says which surface it drives, and the manifest names all five.

```yaml
workflows:
  - name: subscribe
    surface: web
```

`web`, `terminal`, `desktop`, `ios` and `android`. All five may be written,
including the ones a build carries no driver for, and that is the point: a
build registers the drivers it has, so a manifest naming a surface this build
cannot drive is refused BY NAME, against the surfaces that build actually
carries. A schema that simply refused the value would say only that something
is unknown, which reads like a typo and sends somebody looking for a spelling
mistake instead of a release.

Without this a driver could be finished, correct and unreachable, because
nothing in the manifest could ask for it. That is the same defect the terminal
driver had for its whole life, one surface over, and the answer this time is a
key that exists before the driver does.

The refusal happens at both layers on purpose. The engine says it while reading
the manifest, so the answer arrives before an environment is built, and it
names what this build can drive rather than what the schema allows, because
those are different lists and only one of them helps. The runner says it again
before it drives anything, so a surface nothing drove can never come back
green, and it says it as a blocked RESULT rather than by throwing, so one
workflow naming a surface nobody built does not take the nine beside it down
with it.

`surface: terminal` on a `workflows` entry is answered with where it belongs
rather than with a list it is missing from: a terminal workflow needs a program
to run where a browser workflow needs a persona to sign in as, so it is written
in `terminal_workflows`.
