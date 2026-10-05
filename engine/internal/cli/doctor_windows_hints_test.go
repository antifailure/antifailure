package cli

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every hint names something this platform can do. The default branch, "for
// your platform", is what Windows got before it had a case of its own, and it
// tells a person nothing they did not know.
func TestDoctorHintsNameThisPlatformsFix(t *testing.T) {
	for name, hint := range map[string]string{
		"install Docker": dockerInstallHint(),
		"start Docker":   dockerStartHint(),
	} {
		require.NotContains(t, hint, "for your platform", "%s on %s", name, runtime.GOOS)
		require.NotContains(t, hint, "Start the Docker daemon, then", "%s on %s", name, runtime.GOOS)
	}
}

// The way to the latest release is one that works here. af update refuses on
// Windows, so telling a Windows user to run it sent them to a refusal.
func TestUpdateRemediationIsOneThisPlatformCanRun(t *testing.T) {
	got := updateRemediation()
	if runtime.GOOS == "windows" {
		require.Contains(t, got, "install.ps1")
		require.False(t, strings.Contains(got, "Run 'af update'"), got)
		return
	}
	require.Contains(t, got, "Run 'af update'")
}
