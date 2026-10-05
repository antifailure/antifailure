package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The commands a start rung hands over are for the shell the installer left
// the person in. A Windows user installed with install.ps1 is in PowerShell,
// where `export PATH=...` and `which` do not exist, and the binary is af.exe.
func TestStartCommandsAreForThePlatformsShell(t *testing.T) {
	win := shellCommands("windows")
	require.Equal(t, "af.exe", win.binary)
	require.Equal(t, "where.exe af", win.listAll)
	require.Equal(t, `$env:Path = "C:\Users\me\.antifailure\bin;" + $env:Path`,
		win.addToPath(`C:\Users\me\.antifailure\bin`))

	unix := shellCommands("darwin")
	require.Equal(t, "af", unix.binary)
	require.Equal(t, "which -a af", unix.listAll)
	require.Equal(t, `export PATH="/home/me/.antifailure/bin:$PATH"`, unix.addToPath("/home/me/.antifailure/bin"))
}
