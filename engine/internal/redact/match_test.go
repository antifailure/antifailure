package redact

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mustCompileIn(s string) *regexp.Regexp { return regexp.MustCompile(s) }

func TestMatcher_EmptyLiteralSetMatchesNothing(t *testing.T) {
	t.Parallel()
	m := newMatcher(nil, false)
	require.True(t, m.empty)
	require.Empty(t, m.matchSet("anything at all"))
}

func TestMatcher_SingleByteLiteralsAreSkipped(t *testing.T) {
	t.Parallel()
	// A one byte literal cannot be indexed by a two byte prefix. Skipping it
	// rather than mis-indexing it is what keeps the table sound; the callers
	// never supply one, since Register enforces twelve bytes and every rule
	// literal is longer.
	m := newMatcher([]string{"x"}, false)
	require.True(t, m.empty, "a set of only one byte literals is empty")
	require.Empty(t, m.matchSet("xxxx"))
}

func TestMatcher_MixedLengthsIndexOnlyTheUsableLiterals(t *testing.T) {
	t.Parallel()
	m := newMatcher([]string{"a", "bcd"}, false)
	require.False(t, m.empty)
	require.Equal(t, []int32{1}, m.matchSet("zzbcdzz"))
	require.Empty(t, m.matchSet("aaaa"))
}

func TestMatcher_ReturnsEachLiteralAtMostOnce(t *testing.T) {
	t.Parallel()
	m := newMatcher([]string{"ab"}, false)
	require.Equal(t, []int32{0}, m.matchSet("ab ab ab ab"))
}

func TestMatcher_ResultsAreAscending(t *testing.T) {
	t.Parallel()
	m := newMatcher([]string{"zz", "yy", "xx"}, false)
	// Present in reverse order in the input; the result is still ascending by
	// literal index, so rule application order is deterministic.
	require.Equal(t, []int32{0, 1, 2}, m.matchSet("xx yy zz"))
}

func TestMatcher_SharedPrefixCandidatesAreBothChecked(t *testing.T) {
	t.Parallel()
	m := newMatcher([]string{"abcd", "abef"}, false)
	require.Equal(t, []int32{1}, m.matchSet("--abef--"))
	require.Equal(t, []int32{0, 1}, m.matchSet("abcd abef"))
}

func TestMatcher_PrefixHitThatDoesNotCompleteIsRejected(t *testing.T) {
	t.Parallel()
	// The two byte prefix "ab" is present but the full literal is not. The
	// verification step must reject it rather than reporting a match.
	m := newMatcher([]string{"abcdef"}, false)
	require.Empty(t, m.matchSet("abzzzz"))
}

func TestMatcher_LiteralRunningPastTheEndIsRejected(t *testing.T) {
	t.Parallel()
	m := newMatcher([]string{"abcdef"}, false)
	require.Empty(t, m.matchSet("xxabc"), "a truncated tail is not a match")
}

func TestMatcher_FoldingMatchesEveryCase(t *testing.T) {
	t.Parallel()
	m := newMatcher([]string{"akia"}, true)
	for _, s := range []string{"AKIA", "akia", "AkIa", "aKIA"} {
		require.Equal(t, []int32{0}, m.matchSet("id="+s+"XYZ"), "case %q", s)
	}
}

func TestMatcher_FoldingRejectsANonMatchingTail(t *testing.T) {
	t.Parallel()
	m := newMatcher([]string{"akiaq"}, true)
	require.Empty(t, m.matchSet("AKIAZ"))
}

func TestMatcher_ShortInputsAreSafe(t *testing.T) {
	t.Parallel()
	m := newMatcher([]string{"ab"}, false)
	for _, s := range []string{"", "a", "b"} {
		require.Empty(t, m.matchSet(s))
	}
	require.Equal(t, []int32{0}, m.matchSet("ab"))
}

