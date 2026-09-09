package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/emulator"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// The failure these exist for: af license status reported an extension on a
// stock community build.
//
// The engine resolves an emulate rule through the registry and through nothing
// else, so the emulators this repository ships are registered at startup like
// any other. That is correct for resolution and wrong for this command, which
// is read as evidence that somebody added something to the binary. A community
// build that always had an AWS emulator has had nothing added to it.

func TestPluggedIn_SaysNothingWasAddedWhenOnlyTheShippedEmulatorsAreRegistered(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	emulator.RegisterBuiltin(r)

	// The premise, so that this cannot pass by registering nothing at all.
	require.NotEmpty(t, r.EmulatorNames(),
		"this build ships no emulators, so the case under test does not exist")
	require.NotEmpty(t, r.Registered())

	require.Empty(t, pluggedIn(r))
}

// An organization with its own licensed image registers under the SAME name,
// and that registration is the one thing here somebody really did plug in.
// Filtering by name rather than by identity would hide exactly it.
func TestPluggedIn_ReportsAnOutsideEmulatorRegisteredUnderAShippedName(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	r.AddEmulator(theirEmulator{})
	emulator.RegisterBuiltin(r)

	got, ok := r.EmulatorNamed(emulator.AWSName)
	require.True(t, ok)
	require.IsType(t, theirEmulator{}, got,
		"RegisterBuiltin overwrote an outside registration, which is a different bug")

	require.Equal(t, []string{"emulator:" + emulator.AWSName}, pluggedIn(r))
}

func TestPluggedIn_ReportsARegistrationAtAnotherSocket(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	emulator.RegisterBuiltin(r)
	r.AddAuditSink(theirAuditSink{})

	require.Equal(t, []string{"audit:theirs"}, pluggedIn(r))
}

// theirEmulator stands for an organization's own image, registered under a
// name this build also ships.
type theirEmulator struct{}

func (theirEmulator) Name() string    { return emulator.AWSName }
func (theirEmulator) Hosts() []string { return []string{"s3.amazonaws.com"} }
func (theirEmulator) Container() extension.EmulatorContainer {
	return extension.EmulatorContainer{
		Image: "example.invalid/localstack@sha256:" +
			"0000000000000000000000000000000000000000000000000000000000000000",
		Port: 4566,
	}
}

// theirAuditSink stands for a registration at a socket that has nothing to do
// with emulators, so the filter is shown to remove one thing and not
// everything.
type theirAuditSink struct{}

func (theirAuditSink) Name() string { return "theirs" }
func (theirAuditSink) Write(context.Context, extension.AuditEntry) error {
	return nil
}
