import SwiftUI

/// The iOS half of the mobile probe app.
///
/// Three controls and one state change, deliberately the smallest application
/// that can prove a driver end to end: a field to type into, a control to
/// press, and a sentence that appears only after the press. The Android half
/// under ../android carries the same three accessible names, so one workflow
/// sentence drives both surfaces and any difference in the verdict is a
/// difference in the driver rather than in the application.
///
/// Every control carries an explicit accessibility label, because the label is
/// what a screen reader reads and therefore what the accessibility snapshot is
/// built from. A control with no label is invisible to this whole approach,
/// which is the point the `unnamed` count in a snapshot exists to make.
@main
struct ProbeApp: App {
  var body: some Scene { WindowGroup { ContentView() } }
}

struct ContentView: View {
  @State private var email = ""
  @State private var signedIn = false
  @State private var failed = false

  var body: some View {
    VStack(spacing: 24) {
      TextField("Email", text: $email)
        .textFieldStyle(.roundedBorder)
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled(true)
        .accessibilityLabel("Email")

      Button("Sign In") { signedIn = true }
        .accessibilityLabel("Sign In")

      // The failure path, and it is here on purpose. A driver that can only
      // show a workflow passing has proved half of what matters: the whole
      // job is to say no about an application that is broken. Pressing this
      // makes the app behave the way a broken one does, so a workflow that
      // expects it to succeed comes back FAILED rather than merely unproven.
      //
      // The wording matters. "Something went wrong" is one of the sentences
      // runner/src/workflow.ts recognises as a page showing a failure instead
      // of a result, which is what turns an unmatched expectation from
      // "nothing was proved" into "this did not work".
      Button("Delete Account") { failed = true }
        .accessibilityLabel("Delete Account")

      // Present only once signed in, so an expectation on this sentence is
      // met after the press and unmet before it.
      if signedIn {
        Text("Welcome back")
          .accessibilityLabel("Welcome back")
      }

      if failed {
        Text("Something went wrong. Please try again.")
          .accessibilityLabel("Something went wrong. Please try again.")
      }
    }
    .padding(32)
  }
}
