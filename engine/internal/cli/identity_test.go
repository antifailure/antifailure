package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Where the repository name comes from, and what must never come with it.
//
// The name creates a row in the control plane's repositories table the first
// time it is reported, so a parser that keeps too much of the URL puts junk
// into a customer's console, and a parser that keeps the wrong part of a CI
// remote puts a live installation token on the event stream. Both are tested
// here rather than assumed, because the checkout GitHub Actions produces is
// the credential-bearing form and it is the one this runs against every day.

func TestRepoFromRemote_EveryFormAForgeActuallyWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote string
		want   string
	}{
		{"https", "https://github.com/antifailure/antifailure.git", "antifailure/antifailure"},
		{"https without the suffix", "https://github.com/antifailure/antifailure", "antifailure/antifailure"},
		{"https with a trailing slash", "https://github.com/antifailure/antifailure/", "antifailure/antifailure"},
		{"scp", "git@github.com:antifailure/antifailure.git", "antifailure/antifailure"},
		{"ssh url", "ssh://git@github.com/antifailure/antifailure.git", "antifailure/antifailure"},
		{"ssh url with a port", "ssh://git@github.com:22/antifailure/antifailure.git", "antifailure/antifailure"},
		{"self hosted", "https://git.example.test/team/app.git", "team/app"},
		{"a gitlab subgroup keeps only the last two, which is what the id is", "https://gitlab.com/group/sub/app.git", "sub/app"},
		{"nothing at all", "", ""},
		{"a bare name with no owner", "app", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, repoFromRemote(tc.remote))
		})
	}
}

func TestRepoFromRemote_TheCIRemoteCarriesACredentialAndNoneOfItSurvives(t *testing.T) {
	// This is literally what actions/checkout leaves in origin. Anything that
	// kept the host would put a live installation token into an event payload
	// stored in the control plane's database and readable in the console.
	const remote = "https://x-access-token:ghs_averyrealtokenindeed@github.com/antifailure/antifailure.git"

	got := repoFromRemote(remote)

	require.Equal(t, "antifailure/antifailure", got)
	require.NotContains(t, got, "ghs_", "the token reached the repository name")
	require.NotContains(t, got, "x-access-token")
}

func TestCurrentPullRequest_OnlyAPullRequestRefIsAPullRequest(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want int
	}{
		{"refs/pull/42/merge", 42},
		{"refs/pull/1/head", 1},
		{"refs/heads/main", 0},
		{"refs/tags/v1.2.3", 0},
		// No trailing segment, so the shape is not the one GitHub writes and
		// the number is not trusted.
		{"refs/pull/42", 0},
		{"refs/pull/not-a-number/merge", 0},
		{"refs/pull/0/merge", 0},
		{"", 0},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			e := &Env{Getenv: func(k string) string {
				if k == "GITHUB_REF" {
					return tc.ref
				}
				return ""
			}}
			require.Equal(t, tc.want, currentPullRequest(e))
		})
	}
}

func TestCurrentRepository_TheForgesOwnSpellingWinsOverTheRemote(t *testing.T) {
	// GITHUB_REPOSITORY is the string GitHub itself uses, and the control
	// plane's repositories table is filled from the same string by the App
	// webhook. Preferring the remote would mean an environment reported under
	// a name that does not match the row the webhook created, and the console
	// would show two repositories for one repository.
	e := &Env{Getenv: func(k string) string {
		if k == "GITHUB_REPOSITORY" {
			return "antifailure/antifailure"
		}
		return ""
	}}
	require.Equal(t, "antifailure/antifailure", currentRepository(e, t.TempDir()))
}

func TestCurrentRepository_NoForgeAndNoRemoteIsEmptyRatherThanAGuess(t *testing.T) {
	// A directory that is not a repository at all. Empty is the honest answer:
	// the control plane treats it as "not told" and says so in the response,
	// where a guessed name would silently create a repository nobody has.
	e := &Env{Getenv: func(string) string { return "" }}
	require.Empty(t, currentRepository(e, t.TempDir()))
}
