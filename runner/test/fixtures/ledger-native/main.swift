// AfLedger: the NATIVE macOS application the desktop surface is proven against.
//
// The Electron fixture beside this one proves the Chromium half of
// `desktop.kind`. This proves the other half, and until it existed `macos` was
// an enum member whose path no test had ever reached: a value a manifest could
// name, a branch the engine took, and a driver nobody had watched drive
// anything. That is the same dead shippable gap as a function with no callers,
// one enum member wide.
//
// THE ACCESSIBLE NAMES HERE ARE AN INTERFACE, not labels. The driver finds
// every control through the accessibility tree by the name a screen reader
// would announce: "Email address", "Password", "I accept the terms", "Sign in".
// Renaming one is a change to the test in the same commit.
//
// TWO THINGS ARE LOAD BEARING AND LOOK LIKE DETAIL.
//
// The acknowledgment is REQUIRED through `setAccessibilityRequired(true)`.
// AppKit publishes no such thing on its own, and the planner ticks a checkbox
// only when it is required, because an agent that ticks every optional box is
// subscribing somebody to a newsletter to see what happens. Without that line
// the box is never ticked, the form refuses the sign in, and the failure reads
// as a driver that cannot press things.
//
// The confirmation is STATIC TEXT rather than a field's value. An expectation
// is judged against what the application rendered, and a field's own value is
// deliberately left out of that text, so a confirmation written into a text
// box would be a check that cannot say no.
//
// It is also meant to be looked at, because a person watches this being
// driven. System font, one accent, no gradient, nothing animating on a loop.

import AppKit

// MARK: the window

final class Ledger: NSObject, NSApplicationDelegate {
    private let window = NSWindow(
        contentRect: NSRect(x: 0, y: 0, width: 520, height: 430),
        styleMask: [.titled, .closable, .miniaturizable],
        backing: .buffered,
        defer: false)

    private let email = NSTextField()
    private let password = NSSecureTextField()
    private let terms = NSButton()
    private let notice = NSTextField(labelWithString: "")
    private let signedOut = NSStackView()
    private let signedIn = NSStackView()

    func applicationDidFinishLaunching(_: Notification) {
        window.title = "Ledger"
        window.center()
        window.contentView = build()
        // Shown without stealing the keyboard, because a person is using this
        // machine. The driver activates the application itself when it needs
        // the window in front, and it does that once rather than in a loop.
        window.makeKeyAndOrderFront(nil)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_: NSApplication) -> Bool { true }

    private func build() -> NSView {
        let heading = NSTextField(labelWithString: "Sign in to Ledger")
        heading.font = .systemFont(ofSize: 22, weight: .semibold)
        let lead = NSTextField(labelWithString: "Your books for Bellweather Press, up to date as of this morning.")
        lead.font = .systemFont(ofSize: 13)
        lead.textColor = .secondaryLabelColor

        notice.font = .systemFont(ofSize: 12)
        notice.textColor = .systemRed
        notice.isHidden = true

        // NO PLACEHOLDER, and the absence is load bearing rather than a
        // design choice. With one set, this field's `filled` read true in two
        // of four snapshots of a freshly launched window and false in the
        // other two, while the reader's own tree carried no value either
        // time. A planner skips a field it believes is already answered, so
        // that coin decides whether the sign in can happen at all. A fixture
        // whose verdict depends on which way it lands is not a fixture.
        email.setAccessibilityLabel("Email address")
        password.setAccessibilityLabel("Password")
        for field in [email, password] as [NSTextField] {
            field.font = .systemFont(ofSize: 13)
            field.translatesAutoresizingMaskIntoConstraints = false
            field.widthAnchor.constraint(equalToConstant: 420).isActive = true
        }

        terms.setButtonType(.switch)
        terms.title = "I accept the terms"
        // See the note at the top: without this the planner leaves the box
        // alone and the sign in can never succeed.
        terms.setAccessibilityRequired(true)

        let submit = NSButton(title: "Sign in", target: self, action: #selector(signIn))
        submit.bezelStyle = .rounded
        // The platform's own default button, which is what the snapshot reads
        // as the thing that submits. A word list would be a second opinion.
        submit.keyEquivalent = "\r"

        // A real disabled control with its reason stated beside it, rather
        // than an enabled one at half opacity.
        let guest = NSButton(title: "Continue as guest", target: nil, action: nil)
        guest.bezelStyle = .rounded
        guest.isEnabled = false

        let emailLabel = NSTextField(labelWithString: "Email address")
        let passwordLabel = NSTextField(labelWithString: "Password")
        for label in [emailLabel, passwordLabel] {
            label.font = .systemFont(ofSize: 12, weight: .medium)
            label.textColor = .secondaryLabelColor
        }

        signedOut.orientation = .vertical
        signedOut.alignment = .leading
        signedOut.spacing = 10
        for view in [heading, lead, notice, emailLabel, email, passwordLabel,
                     password, terms, submit, guest] {
            signedOut.addArrangedSubview(view)
        }

        let welcome = NSTextField(labelWithString: "Welcome back, Ada")
        welcome.font = .systemFont(ofSize: 22, weight: .semibold)
        let cleared = NSTextField(labelWithString:
            "Six entries have cleared since you were last here, and none of them need you.")
        cleared.textColor = .secondaryLabelColor
        let balance = NSTextField(labelWithString: "Balance, Bellweather Press  42.00")
        balance.font = .monospacedDigitSystemFont(ofSize: 15, weight: .regular)
        signedIn.orientation = .vertical
        signedIn.alignment = .leading
        signedIn.spacing = 10
        for view in [welcome, cleared, balance] { signedIn.addArrangedSubview(view) }
        signedIn.isHidden = true

        let root = NSStackView(views: [signedOut, signedIn])
        root.orientation = .vertical
        root.alignment = .leading
        root.edgeInsets = NSEdgeInsets(top: 28, left: 32, bottom: 28, right: 32)
        return root
    }

    // The application's own behaviour, and it is deliberately real: it refuses
    // what it should refuse and says which field. A fixture that accepted
    // anything would prove nothing when the driver filled it in.
    @objc private func signIn() {
        let missing = email.stringValue.trimmingCharacters(in: .whitespaces).isEmpty
            || password.stringValue.isEmpty
        if missing {
            return refuse("Fill in the fields above and try again.")
        }
        if terms.state != .on {
            return refuse("Accept the terms before signing in.")
        }
        notice.isHidden = true
        signedOut.isHidden = true
        signedIn.isHidden = false
    }

    private func refuse(_ message: String) {
        notice.stringValue = message
        notice.isHidden = false
    }
}

let app = NSApplication.shared
let delegate = Ledger()
app.delegate = delegate
// .regular so the window is a real window with a real accessibility tree,
// which an accessory application does not reliably publish.
app.setActivationPolicy(.regular)
app.run()
