package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/privatefs"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// The state directory check asks who can read the directory rather than what
// its mode bits say. On Windows every directory reports 0777, so the mode check
// warned on every Windows machine and told the person to run chmod.
func TestDoctorStateDirectory_AsksWhoCanReadIt(t *testing.T) {
	work := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(work, state.DirName), 0o700))
	env := &Env{WorkDir: work}

	r := checkStateDirectory(context.Background(), env, fakeProber{})
	require.Equal(t, CheckPass, r.Status, r.Detail)

	exposed := fmt.Errorf("%w: BUILTIN\\Users can read it", privatefs.ErrExposed)
	r = checkStateDirectory(context.Background(), env, fakeProber{privateErr: exposed})
	require.Equal(t, CheckWarn, r.Status)
	require.Contains(t, r.Detail, `BUILTIN\Users can read it`)
	require.Contains(t, r.Remediation,
		privatefs.RestrictCommand(filepath.Join(work, state.DirName), true),
		"the fix it names is the one this platform has")

	// A directory whose access could not be read at all is not called private.
	r = checkStateDirectory(context.Background(), env, fakeProber{privateErr: errors.New("access denied")})
	require.Equal(t, CheckWarn, r.Status)
	require.Contains(t, r.Detail, "could not be determined")
}

// Seed commands run in an sh that, on Windows, comes with Git for Windows.
// A machine without it hears so from doctor rather than at the seed step,
// after the images are built.
func TestDoctorHostShell_SaysWhereSeedCommandsRun(t *testing.T) {
	r := checkHostShell(context.Background(), &Env{}, fakeProber{})
	require.Equal(t, CheckPass, r.Status)
	require.Contains(t, r.Detail, "/bin/sh")

	r = checkHostShell(context.Background(), &Env{}, fakeProber{hostShellErr: errors.New("not found")})
	require.Equal(t, CheckWarn, r.Status)
	require.NotEmpty(t, r.Remediation)
}

// And the check is in the report, which is the difference between a check and
// a function nobody calls.
func TestDoctorHostShell_IsInTheCatalog(t *testing.T) {
	want := reflect.ValueOf(checkHostShell).Pointer()
	for _, c := range doctorChecks {
		if reflect.ValueOf(c).Pointer() == want {
			return
		}
	}
	t.Fatal("checkHostShell is not in doctorChecks")
}
