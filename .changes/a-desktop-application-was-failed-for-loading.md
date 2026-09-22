# fixed

A desktop or phone workflow could fail against an application that was still
loading, and quote the loading screen as what the application showed.

An Electron ledger client with thousands of `transfer.posted` rows was failed
in 3.7 seconds with `"transfer.posted" was not found`, quoting "Journal Reading
the ledger". That was the client's loading screen. It fetches its journal in
Electron's main process and draws a skeleton until the answer lands. The desktop
driver read the accessibility tree as soon as the document parsed, the planner
found nothing to press, and that first look became the final verdict. The phone
loop had the same shape, and an iOS run had passed only because the app finished
loading before the first read.

The browser surface waits for the network to go quiet. Nothing like that exists
here: a main process fetch is invisible to the page, and a tree read through
Appium has no network signal at all. So the tree itself is watched. The first
read is read again until it holds still. Before any verdict that is not a pass,
the screen gets up to ten seconds to change. A changed screen goes back to the
planner and a still one is judged as it is. `aria-busy` and macOS's
`AXElementBusy` keep a screen from counting as still.

This never turns a failure into a pass. A screen that finishes loading without
the expectation still fails, and the verdict quotes the loaded screen. A screen
that never stops changing is judged on its last read, and the verdict says it was
still changing. A passing workflow costs one extra read of its first screen.
