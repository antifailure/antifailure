package policy

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What a leading wildcard in allow actually reaches, measured rather than read
// off the code.
//
// The finding that prompted this said *.zapier.com in allow was explained as a
// clean ALLOW. Deciding what to say about such a rule needed its reach
// established first, because a caution that states the wrong breadth is its own
// defect. Each case is one row of that measurement, and each has a line in the
// matcher whose breaking turns it red.
func TestALeadingWildcardInAllowReaches(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("*.zapier.com", schema.ModeAllow))
	for name, tc := range map[string]struct {
		host string
		want schema.Mode
	}{
		"not the apex":                      {"zapier.com", schema.ModeBlock},
		"not a lookalike apex":              {"evilzapier.com", schema.ModeBlock},
		"one label":                         {"hooks.zapier.com", schema.ModeAllow},
		"many labels":                       {"a.b.c.hooks.zapier.com", schema.ModeAllow},
		"a trailing dot":                    {"hooks.zapier.com.", schema.ModeAllow},
		"mixed case":                        {"HOOKS.Zapier.COM", schema.ModeAllow},
		"a punycode label":                  {"xn--80ak6aa92e.zapier.com", schema.ModeAllow},
		"a unicode label":                   {"hööks.zapier.com", schema.ModeAllow},
		"a unicode lookalike":               {"hooks.zаpier.com", schema.ModeBlock},
		"only the dotted apex":              {".zapier.com", schema.ModeBlock},
		"an empty label, as a stated limit": {"a..zapier.com", schema.ModeAllow},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, e.Evaluate(Request{Host: tc.host, TLS: true, Path: "/"}).Mode)
		})
	}
}

// The empty label case above matches, and it is pinned as a stated limit
// rather than as a wish. The suffix test reads a.. as a label followed by an
// empty one and the star covers both. Nothing reaches such a host, because the
// sidecar dials the name it was given and a name with an empty label does not
// resolve, so the policy answering allow for it lets nothing out. It is written
// down so that a reader of the matcher does not assume it was checked.

func TestALeadingWildcardWrittenInCapitalsStillMatches(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("*.ZAPIER.com", schema.ModeAllow))
	require.Equal(t, schema.ModeAllow,
		e.Evaluate(Request{Host: "hooks.zapier.com", TLS: true, Path: "/"}).Mode)
}
