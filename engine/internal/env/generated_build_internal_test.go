package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/build"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Whether a build gets the generated build excludes is decided from the disk
// before the context is walked, so it has to agree with the switch in
// buildOneFrom that later asks the context the same question.
func TestGeneratesDockerfile_MatchesWhatTheBuildWillUse(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, p := range []string{"apps/api/Dockerfile", "apps/hidden/Dockerfile", "apps/web/package.json"} {
		full := filepath.Join(root, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("x"), 0o644))
	}
	ig, err := build.ParseIgnore(strings.NewReader("apps/hidden/Dockerfile\n"))
	require.NoError(t, err)
	same := func(p string) (string, bool) { return p, true }

	require.False(t, generatesDockerfile(root, schema.Service{Name: "api"}, "apps/api", ig, same),
		"a Dockerfile beside the service is the user's build")
	require.True(t, generatesDockerfile(root, schema.Service{Name: "web"}, "apps/web", ig, same),
		"no Dockerfile means Antifailure writes one")
	require.True(t, generatesDockerfile(root, schema.Service{Name: "hidden"}, "apps/hidden", ig, same),
		"a Dockerfile the ignore file excludes is not in the context, so the build generates one")
	require.False(t, generatesDockerfile(root,
		schema.Service{Name: "web", Build: &schema.Build{Dockerfile: "deploy/web.Dockerfile"}}, "apps/web", ig, same),
		"a named Dockerfile is the user's build")
}
