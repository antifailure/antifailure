package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// six is the real list, in schema order. The tests pass it explicitly rather
// than reading the schema, so that a test failure means the rules changed and
// not that somebody edited the enum.
var six = []string{"block", "allow", "capture", "mock", "sandbox", "synth"}

func check(t *testing.T, body string) []finding {
	t.Helper()
	return Check("x.tsx", body, six)
}

func only(t *testing.T, body string) finding {
	t.Helper()
	got := check(t, body)
	if len(got) != 1 {
		t.Fatalf("want one finding, got %d: %+v", len(got), got)
	}
	return got[0]
}

func none(t *testing.T, body string) {
	t.Helper()
	if got := check(t, body); len(got) != 0 {
		t.Fatalf("want no findings, got %d: %+v", len(got), got)
	}
}

// The defect this tool was written for, quoted from the page it shipped on.
func TestTheHistoricalFirewallTitleIsFound(t *testing.T) {
	f := only(t, `title="<strong>Every outbound attempt is recorded.</strong> `+
		`Simulate, capture, mock, or deny. Never a live processor."`)
	for _, want := range []string{"Simulate", "deny", "mock", "block"} {
		if !strings.Contains(f.why, want) {
			t.Errorf("the finding should name %q and its correction: %s", want, f.why)
		}
	}
}

// The corrected line, which must be silent. A gate that still fires after the
// fix is a gate somebody switches off.
func TestTheCorrectedFirewallTitleIsSilent(t *testing.T) {
	none(t, `title="<strong>Every outbound attempt is recorded.</strong> `+
		`Six per-host modes, from refusing outright to answering from an offline pack."`)
}

func TestAWrongCountIsFound(t *testing.T) {
	f := only(t, `summary: "The five per-host egress modes and what happens to a request."`)
	if !strings.Contains(f.why, "There are 6") {
		t.Errorf("the finding should say the real count: %s", f.why)
	}
}

func TestTheRightCountIsSilent(t *testing.T) {
	none(t, `summary: "The six per-host egress modes and what happens to a request."`)
}

// The root cause of eight of the ten historical findings: synth was added to
// the schema and to the proxy and to nothing that describes them.
func TestAPromisedSetMissingSynthIsFound(t *testing.T) {
	f := only(t, "Each host gets a mode: BLOCK refuses with a decision you can read, "+
		"ALLOW lets it through with a rate limit, SANDBOX swaps in test credentials, "+
		"CAPTURE records the email into an inbox, and MOCK answers from an offline pack.")
	if !strings.Contains(f.why, "Missing: synth") {
		t.Errorf("the finding should name what is missing: %s", f.why)
	}
}

func TestACompletePromisedSetIsSilent(t *testing.T) {
	none(t, "Each host gets a mode: BLOCK refuses, ALLOW lets it through with a rate "+
		"limit, SANDBOX swaps in test credentials, CAPTURE records the email into an "+
		"inbox, MOCK answers from an offline pack, and SYNTH asks a model to invent a "+
		"response and marks the result unverified.")
}

func TestARuleLabelNamingANonModeIsFound(t *testing.T) {
	f := only(t, `const RULES = [{ key: "deny", label: "*:deny" }];`)
	if !strings.Contains(f.why, "Write block") {
		t.Errorf("the finding should say what to write: %s", f.why)
	}
}

func TestARuleLabelNamingARealModeIsSilent(t *testing.T) {
	none(t, `const RULES = [{ key: "deny", label: "*:block" }, { label: "auth0:sandbox" }];`)
}

