package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// otherModes is every mode except the one named.
func otherModes(except schema.Mode) []schema.Mode {
	out := make([]schema.Mode, 0, len(schema.AllModes()))
	for _, m := range schema.AllModes() {
		if m != except {
			out = append(out, m)
		}
	}
	return out
}

// The emulate mode, at the point a manifest is read.
//
// Two checks live here and one deliberately does not. Whether the emulator
// NAME is one this build has is decided against the registry, which the
// manifest package cannot see and should not: a manifest is read in CI where
// nothing is registered, and refusing every emulate rule there would make
// af validate useless on a repository that is perfectly correct. That check
// lives in engine/internal/env, at the moment an environment is created, and
// it has its own tests.
//
// What is checkable from the document alone is whether the rule makes sense as
// written, and those two cases are the ones somebody actually types.

const emulateBase = minimal + `
egress:
  default: block
  rules:
`

func TestParse_AcceptsAnEmulateRuleThatNamesAnEmulator(t *testing.T) {
	t.Parallel()
	m := mustParse(t, emulateBase+
		"    - host: s3.amazonaws.com\n      mode: emulate\n      emulator: localstack\n")
	require.Equal(t, "localstack", m.Egress.Rules[0].Emulator)
}

func TestParse_RefusesAnEmulateRuleWithNoEmulator(t *testing.T) {
	t.Parallel()
	// Emulate is routing to something a registration supplied. A rule with
	// nothing to route to could only fall through to block, and a rule that
	// silently does nothing is how somebody comes to believe an environment
	// was tested against S3.
	msg := messages(problems(t, mustFail(t, emulateBase+
		"    - host: s3.amazonaws.com\n      mode: emulate\n")))
	require.Contains(t, msg, `The emulate rule for "s3.amazonaws.com" names no emulator.`)
	require.Contains(t, msg, "emulator: localstack",
		"the hint does not show the shape of the line to write")
}

func TestParse_RefusesAnEmulatorOnEveryOtherMode(t *testing.T) {
	t.Parallel()
	// The same shape as the existing refusals for a credential outside
	// sandbox and fixtures outside mock. A key that does nothing is a key
	// whose author believes it does something.
	// Derived from AllModes rather than written out, so that adding a mode
	// forces a decision here instead of quietly leaving it unchecked. A
	// hardcoded list is how this lane's own defects happened: a seventh mode
	// went into the schema and three other lists did not know about it.
	for _, mode := range otherModes(schema.ModeEmulate) {
		mode := string(mode)
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			body := emulateBase + "    - host: s3.amazonaws.com\n      mode: " + mode +
				"\n      emulator: localstack\n"
			if mode == "sandbox" {
				body += "      credential: AWS_KEY\n"
			}
			msg := messages(problems(t, mustFail(t, body)))
			require.Contains(t, msg,
				"An emulator is only used in emulate mode, and this rule is "+mode+".")
		})
	}
}

func TestParse_RefusesEmulateAsTheDefault(t *testing.T) {
	t.Parallel()
	// The one mode a default cannot express, because the emulator is named on
	// the rule and a default names no rule. The schema still lists it in both
	// enums, because the two enums are required to agree so that a sentence
	// claiming to name every mode can be checked against one list.
	msg := messages(problems(t, mustFail(t, minimal+"\negress:\n  default: emulate\n")))
	require.Contains(t, msg, "The default egress mode is emulate, and a default names no emulator.")
	require.Contains(t, msg, "Set the default to block")
}
