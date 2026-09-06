package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Every spelling of a github.com remote, and the ones that only look like it.
func TestIsGitHubURL(t *testing.T) {
	for _, u := range []string{
		"https://github.com/acme/shop.git",
		"https://github.com/acme/shop",
		"git@github.com:acme/shop.git",
		"ssh://git@github.com/acme/shop.git",
		"git://github.com/acme/shop.git",
		"HTTPS://GitHub.com/acme/shop",
	} {
		require.True(t, isGitHubURL(u), u)
	}
	for _, u := range []string{
		"https://gitlab.com/acme/shop.git",
		"git@bitbucket.org:acme/shop.git",
		"https://github.example.com/acme/shop.git",
		"https://notgithub.com/acme/shop",
		"",
	} {
		require.False(t, isGitHubURL(u), u)
	}
}

// A worktree's .git is a file naming the real directory, and the remotes
// live in the common directory that directory points at. af init runs in
// worktrees all day in this repository, so the pointer has to be followed.
func TestGitHubRemote_FollowsAWorktreePointer(t *testing.T) {
	main := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git", "worktrees", "wt"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(main, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = https://github.com/acme/shop.git\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(main, ".git", "worktrees", "wt", "commondir"),
		[]byte("../..\n"), 0o600))

	wt := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n"), 0o600))
	sub := filepath.Join(wt, "api", "handlers")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	require.True(t, githubRemote(sub), "the remote is on github.com, two pointers away")
	require.Equal(t, wt, gitWorkTree(sub), "the workflow belongs at the top of the worktree")
}

func TestGitHubRemote_IsFalseWithNoRepositoryOrNoRemote(t *testing.T) {
	require.False(t, githubRemote(t.TempDir()))

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "config"),
		[]byte("[core]\n\tbare = false\n[user]\n\turl = https://github.com/not-a-remote\n"), 0o600))
	require.False(t, githubRemote(dir), "a url under a section that is not a remote is not a remote")
}

// The secrets are named from the manifest, and only the ones the manifest
// can use. A repository with no sandbox rule for Stripe has no use for a
// Stripe key, and listing it would send somebody to create a secret nothing
// reads.
func TestOptionalSecrets_FollowTheManifest(t *testing.T) {
	require.Equal(t, []string{"ANTHROPIC_API_KEY", "AF_MASKING_KEY"}, optionalSecrets(&schema.Manifest{}))

	m := &schema.Manifest{
		Database: &schema.Database{SourceURLEnv: "PRODUCTION_DATABASE_URL"},
		Egress: &schema.Egress{Rules: []schema.EgressRule{
			{Host: "api.stripe.com", Mode: schema.ModeSandbox, Credential: "STRIPE_SECRET_KEY"},
		}},
	}
	require.Equal(t, []string{"ANTHROPIC_API_KEY", "AF_MASKING_KEY", "PRODUCTION_DATABASE_URL",
		"STRIPE_TEST_SECRET_KEY"}, optionalSecrets(m))

	mocked := &schema.Manifest{Egress: &schema.Egress{Rules: []schema.EgressRule{
		{Host: "api.stripe.com", Mode: schema.ModeMock},
	}}}
	require.NotContains(t, optionalSecrets(mocked), "STRIPE_TEST_SECRET_KEY",
		"a mocked Stripe never sends a key, so there is nothing to set")
}
