# added

A manifest says which desktop application its workflows drive, so the desktop
surface can be run rather than only named.

```yaml
desktop:
  kind: electron
  application: ./node_modules/electron/dist/Electron.app/Contents/MacOS/Electron
  args: ["./desktop"]

workflows:
  - name: sign-in
    surface: desktop
    persona: ada
    description: Sign in to the ledger and confirm you land on the signed in screen.
    expect: ["Welcome back"]
```

`surface: desktop` selects the desktop driver, and that driver is handed an
application to open or it refuses the run. Nothing in the manifest could name
one. So the surface was reachable and every run that could ever have used it
was dead one field short of working: the schema accepted the manifest, the
validator passed it, the documents were built, an environment was created and
paid for, and the runner then refused the job for want of a field no person
could have supplied.

`kind` is `electron` or `macos`, and it is stated rather than guessed from the
path, because a wrong guess means an application driven the wrong way reports
as an application that does not work. `application` is the Electron binary or
the `.app` bundle, resolved against the directory holding the manifest before
the runner is told, because the runner is started from somewhere the manifest
never mentions. `process` is what macOS calls a native application when that is
not its bundle's name, derived from the bundle when it is left out, and refused
on an Electron application, which is launched directly and never looked up.

The pairing is refused in both directions. A workflow that drives the desktop
with no application declared cannot run at all. An application no workflow
drives is the quieter mistake and the one that looks like a working setting:
the run opens a browser instead, every workflow passes, and nothing anywhere
says the application somebody named was never launched.

The workflows themselves are ordinary `workflows` entries with a surface on
them, not a list of their own. A desktop application is driven through its
accessibility tree as a persona, which is what a browser workflow already says,
so the fields are the same fields and a second list would have been a second
spelling of one thing.
