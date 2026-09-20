# added

The iOS surface now drives real applications.

It was declared and scaffolded: a job naming it was refused, so an iOS
application could not be rehearsed at all. It is now implemented against the iOS
Simulator through Appium's XCUITest driver, and proven against one: a workflow
that signs in passes, one that presses a deliberately broken control fails with
the application's own error quoted back, and one whose expectation the app never
shows comes back unverified rather than pretending either way.

The approach is the one every other surface here already uses. An iOS app's
accessibility tree is what a screen reader reads, which is what these workflows
are written against, so it is normalized into the same snapshot shape the
browser produces. The planner, the judgement and the verdict are then the
existing ones, unchanged: the same workflow sentence means the same thing on a
web page and in an app, and a mobile verdict is decided by the same function
that decides a web one.

An `android` surface is added alongside it, implemented against Appium's
UiAutomator2 driver and deliberately NOT marked available. No run has driven an
application with it: the emulator on the machine it was written on could not
finish booting, and its own system watchdog was restarting the device. Claiming
it would be a promise nobody has tested, and the iOS work is the argument
against doing that: driving a real device for the first time found three defects
that every unit test had passed over.

An Appium server is an external tool, the way the engine binary is. A run that
cannot find one BLOCKS every workflow with a reason instead of failing it,
because our own tooling being absent is not evidence about the application.

Three things measured against real devices rather than assumed, each of which
had already produced a wrong answer:

An empty iOS text field reports its own placeholder as its value, so a field
nobody had typed in read as already answered. The planner skipped it, submitted
an empty form, and the application's complaint about it would have been reported
as the application's fault.

The iOS software keyboard is part of the application's accessibility tree, so
opening one text field turned a two control screen into one offering twenty six
letter keys, `shift`, `Return`, `Emoji` and `Dictate` as things to press. The
keyboard's window is now left out, because it is system interface and no
workflow is about the letter q.

A screen recording stopped before its recorder had started produced no file, and
one stopped uncleanly produces a file of plausible size that no player will
open. Recordings are now held until they are genuinely capturing, stopped with a
signal the recorder handles, and checked for the index that makes them playable;
a recording that does not survive is reported as no video rather than as a path
to a broken one.

`examples/mobile-probe` is the application these were found against: the same
three controls in SwiftUI and in Java, including a failure path, so a driver can
be shown saying no as well as yes. The Java half is built and installable and
has not been driven, for the reason above.
