// The macOS accessibility reader.
//
// It walks a running application's AXUIElement tree and prints it as the JSON
// node runner/src/drivers/ax.ts reduces to a Snapshot, and it performs one
// action on one element. runner/src/drivers/macax.ts compiles it on first use
// and caches the binary, so a clone of this repository needs nothing but the
// Swift compiler that ships with the Xcode command line tools.
//
// WHY THIS IS COMPILED SWIFT AND NOT A SCRIPT. The first version of this file
// was JavaScript for Automation, which reaches the same C API through its
// Objective C bridge and needs no compiler at all. It worked, and it could not
// be made to CHECK ITSELF, which is the thing that matters here.
// AXUIElementCopyAttributeValue hands back an untyped CFTypeRef. Swift asks
// CFGetTypeID whether that really is an element, or an array, before using it,
// and answers honestly when it is neither. The automation host cannot: passing
// the raw reference to CFGetTypeID is refused outright with "Ref has
// incompatible type", so ObjC.castRefToObject is the only route and it can
// only be trusted, never verified. Those type checks are what turn the
// degraded answers macOS gives below into a sentence rather than into a tree
// of applications inside applications. Compiling also removes the automation
// host's own startup, which was the whole two seconds a snapshot cost.
//
// AND THE SCREEN HAS TO BE UNLOCKED. Measured, after a long session of this
// driver reading applications correctly went quiet all at once: with
// CGSSessionScreenIsLocked set, macOS withholds every application's
// accessibility tree. The window server still has the windows, so
// CGWindowListCopyWindowInfo lists them on screen at layer zero, and AXWindows
// answers with a degraded list whose first member reads back as the
// APPLICATION rather than as a window. Every symptom of that state is
// indistinguishable from an application that renders nothing: Finder, a fresh
// Electron app and System Events all reported zero windows within the same
// minute. So the lock is checked FIRST and reported as itself, because "I
// could not look" and "I looked and there was nothing" are different answers
// and only one of them is about the application.
//
// The protocol is one JSON request on argv and one JSON line on stdout:
//
//   {"op":"trust"}     -> {"ok":true,"trusted":<bool>}
//   {"op":"apps"}      -> {"ok":true,"apps":[{name,pid,bundleId,active}]}
//   {"op":"activate","pid":N}
//   {"op":"snapshot","pid":N,"maxNodes":1500,"maxDepth":40}
//                      -> {"ok":true,"app":...,"window":...,"tree":<node>,"truncated":<bool>}
//   {"op":"act","pid":N,"ref":"0.2.1","action":"press"|"focus"|"setValue","value":"..."}
//                      -> {"ok":true}
//
// A failure answers {"ok":false,"error":"<sentence>"} and exits zero, because
// the caller has to tell "the application said no" apart from "the helper
// could not run", and a crash makes those the same event.

import Cocoa
import ApplicationServices

// MARK: reading an element

func attribute(_ element: AXUIElement, _ name: String) -> CFTypeRef? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, name as CFString, &value) == .success else {
        return nil
    }
    return value
}

func stringAttribute(_ element: AXUIElement, _ name: String) -> String {
    guard let value = attribute(element, name) else { return "" }
    return value as? String ?? ""
}

func boolAttribute(_ element: AXUIElement, _ name: String) -> Bool? {
    guard let value = attribute(element, name) else { return nil }
    return value as? Bool
}

func elementAttribute(_ element: AXUIElement, _ name: String) -> AXUIElement? {
    guard let value = attribute(element, name) else { return nil }
    guard CFGetTypeID(value) == AXUIElementGetTypeID() else { return nil }
    return (value as! AXUIElement)
}

func elementsAttribute(_ element: AXUIElement, _ name: String) -> [AXUIElement] {
    guard let value = attribute(element, name) else { return [] }
    guard CFGetTypeID(value) == CFArrayGetTypeID() else { return [] }
    return (value as? [AXUIElement]) ?? []
}

/// valueString renders AXValue as something a person would read back, whatever
/// the platform stored there: a text field holds a string, a slider a number,
/// a checkbox a 0 or a 1. The tick state is also reported separately as
/// `checked`, so no planner ever has to parse "1".
func valueString(_ element: AXUIElement) -> String {
    guard let value = attribute(element, "AXValue") else { return "" }
    if let text = value as? String { return text }
    if let number = value as? NSNumber { return number.stringValue }
    return ""
}

