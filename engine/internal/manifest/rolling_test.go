package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInsights_RollingDefaultsAreFilledIn(t *testing.T) {
	t.Parallel()
	// Filled in rather than left nil, so `af explain` can print which commit
	// this repository's rolling check compares against. A block whose
	// effective value nobody can see is a block people set twice.
	m := mustParse(t, minimal)
	require.NotNil(t, m.Insights.RollingCompatibility)
	require.Equal(t, "risky", m.Insights.RollingCompatibility.When)
	require.Equal(t, "merge-base", m.Insights.RollingCompatibility.Against)
}

func TestInsights_RollingKeepsWhatWasWritten(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`
insights:
  rolling_compatibility:
    when: always
    against: v2.4.0
`)
	require.Equal(t, "always", m.Insights.RollingCompatibility.When)
	require.Equal(t, "v2.4.0", m.Insights.RollingCompatibility.Against)
}

func TestInsights_RollingRejectsAWhenNobodyImplements(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`
insights:
  rolling_compatibility:
    when: sometimes
`)
	require.Contains(t, messages(problems(t, err)), "not one of never, risky or always")
}

func TestInsights_RollingRejectsARevisionGitWouldMisread(t *testing.T) {
	t.Parallel()
	// Only the shapes that are wrong whatever the checkout holds. Whether a
	// revision exists is a question about the clone rather than about the
	// manifest, and refusing a manifest because somebody did a shallow clone
	// would fail for the wrong reason.
	for body, want := range map[string]string{
		"    against: origin/main HEAD": "contains whitespace",
		"    against: --exec=rm":        "starts with a hyphen",
	} {
		_, err := parse(t, minimal+"\ninsights:\n  rolling_compatibility:\n"+body+"\n")
		require.Contains(t, messages(problems(t, err)), want, body)
	}

	// A tag, a branch and a revision expression are all accepted, because git
	// is what decides whether they resolve.
	for _, ok := range []string{"v2.4.0", "origin/main", "HEAD~3", "merge-base"} {
		_, err := parse(t, minimal+"\ninsights:\n  rolling_compatibility:\n    against: "+ok+"\n")
		require.NoError(t, err, ok)
	}
}