func TestLowerByte_FoldsOnlyAsciiLetters(t *testing.T) {
	t.Parallel()
	require.Equal(t, byte('a'), lowerByte('A'))
	require.Equal(t, byte('z'), lowerByte('Z'))
	require.Equal(t, byte('a'), lowerByte('a'))
	require.Equal(t, byte('0'), lowerByte('0'))
	require.Equal(t, byte('_'), lowerByte('_'))
	require.Equal(t, byte(0xC3), lowerByte(0xC3), "a non ASCII byte is left alone")
}

func TestRedactor_ZeroValueIsUsable(t *testing.T) {
	t.Parallel()
	// A Redactor built as a zero value has no stored exact set. It must behave
	// as a redactor with nothing registered rather than panic, so that a
	// struct literal in a test or an embedded field cannot crash a log write.
	var r Redactor
	require.Equal(t, "plain text", r.String("plain text"))
	require.Zero(t, r.RegisteredCount())
}

func TestRedactor_RuleWithNoRequireAlwaysRuns(t *testing.T) {
	t.Parallel()
	// A rule that declares no prefilter literal is unconditional. Custom rule
	// sets will use this, so the always-run path has to work.
	r := NewWithRules([]Rule{{
		Name:    "unconditional",
		Pattern: mustCompileIn(`\bZZQ[0-9]{6}\b`),
	}})
	require.Equal(t, "id="+Marker, r.String("id=ZZQ123456"))
	require.Equal(t, "nothing", r.String("nothing"))
}

func TestRedactor_StopsAfterMaxPasses(t *testing.T) {
	t.Parallel()
	// A rule whose replacement re-triggers it would loop forever without the
	// cap. It must return, and it must have made progress.
	r := NewWithRules([]Rule{{
		Name:    "grows",
		Require: []string{"qq"},
		Pattern: mustCompileIn(`qq`),
	}})
	// Each pass replaces every "qq"; the marker contains none, so this
	// converges in two passes and exercises the change-detection exit.
	require.Equal(t, Marker+" "+Marker, r.String("qq qq"))
}

func TestRedactor_SeveralLiteralsOfOneRuleRunItOnce(t *testing.T) {
	t.Parallel()
	// stripe-key declares six literals. A line containing two of them must not
	// run the rule twice, which the lastRule guard prevents.
	r := New()
	got := r.String("a=" + fakeKeyIn(stripeSecretLiveIn, 22) + " b=" + fakeKeyIn(stripePublicLiveIn, 22))
	require.NotContains(t, got, stripeSecretLiveIn)
	require.NotContains(t, got, stripePublicLiveIn)
}

func TestRedactor_EmptyRuleSetStillRedactsRegisteredSecrets(t *testing.T) {
	t.Parallel()
	r := NewWithRules(nil)
	require.True(t, r.Register("a-registered-secret-value"))
	require.NotContains(t, r.String("v=a-registered-secret-value"), "a-registered-secret-value")
}

func TestRedactor_MaxPassesBoundsARuleThatRewritesItsOwnMarker(t *testing.T) {
	t.Parallel()
	// The pathological case the cap exists for: a rule whose replacement
	// contains text the rule matches again. Redacting every "e" reintroduces
	// the two "e"s inside the marker, and the prefilter literal "ed" is inside
	// the marker too, so nothing about the input stops the loop. The cap must,
	// and it must return rather than hang or exhaust memory.
	r := NewWithRules([]Rule{{
		Name:    "self-feeding",
		Require: []string{"ed"},
		Pattern: mustCompileIn(`e`),
	}})

	done := make(chan string, 1)
	go func() { done <- r.String("ed") }()
	select {
	case got := <-done:
		require.NotEmpty(t, got)
		require.Contains(t, got, Marker)
		// Growth is bounded by the pass cap, not by the input.
		require.Less(t, len(got), 1<<20)
	case <-time.After(10 * time.Second):
		t.Fatal("redaction did not terminate under a self feeding rule")
	}
}