/// accessibleName is what a screen reader announces.
///
/// Four attributes in the order macOS itself prefers them. A button carries
/// AXTitle; an icon button carries only AXDescription; a text field is
/// labelled by a separate AXTitleUIElement when it is labelled properly and
/// carries AXPlaceholderValue when it is not. Reading AXTitle alone, which is
/// the obvious first version of this function, reports every correctly
/// labelled text field on a well built form as having no name at all.
func accessibleName(_ element: AXUIElement) -> String {
    let title = stringAttribute(element, "AXTitle")
    if !title.isEmpty { return title }
    let description = stringAttribute(element, "AXDescription")
    if !description.isEmpty { return description }
    if let label = elementAttribute(element, "AXTitleUIElement") {
        let viaLabel = stringAttribute(label, "AXValue")
        if !viaLabel.isEmpty { return viaLabel }
        let viaTitle = stringAttribute(label, "AXTitle")
        if !viaTitle.isEmpty { return viaTitle }
    }
    let placeholder = stringAttribute(element, "AXPlaceholderValue")
    if !placeholder.isEmpty { return placeholder }
    return ""
}

let chosenRoles: Set<String> = ["AXCheckBox", "AXRadioButton", "AXSwitch", "AXToggle"]

func checkedState(_ element: AXUIElement, role: String) -> Bool? {
    guard chosenRoles.contains(role) else { return nil }
    guard let value = attribute(element, "AXValue") else { return nil }
    if let flag = value as? Bool { return flag }
    if let number = value as? NSNumber { return number.intValue == 1 }
    return nil
}

// MARK: walking

final class Budget {
    var left: Int
    var truncated = false
    init(_ left: Int) { self.left = left }
}

/// node renders one element and everything under it.
///
/// Capped on both axes and the cap is REPORTED rather than silently applied. A
/// single Electron window can carry tens of thousands of elements; a walk that
/// ran to the end of one would take minutes and hand the planner a tree it
/// cannot read. A truncated tree that says it is truncated is something a
/// caller can reason about. One that does not say so is a caller quietly
/// deciding a control is absent because the walk stopped before it.
func node(
    _ element: AXUIElement, path: String, depth: Int, budget: Budget, defaultButton: AXUIElement?
) -> [String: Any]? {
    if budget.left <= 0 { budget.truncated = true; return nil }
    budget.left -= 1

    let role = stringAttribute(element, "AXRole")
    var out: [String: Any] = ["role": role, "name": accessibleName(element), "ref": path]

    let value = valueString(element)
    if !value.isEmpty { out["value"] = value }
    if boolAttribute(element, "AXEnabled") == false { out["enabled"] = false }
    if boolAttribute(element, "AXFocused") == true { out["focused"] = true }
    if boolAttribute(element, "AXRequired") == true { out["required"] = true }
    // AXElementBusy is macOS's "still loading", the native twin of aria-busy.
    // The runner refuses to judge a busy screen as finished (settle.ts).
    if boolAttribute(element, "AXElementBusy") == true { out["busy"] = true }
    if let checked = checkedState(element, role: role) { out["checked"] = checked }
    if let button = defaultButton, CFEqual(button, element) { out["isDefault"] = true }

    if depth <= 0 { budget.truncated = true; return out }
    var children: [[String: Any]] = []
    for (index, child) in elementsAttribute(element, "AXChildren").enumerated() {
        let childPath = path.isEmpty ? String(index) : "\(path).\(index)"
        guard let rendered = node(
            child, path: childPath, depth: depth - 1, budget: budget, defaultButton: defaultButton
        ) else { break }
        children.append(rendered)
    }
    if !children.isEmpty { out["children"] = children }
    return out
}

/// elementAt walks an index path back down to the element it names.
func elementAt(_ window: AXUIElement, _ ref: String) -> AXUIElement? {
    if ref.isEmpty { return window }
    var current = window
    for part in ref.split(separator: ".") {
        let children = elementsAttribute(current, "AXChildren")
        guard let index = Int(part), index >= 0, index < children.count else { return nil }
        current = children[index]
    }
    return current
}

