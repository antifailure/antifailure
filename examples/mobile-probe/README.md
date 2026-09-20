# The mobile probe app

The smallest application that can prove a mobile driver end to end, built twice:
once in SwiftUI for iOS and once in Java for Android. Both halves carry the same
three controls with the same accessible names, so one workflow sentence drives
both surfaces and any difference in the verdict is a difference in the driver
rather than in the application.

It exists because "the driver compiles" is not evidence that the driver works.
The runner's mobile tests read accessibility trees, which proves the
normalization; this proves the rest of it against a real device: it boots, the
app installs and launches, the accessibility tree comes back, a control is found
by its accessible name, text is typed, a button is pressed, and the verdict is
what it should be.

The iOS half has been driven this way. The Android half builds and installs and
has NOT been driven end to end, which is why the `android` surface is not marked
available in `runner/src/drivers/driver.ts`. Driving it is what that flag is
waiting on.

## What it shows

Three controls and two state changes:

- `Email`, a text field.
- `Sign In`, which makes `Welcome back` appear.
- `Delete Account`, which makes `Something went wrong. Please try again.` appear.

The failure path is the half that is easy to leave out and the half that
matters. A driver that can only show a workflow passing has proved that it can
say yes. The product's job is to say no about an application that is broken, so
the app has to be able to break on purpose. `Something went wrong` is one of the
sentences `runner/src/workflow.ts` recognises as a screen showing a failure
instead of a result, which is what turns an unmatched expectation from "nothing
was proved" into "this did not work".

Together the three verdicts a run can honestly reach are all reachable here:

A workflow that signs in and expects `Welcome back` PASSES.

A workflow that presses `Delete Account` and expects `The account was deleted`
FAILS, and the report quotes the sentence the app showed instead.

A workflow that expects `Your order history`, which this app never shows,
comes back UNVERIFIED.

The third is not a weaker failure. Nothing on the screen argues either way, so
the run proved nothing, and saying so is the honest answer.

## Building

Neither half needs a project file, and that is deliberate: an `.xcodeproj` and a
Gradle wrapper are both generated, merge hostile, and unnecessary for something
this size. Each `build.sh` is the entire build and is readable top to bottom.

```sh
ios/build.sh          # needs Xcode. Writes ios/build/Probe.app
android/build.sh      # needs an Android SDK and a JDK. Writes android/build/probe.apk
```

`ios/build.sh` compiles with `swiftc`, writes an `Info.plist`, and signs ad hoc,
which needs no developer account and no provisioning profile.
`android/build.sh` links the manifest with `aapt2`, compiles with `javac`, dexes
with `d8`, aligns with `zipalign` and signs with a keystore it generates on the
spot, so no key is committed and none has to be found.

Both scripts find the toolchain rather than assuming a path, and name whatever
is missing.

## Running a workflow against it

The drivers need an Appium server, which is an external tool in the same way the
engine binary is. Install it once:

```sh
npm install -g appium
appium driver install xcuitest
appium driver install uiautomator2
appium server --port 4723
```

Then a job document naming the `ios` or `android` surface drives it. The
`mobile` block says which device and which application; leaving the device out
picks the booted simulator, or the attached Android device.

```json
{
  "surface": "ios",
  "artifacts": "/tmp/af",
  "mobile": { "id": "dev.antifailure.probe", "app": "examples/mobile-probe/ios/build/Probe.app" },
  "workflows": [
    { "name": "sign in", "description": "Sign in and see the greeting.", "expect": ["Welcome back"] }
  ]
}
```

The first iOS run on a machine builds WebDriverAgent with `xcodebuild`, which
takes minutes. Later runs reuse the build and take seconds. The driver's default
timeout allows for it, because a short one reads exactly like a broken driver.