// Everything below is a false alarm this tool produced at some point while it
// was being written, or one it would produce under an obvious looser rule.
// Each is real text from this repository. They are the reason the rules are
// shaped the way they are, and a regression in any of them makes the gate
// untrustworthy rather than merely noisy.
func TestTextThatIsNotAClaimIsSilent(t *testing.T) {
	for name, body := range map[string]string{
		// A true statement about a subset, with no claim of completeness.
		// docs/src/content/docs/concepts/egress.md.
		"a true subset": "`sandbox`, `capture`, `mock` and `synth` all terminate TLS, " +
			"and `allow` does not intercept.",

		// The quickstart, which hedges and forward-references the full table.
		"an explicitly hedged subset": "The modes are covered in [egress](/docs/concepts/egress). " +
			"The short version is that `BLOCK` refuses with a decision you can read, " +
			"`SANDBOX` swaps in test credentials, `CAPTURE` records mail into an inbox, " +
			"and `MOCK` answers from an offline pack.",

		// A UI caption. "capture" and "recorded" are adjacent but nothing is
		// being enumerated, so a rule keyed on adjacency rather than on commas
		// flagged it.
		"a status caption": `body: "POST hooks.slack.com  capture  recorded, never posted",`,

		// A status pill, for the same reason: `tone="block">denied` reads as
		// two mode-ish words in a row once the markup is flattened.
		"a status pill": `<Pill tone="block">denied</Pill>`,

		// An unrelated enum. github.mode is actions, app or off, and the guide
		// says "Two modes" about it correctly.
		"another enum's count": "## Two modes\n\n**`actions`** runs everything inside a workflow.",

		// The other unrelated idiom.
		"failure modes": "Two failure modes to watch for, both of which produce a green run.",

		// True: a host gets one mode. Not a claim that one mode exists.
		"one mode per host": "In Antifailure each host gets one mode, chosen per host rather " +
			"than globally.",

		// Ordinary prose that happens to use the banned near-miss words
		// correctly. This is most of the firewall page.
		"correct English": "Unknown destinations are denied and written to the ledger. " +
			"A denied destination is denied inside the twin.",

		// Code, not prose. The console switches on all six.
		"a switch over the modes": `if (m === "capture" || m === "mock" || m === "synth" || ` +
			`m === "sandbox") return "warn" as const;`,
	} {
		t.Run(name, func(t *testing.T) { none(t, body) })
	}
}

// A markdown fence holds a manifest fragment, where a mode name is data rather
// than a claim about the set.
func TestAFencedManifestIsNotAClaim(t *testing.T) {
	body := "Set the rule:\n\n```yaml\negress:\n  rules:\n    - host: api.stripe.com\n" +
		"      mode: mock\n    - host: api.resend.com\n      mode: capture\n```\n"
	if got := Check("x.md", body, six); len(got) != 0 {
		t.Fatalf("a fenced manifest is data, got %+v", got)
	}
}

// The finding has to point at the line somebody opens to fix it. JSX writes a
// full stop against a tag rather than a space, so sentence splitting alone put
// a page's hero copy under a finding about its section heading.
func TestTheFindingPointsAtTheRightLine(t *testing.T) {
	body := "const a = 1;\nconst b = 2;\nconst c = \"Simulate, capture, mock, or deny.\";\n"
	f := only(t, body)
	if f.line != 3 {
		t.Errorf("line = %d, want 3", f.line)
	}
	if !strings.Contains(f.text, "Simulate, capture, mock, or deny") {
		t.Errorf("the excerpt should quote the defect, got %q", f.text)
	}
}

// The list is read from the schema so that the checker cannot carry its own
// wrong copy. This is the assertion that keeps that true.
func TestModesComeFromTheSchema(t *testing.T) {
	got, err := Modes(filepath.Join("..", "..", "schemas", "manifest.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(six, ",")
	if strings.Join(got, ",") != want {
		t.Fatalf("the schema's egress modes are %q, this test expected %q. If the enum "+
			"really changed, the prose describing it has to change too", strings.Join(got, ","), want)
	}
}

// The schema has a second, unrelated "mode" under github whose values are
// actions, app and off. Matching that one would make this checker enforce the
// wrong list, which is the exact failure it exists to prevent.
func TestTheGitHubModeEnumIsNotMistakenForThisOne(t *testing.T) {
	got, err := Modes(filepath.Join("..", "..", "schemas", "manifest.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		if v == "actions" || v == "app" || v == "off" {
			t.Fatalf("read the github.mode enum instead of the egress one: %v", got)
		}
	}
}
