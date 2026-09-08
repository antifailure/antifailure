package env

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Resolving a rule against the registry, which is where the manifest and the
// build meet on this subject and the only place they do.

// testEmulator is a registration, with nothing behind it. The registry stores
// declarations, so a declaration is all a resolver test needs.
type testEmulator struct {
	name  string
	hosts []string
	image string
	port  int
	env   map[string]string
	cmd   []string
}

func (e testEmulator) Name() string    { return e.name }
func (e testEmulator) Hosts() []string { return e.hosts }
func (e testEmulator) Container() extension.EmulatorContainer {
	return extension.EmulatorContainer{
		Image: e.image, Port: e.port, Env: e.env, Command: e.cmd,
	}
}

const pinnedImage = "localstack/localstack@sha256:" +
	"0000000000000000000000000000000000000000000000000000000000000000"

func registryWith(emulators ...extension.Emulator) *extension.Registry {
	r := &extension.Registry{}
	for _, e := range emulators {
		r.AddEmulator(e)
	}
	return r
}

func emulateRule(host, emulator string) schema.EgressRule {
	return schema.EgressRule{Host: host, Mode: schema.ModeEmulate, Emulator: emulator}
}

func TestEmulatorsFor_CarriesTheRegistrationAndNotTheManifest(t *testing.T) {
	t.Parallel()
	// Everything about the container comes from the registration. A manifest
	// that could name an image could name any image, which is why the rule
	// carries a name and nothing else.
	reg := registryWith(testEmulator{
		name: "localstack", hosts: []string{"s3.amazonaws.com"},
		image: pinnedImage, port: 4566,
		env: map[string]string{"SERVICES": "s3"},
		cmd: []string{"localstack", "start"},
	})
	specs, err := emulatorsFor(&schema.Egress{
		Rules: []schema.EgressRule{emulateRule("s3.amazonaws.com", "localstack")},
	}, reg)
	require.NoError(t, err)
	require.Len(t, specs, 1)
	require.Equal(t, "localstack", specs[0].Name)
	require.Equal(t, pinnedImage, specs[0].Image)
	require.Equal(t, 4566, specs[0].Port)
	require.Equal(t, map[string]string{"SERVICES": "s3"}, specs[0].Env)
	require.Equal(t, []string{"localstack", "start"}, specs[0].Command)
}

func TestEmulatorsFor_TwoRulesOneEmulatorStartOneContainer(t *testing.T) {
	t.Parallel()
	// Path style and virtual hosted addressing are two hostnames for one
	// service, so two rules naming one LocalStack is the ordinary case rather
	// than a mistake. Starting it twice would collide on the container name
	// and the network alias.
	reg := registryWith(testEmulator{
		name: "localstack", hosts: []string{"s3.amazonaws.com"}, image: pinnedImage, port: 4566,
	})
	specs, err := emulatorsFor(&schema.Egress{
		Rules: []schema.EgressRule{
			emulateRule("s3.*.amazonaws.com", "localstack"),
			emulateRule("*.s3.*.amazonaws.com", "localstack"),
		},
	}, reg)
	require.NoError(t, err)
	require.Len(t, specs, 1, "two rules naming one emulator started %d containers", len(specs))
}

func TestEmulatorsFor_IgnoresEveryOtherMode(t *testing.T) {
	t.Parallel()
	reg := registryWith(testEmulator{
		name: "localstack", hosts: []string{"s3.amazonaws.com"}, image: pinnedImage, port: 4566,
	})
	specs, err := emulatorsFor(&schema.Egress{
		Rules: []schema.EgressRule{
			{Host: "api.stripe.com", Mode: schema.ModeSandbox, Credential: "STRIPE"},
			{Host: "api.resend.com", Mode: schema.ModeCapture},
		},
	}, reg)
	require.NoError(t, err)
	require.Empty(t, specs,
		"a policy with no emulate rule started an emulator, so every environment would "+
			"pay for a container nothing routes to")
}

func TestEmulatorsFor_RefusesAnEmulatorThisBuildDoesNotHave(t *testing.T) {
	t.Parallel()
	// Refused, not blocked. Blocking reads as a missing egress rule and sends
	// somebody to edit the manifest they already wrote correctly.
	reg := registryWith(testEmulator{
		name: "azurite", hosts: []string{"blob.core.windows.net"}, image: pinnedImage, port: 10000,
	})
	_, err := emulatorsFor(&schema.Egress{
		Rules: []schema.EgressRule{emulateRule("s3.amazonaws.com", "localstack")},
	}, reg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "localstack", "the refusal does not name what was asked for")
	require.Contains(t, err.Error(), "azurite",
		"the refusal does not list what IS registered, so a typo and a build with the "+
			"wrong emulator read identically")
}

func TestEmulatorsFor_SaysSoWhenNothingIsRegisteredAtAll(t *testing.T) {
	t.Parallel()
	// The two ways to arrive at a refusal are a typo and a build with nothing
	// registered, and they want completely different next steps. A message
	// saying only "unknown emulator" sends the second reader looking for a
	// spelling mistake that is not there.
	_, err := emulatorsFor(&schema.Egress{
		Rules: []schema.EgressRule{emulateRule("s3.amazonaws.com", "localstack")},
	}, registryWith())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no emulators registered at all")
	require.Contains(t, err.Error(), "supplied by a build rather than by the manifest",
		"the message does not say where an emulator comes from, which is the one thing "+
			"the reader needs and cannot guess")
}

func TestEmulatorsFor_RefusesARuleWithNoEmulatorNamed(t *testing.T) {
	t.Parallel()
	// The manifest validator catches this and can point at the line. Reaching
	// here means a manifest that never went through it, so this refuses
	// rather than assuming.
	_, err := emulatorsFor(&schema.Egress{
		Rules: []schema.EgressRule{{Host: "s3.amazonaws.com", Mode: schema.ModeEmulate}},
	}, registryWith())
	require.Error(t, err)
	require.Contains(t, err.Error(), "names no emulator")
}

func TestEmulatorsFor_NoEgressSectionIsNotAnError(t *testing.T) {
	t.Parallel()
	specs, err := emulatorsFor(nil, registryWith())
	require.NoError(t, err)
	require.Empty(t, specs)
}

func TestUnregisteredEmulator_ListsTheRegisteredNamesInOneOrder(t *testing.T) {
	t.Parallel()
	// Sorted, because a message built from a map prints in a different order
	// every run and two reports of one failure then look like two failures.
	msg := unregisteredEmulator(
		emulateRule("s3.amazonaws.com", "localstack"),
		[]string{"pubsub", "azurite"})
	require.Less(t, strings.Index(msg, "azurite"), strings.Index(msg, "pubsub"))
}