// MARK: applications and windows

func runningApp(_ pid: pid_t) -> NSRunningApplication? {
    NSRunningApplication(processIdentifier: pid)
}

/// enableChromiumAccessibility turns on the accessibility tree of a Chromium
/// application, which is off until an assistive client asks for it.
///
/// This is the single line that makes Slack, VS Code, Discord and every other
/// Electron application readable through AXUIElement. Chromium does not build
/// a native accessibility tree unless a client sets AXManualAccessibility on
/// its application element, because the tree is expensive and almost nobody
/// wants it. Without it the application answers with a window list that
/// contains nothing a screen reader could read, and a driver reports the
/// richest desktop applications on the machine as empty screens.
///
/// Set unconditionally and its result ignored: an application that is not
/// Chromium refuses the attribute, which is a no-op and not an error.
func enableChromiumAccessibility(_ app: AXUIElement) {
    AXUIElementSetAttributeValue(app, "AXManualAccessibility" as CFString, kCFBooleanTrue)
}

/// windowOf picks the window a workflow is looking at: the focused one when
/// the application reports one, and the first real window otherwise.
///
/// Every candidate's role is CHECKED. In the seconds after a launch, an
/// application can answer AXFocusedWindow and AXWindows with references that
/// read back as the application itself, whose children are its menu bar and
/// its other windows. Walked without this check that is a tree of applications
/// inside applications, ended only by the node budget, and it is reported as a
/// screen with no controls on it. Checked, the application is simply not ready
/// yet, which is the truth, and the caller waits.
func windowOf(_ app: AXUIElement) -> AXUIElement? {
    if let focused = elementAttribute(app, "AXFocusedWindow"),
       stringAttribute(focused, "AXRole") == "AXWindow" {
        return focused
    }
    for window in elementsAttribute(app, "AXWindows")
    where stringAttribute(window, "AXRole") == "AXWindow" {
        return window
    }
    return nil
}

// MARK: operations

/// screenIsLocked reports whether this GUI session is locked.
///
/// AXIsProcessTrusted stays TRUE across a lock, which is why this has to be
/// asked separately: the permission is still granted and the answers are still
/// empty. A process with no GUI session at all, an ssh shell say, gets no
/// session dictionary, and that is not a lock, it is nowhere to look.
func screenIsLocked() -> Bool {
    guard let session = CGSessionCopyCurrentDictionary() as? [String: Any] else { return false }
    if let locked = session["CGSSessionScreenIsLocked"] as? Bool { return locked }
    if let locked = session["CGSSessionScreenIsLocked"] as? Int { return locked == 1 }
    return false
}

let LOCKED_SCREEN_ERROR =
    "The screen is locked. macOS withholds every application's accessibility tree while it is, " +
    "so this is not an application with nothing on it, it is a screen nothing is allowed to " +
    "read. The windows are still there: the window server still lists them. Unlock the Mac, or " +
    "run this on a host whose session stays unlocked."

func opTrust() -> [String: Any] {
    ["ok": true, "trusted": AXIsProcessTrusted(), "locked": screenIsLocked()]
}

func opApps() -> [String: Any] {
    var apps: [[String: Any]] = []
    for app in NSWorkspace.shared.runningApplications where app.activationPolicy == .regular {
        apps.append([
            "name": app.localizedName ?? "",
            "pid": Int(app.processIdentifier),
            "bundleId": app.bundleIdentifier ?? "",
            "active": app.isActive,
        ])
    }
    return ["ok": true, "apps": apps]
}

/// opActivate brings an application to the front.
///
/// Needed, not cosmetic. Several applications draw no window at all until they
/// are activated, so a driver that launches one and waits politely in the
/// background waits forever: this is exactly what TextEdit does, and it is why
/// a cold launch reported no window for thirty seconds while the application
/// was running perfectly well. Real keyboard and mouse input also goes to
/// whatever is frontmost, so an application being driven has to BE frontmost.
func opActivate(_ pid: pid_t) -> [String: Any] {
    guard let app = runningApp(pid) else {
        return ["ok": false, "error": "No running application has pid \(pid)."]
    }
    app.activate()
    return ["ok": true]
}

