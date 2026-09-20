# added

The runner can drive a desktop application.

It has named four surfaces for as long as it has had a surface abstraction, and
it could drive two. A job that asked for the desktop was refused, correctly,
because a scaffolded driver returning an empty result would have reported a
green run that tested nothing. It was still a no.

Desktop now drives native macOS applications through AXUIElement, the platform's
own accessibility API, and Electron applications, which is where VS Code, Slack
and Discord live, through Chromium's accessibility tree. Both reduce to the same
snapshot a browser produces, so the planner, the expectation check, the verdict
and the live stream are the ones a browser run already uses, and a desktop
result counts and reads exactly as a web result does. A workflow written as a
sentence, press Continue, expect Welcome, carries across unchanged.

Native accessibility is a permission a person grants in System Settings, and a
locked screen withholds every application's tree whether or not it was granted.
Both are reported as themselves, with the step to take, never as an application
with no controls on it.
