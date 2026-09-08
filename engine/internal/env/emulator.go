package env

import (
	"fmt"
	"sort"
	"strings"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Resolving an emulate rule into a container to start.
//
// This is the only place the manifest and the registry meet on this subject,
// and the shape of that meeting is the point. A rule names an emulator; a
// registration supplies everything else. The manifest cannot name an image, a
// digest, a port or a variable, so a repository cannot decide what answers for
// s3.amazonaws.com inside an environment: only a build can, and the registry
// checked its digest and its host list when it was registered.
//
// A name nothing registered is REFUSED, and it is worth saying why a fallback
// would be worse. The alternatives are to block the host, which reads as a
// missing egress rule and sends somebody to edit the manifest they already
// wrote correctly, or to let the request through, which is the one outcome
// this product exists to prevent. So the environment does not start, and the
// refusal names what is registered.

// emulatorsFor resolves every emulator an egress policy names.
//
// The result is in manifest order with duplicates removed, because two rules
// commonly name one emulator: s3.amazonaws.com and *.s3.*.amazonaws.com are
// two rules and one LocalStack.
func emulatorsFor(
	egress *schema.Egress, registry *extension.Registry,
) ([]provider.EmulatorSpec, error) {
	if egress == nil {
		return nil, nil
	}
	var out []provider.EmulatorSpec
	seen := map[string]bool{}
	for _, r := range egress.Rules {
		if r.Mode != schema.ModeEmulate || seen[r.Emulator] {
			continue
		}
		// An emulate rule with no emulator is refused by the manifest
		// validator, which reads the rule that names none and can point at
		// its line. Reaching here with one would mean a manifest that was
		// never loaded through it, so this refuses rather than assuming.
		if r.Emulator == "" {
			return nil, aferrors.Coded(aferrors.AFRUN040, "detail", fmt.Sprintf(
				"the emulate rule for %s names no emulator", r.Host))
		}
		seen[r.Emulator] = true

		e, ok := registry.EmulatorNamed(r.Emulator)
		if !ok {
			return nil, aferrors.Coded(aferrors.AFRUN040,
				"detail", unregisteredEmulator(r, registry.EmulatorNames()))
		}
		c := e.Container()
		out = append(out, provider.EmulatorSpec{
			Name: e.Name(), Image: c.Image, Port: c.Port, Env: c.Env, Command: c.Command,
		})
	}
	return out, nil
}

// unregisteredEmulator is the sentence somebody reads when a rule names an
// emulator this build does not have.
//
// It lists what IS registered, because the two ways to arrive here are a typo
// and a build with nothing registered at all, and those want completely
// different next steps. A message saying only "unknown emulator" sends the
// second reader looking for a spelling mistake that is not there.
func unregisteredEmulator(r schema.EgressRule, registered []string) string {
	if len(registered) == 0 {
		return fmt.Sprintf(
			"the rule for %s is set to emulate and names %q, and this build has no emulators "+
				"registered at all. An emulator is supplied by a build rather than by the "+
				"manifest, so there is nothing for this rule to reach",
			r.Host, r.Emulator)
	}
	names := append([]string(nil), registered...)
	sort.Strings(names)
	return fmt.Sprintf(
		"the rule for %s is set to emulate and names %q, and this build registers %s",
		r.Host, r.Emulator, strings.Join(names, ", "))
}
