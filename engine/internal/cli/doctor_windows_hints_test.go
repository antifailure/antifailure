package cli

import (
	"runtime"
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
