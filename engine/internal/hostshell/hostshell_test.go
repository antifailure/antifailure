package hostshell_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/hostshell"
)

// The command a manifest writes is shell: a variable, a pipe and a redirect
// into a path relative to where it runs. All three have to mean what they mean
// in sh, on every platform, or a seed that works on a Mac writes nowhere on
// Windows.
func TestAManifestCommandRunsAsShell(t *testing.T) {
	dir := t.TempDir()
	cmd, err := hostshell.Command(context.Background(),
		`printf '%s' "$AF_TEST_VALUE" | tr a-z A-Z > out/seeded.txt`)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "out"), 0o755))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "AF_TEST_VALUE=seeded")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)

	got, err := os.ReadFile(filepath.Join(dir, "out", "seeded.txt"))
	require.NoError(t, err)
	require.Equal(t, "SEEDED", string(got))
}