func opSnapshot(_ request: [String: Any]) -> [String: Any] {
    guard let pid = request["pid"] as? Int else { return ["ok": false, "error": "No pid given."] }
    let app = AXUIElementCreateApplication(pid_t(pid))
    enableChromiumAccessibility(app)
    guard let window = windowOf(app) else {
        // The lock is asked about only once the read has come back empty, so
        // the ordinary case costs nothing, and the empty answer is never
        // reported as the application's when something else explains it.
        if screenIsLocked() {
            return ["ok": false, "locked": true, "error": LOCKED_SCREEN_ERROR]
        }
        return [
            "ok": false,
            "noWindow": true,
            "error": "The application has no window open yet. A screen that is not there is not " +
                "an empty screen.",
        ]
    }
    let budget = Budget(request["maxNodes"] as? Int ?? 1500)
    let tree = node(
        window, path: "", depth: request["maxDepth"] as? Int ?? 40, budget: budget,
        defaultButton: elementAttribute(window, "AXDefaultButton")
    )
    return [
        "ok": true,
        "app": stringAttribute(app, "AXTitle"),
        "window": stringAttribute(window, "AXTitle"),
        "tree": tree as Any,
        "truncated": budget.truncated,
    ]
}

func opAct(_ request: [String: Any]) -> [String: Any] {
    guard let pid = request["pid"] as? Int else { return ["ok": false, "error": "No pid given."] }
    let ref = request["ref"] as? String ?? ""
    let app = AXUIElementCreateApplication(pid_t(pid))
    enableChromiumAccessibility(app)
    guard let window = windowOf(app) else {
        if screenIsLocked() { return ["ok": false, "locked": true, "error": LOCKED_SCREEN_ERROR] }
        return ["ok": false, "error": "The application has no window open."]
    }
    guard let element = elementAt(window, ref) else {
        return [
            "ok": false,
            "error": "The element at \(ref) is gone. The window changed between reading it and " +
                "acting on it.",
        ]
    }
    switch request["action"] as? String ?? "" {
    case "press":
        let result = AXUIElementPerformAction(element, "AXPress" as CFString)
        if result != .success {
            return ["ok": false, "error": "AXPress was refused with error \(result.rawValue)."]
        }
        return ["ok": true]
    case "focus":
        let result = AXUIElementSetAttributeValue(element, "AXFocused" as CFString, kCFBooleanTrue)
        if result != .success {
            return ["ok": false, "error": "The element refused focus with error \(result.rawValue)."]
        }
        return ["ok": true]
    case "setValue":
        // Focused first, then set. Some controls accept a value only while
        // they hold focus, because they commit a change they believe a person
        // made.
        AXUIElementSetAttributeValue(element, "AXFocused" as CFString, kCFBooleanTrue)
        let text = request["value"] as? String ?? ""
        let result = AXUIElementSetAttributeValue(element, "AXValue" as CFString, text as CFString)
        if result != .success {
            return [
                "ok": false,
                "error": "The field refused a value with error \(result.rawValue). It is read " +
                    "only, or it only accepts typing.",
            ]
        }
        return ["ok": true]
    default:
        return ["ok": false, "error": "Unknown action."]
    }
}

// MARK: entry

func emit(_ answer: [String: Any]) {
    guard let data = try? JSONSerialization.data(withJSONObject: answer),
          let line = String(data: data, encoding: .utf8) else {
        print("{\"ok\":false,\"error\":\"The answer could not be encoded as JSON.\"}")
        return
    }
    print(line)
}

let argument = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : ""
guard let data = argument.data(using: .utf8),
      let request = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] else {
    emit(["ok": false, "error": "The request was not a JSON object."])
    exit(0)
}

switch request["op"] as? String ?? "" {
case "trust":
    emit(opTrust())
case "apps":
    emit(opApps())
case "activate":
    emit(opActivate(pid_t(request["pid"] as? Int ?? 0)))
case "snapshot":
    emit(opSnapshot(request))
case "act":
    emit(opAct(request))
default:
    emit(["ok": false, "error": "Unknown op."])
}
