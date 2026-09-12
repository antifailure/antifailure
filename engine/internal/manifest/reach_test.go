package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestEgressCaution_SaysHowFarARuleThatLetsRequestsOutReaches(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		host string
		mode schema.Mode
		says []string
	}{
		"one owner's domain": {"*.zapier.com", schema.ModeAllow,
			[]string{"The rule for *.zapier.com names no host.", "every name under zapier.com", "reaches the real host."}},
		// supabase.co is in the public suffix list's private section: every
		// project on it is a different customer's, which is the fact the
		// catalogue's own Supabase rule never said.
		"a platform's suffix": {"*.supabase.co", schema.ModeAllow,
			[]string{"every customer's names under supabase.co, not only yours"}},
		"sandbox carries the credential": {"*.zapier.com", schema.ModeSandbox,
			[]string{"carrying the sandbox credential"}},
		"a star over a public suffix": {"*.co.uk", schema.ModeAllow,
			[]string{"names under co.uk registered by anybody at all"}},
		"a star at the end": {"api.*", schema.ModeAllow,
			[]string{"every top level domain"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := manifest.EgressCaution(tc.host, tc.mode)
			for _, want := range tc.says {
				require.Contains(t, got, want)
			}
		})
	}
}

func TestEgressCaution_IsSilentForARuleThatNamesItsHostOrKeepsTheRequestIn(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		host string
		mode schema.Mode
	}{
		"an exact host": {"hooks.zapier.com", schema.ModeAllow},
		"an address":    {"10.0.4.20", schema.ModeAllow},
		// An interior star under one owner's domain pins the service label and
		// the label count, so it reaches one service in any region.
		"a star in the middle": {"email.*.amazonaws.com", schema.ModeAllow},
		// A wildcard block is conservative, and these three answer inside the
		// environment, so none of them lets a request out.
		"block":   {"*.zapier.com", schema.ModeBlock},
		"capture": {"*.zapier.com", schema.ModeCapture},
		"mock":    {"*.zapier.com", schema.ModeMock},
		"emulate": {"*.zapier.com", schema.ModeEmulate},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Empty(t, manifest.EgressCaution(tc.host, tc.mode))
		})
	}
}

// A star standing where the owner's name goes reaches names anybody registered,
// which is the reach only block may have. It walked straight past the rule that
// refuses * outside block.
func TestParse_RefusesAStarWhereTheOwnersNameGoes(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"*.com", "*.co.uk", "hooks.*.com", "api.*"} {
		for _, mode := range []string{"allow", "capture"} {
			t.Run(host+" in "+mode, func(t *testing.T) {
				t.Parallel()
				body := minimal + "\negress:\n  rules:\n    - host: '" + host + "'\n      mode: " + mode + "\n"
				require.Contains(t, messages(problems(t, mustFail(t, body))), "where the owner's name goes")
			})
		}
	}
}

func TestParse_AStarWhereTheOwnersNameGoesMayStillBlock(t *testing.T) {
	t.Parallel()
	mustParse(t, minimal+"\negress:\n  rules:\n    - host: '*.com'\n      mode: block\n")
}

// The refusal is for the star over a suffix nobody owns, and nothing wider. A
// leading wildcard over one owner's domain is a real decision people need,
// because a Supabase project's host is <ref>.supabase.co and the reference is
// not known when the manifest is written. It is accepted and its breadth is
// said wherever it is explained.
func TestParse_AcceptsALeadingWildcardOverOneOwnersDomain(t *testing.T) {
	t.Parallel()
	for _, rule := range []string{
		"    - host: '*.zapier.com'\n      mode: allow\n",
		"    - host: '*.supabase.co'\n      mode: allow\n",
		"    - host: 'email.*.amazonaws.com'\n      mode: capture\n",
	} {
		mustParse(t, minimal+"\negress:\n  rules:\n"+rule)
	}
}

func TestParse_RefusesASandboxDefault(t *testing.T) {
	t.Parallel()
	body := minimal + "\negress:\n  default: sandbox\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "a default names no credential")
}
