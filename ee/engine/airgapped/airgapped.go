// Package airgapped turns a licensed word into an installation that reaches
// nothing.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Before this existed, air_gapped was a Feature constant and a line in
// AllFeatures, and those two lines were every mention of it in the repository.
// A customer who bought it got a licence that said air_gapped and a binary that
// checked for updates, exported telemetry, pulled images from Docker Hub, asked
// a model to invent an HTTP response, and forwarded their application's traffic
// to whatever the manifest named, exactly as one without it did. That is worse
// than not selling the feature: an installation that believes it is air gapped
// and makes one call it did not expect is the failure this is supposed to
// prevent, and the belief is the part that does the damage.
//
// So the deliverable here is not a flag. It is two enforcement points and a
// count.
//
// THE PROCESS IS SEALED, which is engine/pkg/airgap. Every outbound client in
// the engine dials through a guard that refuses an address the operator did not
// name, records the attempt, and refuses a hostname BEFORE resolving it so that
// the refusal does not itself leak the name over DNS.
//
// THE ENVIRONMENT IS REFUSED, which is the hook below. The largest outbound
// path in this product is not the engine, it is the application under test:
// egress rules in allow, sandbox and synth modes all end in a connection the
// sidecar makes to the real internet. Sealing this process would do nothing
// about them, because the sidecar is a separate binary in a separate container
// and cannot import this code. What it can be told is that the environment is
// refused before it is created, naming the rules that would have to change.
//
// WHY THE SEAL IS CHECKED ONCE AND NEVER RELEASED. Everywhere else in this
// product a licence is asked per call, so that a lapse degrades a feature
// rather than requiring a restart. This one is the opposite on purpose. The
// expensive direction of the mistake is not "the air gap stopped working", it
// is "the machine in the secure facility started talking to the internet
// because a purchase order was slow". So the licence decides at startup whether
// the process may seal, an installation that asked to be air gapped without a
// licence for it does not start at all, and once sealed nothing unseals it.
package airgapped

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

func init() {
	// Recorded so that a feature which is sold and never checked shows up as
	// such. This registry held three of twelve features and air_gapped was one
	// of the nine that were sold and enforced nowhere.
	feature.Declare(license.FeatureAirGapped, "ee/engine/airgapped.RegisterFromEnvironment")
	feature.Declare(license.FeatureAirGapped, "ee/engine/airgapped.Hook")
}

const (
	// ModeEnv turns the mode on.
	ModeEnv = "AF_AIR_GAPPED"
	// AllowEnv is the operator's own network, which is the only thing an air
	// gapped installation may reach.
	AllowEnv = "AF_AIR_GAPPED_ALLOW"
)

// reachingModes are the egress modes that end in a connection leaving the
// environment.
//
// block, capture and mock are answered inside the sidecar and reach nothing, so
// they are untouched: an air gapped installation is supposed to be able to run
// an application whose third party calls are captured, and refusing those would
// make the mode useless rather than strict.
//
// allow forwards to the real host. sandbox substitutes a test credential and
// still forwards to the real host, which people read as safe and is not: the
// connection is made and the request leaves. synth reaches a model API, which
// is an outbound call to a vendor and also the one people are most surprised
// by, because it looks like a local simulation from the manifest.
var reachingModes = map[string]string{
	"allow":   "forwards the request to the real host",
	"sandbox": "substitutes a test credential and still forwards to the real host",
	"synth":   "asks a model provider to invent the response, which is a call to that provider",
}

// localProviders are the database providers whose control plane is the
// operator's own.
//
// A LIST OF WHAT IS PERMITTED rather than a list of what is not, because the
// set of providers grows and the direction the mistake has to fail in is
// refusal. A provider added next year is refused here until somebody decides
// which side of the line it is on, which is one bad afternoon for whoever adds
// it and the alternative is an air gapped installation quietly reaching a cloud
// nobody had classified.
//
// docker is a container on this machine. dblab talks to a Database Lab Engine
// that defaults to http://127.0.0.1:2345 and is self hosted wherever the
// operator put it. pgurl is a connection string the operator supplied, which is
// their own decision in the same way a kubeconfig is.
//
// neon and supabase are refused because reaching them means api.supabase.com
// and console.neon.tech, which are somebody else's servers on the public
// internet, and there is no configuration that changes that.
var localProviders = map[string]string{
	"docker": "a container on this machine",
	"dblab":  "a Database Lab Engine you host",
	"pgurl":  "a connection string you supplied",
}

// Hook refuses an environment whose egress would leave the operator's network.
//
// It plugs into the same extension.PolicyHook socket the organization policy
// uses, which is deliberate: a hook can only refuse, and there is no return
// value by which an air gap could be widened from outside the repository.
type Hook struct{}

// Name identifies the hook in the refusal.
func (Hook) Name() string { return "air gapped" }

// Check refuses the environment when any rule would reach outside.
//
// Keyed on the seal rather than on the licence per call, and that is the same
// decision the package comment defends: an installation deployed behind an air
// gap must not start creating environments that reach the internet because its
// licence lapsed overnight. The licence decided whether this hook was ever
// registered.
func (Hook) Check(ctx context.Context, req extension.EnvironmentRequest) error {
	if !airgap.Sealed() {
		return nil
	}

	// The database first, because it is the larger problem and because the
	// answer to it is a different line of the manifest. CheckPolicy returns the
	// first refusal by design, so what a reader gets is one class of problem at
	// a time with every instance of that class named.
	if err := checkProvider(req.Provider); err != nil {
		return err
	}

	type offence struct{ host, mode, why string }
	var offences []offence
	// The default first. A manifest with `egress: {default: allow}` and no
	// rules at all reaches the whole internet, EgressModes is empty for it, and
	// a hook that only walked the rules would have called that environment
	// contained. The manifest validator only warns about this shape, so it is
	// not a theoretical one.
	if why, reaching := reachingModes[strings.ToLower(req.EgressDefault)]; reaching {
		offences = append(offences, offence{
			host: "every host no rule names", mode: req.EgressDefault, why: why})
	}
	for host, mode := range req.EgressModes {
		if why, reaching := reachingModes[strings.ToLower(mode)]; reaching {
			offences = append(offences, offence{host: host, mode: mode, why: why})
		}
	}
	if len(offences) == 0 {
		return nil
	}
	sort.Slice(offences, func(i, j int) bool { return offences[i].host < offences[j].host })

	var b strings.Builder
	fmt.Fprintf(&b, "this installation is air gapped, and %d egress rule", len(offences))
	if len(offences) != 1 {
		b.WriteString("s")
	}
	b.WriteString(" would reach outside the operator's network:")
	for _, o := range offences {
		fmt.Fprintf(&b, "\n  %s in %s mode, which %s", o.host, o.mode, o.why)
	}
	b.WriteString("\nThe modes an air gapped environment can use are block, capture and mock, " +
		"which are answered inside the environment. This is refused rather than downgraded, " +
		"because an environment quietly switched from allow to block would report that it " +
		"tested a code path it never reached.")
	return fmt.Errorf("%s", b.String())
}

// checkProvider refuses a database whose control plane is somebody else's.
//
// This closes the largest outbound path the process guard cannot see. A
// Postgres connection goes wherever its URL points, and the URL for a Neon
// branch points at neon.tech, so the connection leaves the network through a
// driver the guard does not sit on. What the guard DOES sit on is the control
// API that mints that URL, so a neon environment already fails at the first
// call. It fails there with a refused connection to console.neon.tech though,
// three minutes into an af up, rather than with a sentence naming the manifest
// line that has to change.
func checkProvider(provider string) error {
	name := strings.ToLower(strings.TrimSpace(provider))
	if name == "" {
		// The orchestrator defaults this to docker and only replaces it when
		// the manifest names one, so empty is not a case that reaches here. It
		// is refused rather than assumed because assuming would make this
		// check silently absent for any caller that forgets to fill the field.
		return fmt.Errorf("this installation is air gapped and the environment names no database provider, " +
			"so there is nothing to check. That is a refusal rather than an assumption, because a " +
			"provider this hook could not see is a provider it could not have refused")
	}
	if _, ok := localProviders[name]; ok {
		return nil
	}
	permitted := make([]string, 0, len(localProviders))
	for p, what := range localProviders {
		permitted = append(permitted, p+" ("+what+")")
	}
	sort.Strings(permitted)
	return fmt.Errorf(
		"this installation is air gapped and the %s database provider reaches a control plane "+
			"outside the operator's network. The providers an air gapped installation can use are "+
			"%s. This list is what is permitted rather than what is not, so a provider added later "+
			"is refused until somebody decides which side of the line it is on",
		name, strings.Join(permitted, ", "))
}

// RegisterFromEnvironment seals this process when the operator asked for it.
//
// Returns false and no error when AF_AIR_GAPPED is unset, which is the ordinary
// case and prints nothing.
//
// Returns an error when the variable is set and the licence does not permit the
// feature. That is the whole reason this returns an error at all: starting
// unsealed would give an operator who asked for an air gap an installation that
// reaches the internet and says nothing, which is precisely the belief this
// feature exists to make impossible. The caller exits.
func RegisterFromEnvironment(ctx context.Context, reg *extension.Registry, getenv func(string) string) (bool, []string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if !truthy(getenv(ModeEnv)) {
		return false, nil, nil
	}
	if !feature.Enabled(ctx, license.FeatureAirGapped) {
		return false, nil, fmt.Errorf(
			"%s is set and this licence does not include air_gapped, so the installation "+
				"would run with the network open while believing it was sealed. It is refused "+
				"instead: either unset %s or install a licence that includes air_gapped",
			ModeEnv, ModeEnv)
	}

	entries := split(getenv(AllowEnv))
	if err := airgap.Allow(entries...); err != nil {
		return false, nil, err
	}
	airgap.Seal(ModeEnv + " is set")
	reg.AddPolicy(Hook{})

	notes := []string{"air gapped: nothing outside the operator's own network is reachable"}
	if allowed := airgap.Allowed(); len(allowed) > 0 {
		notes = append(notes, "air gapped: permitted inside the network: "+strings.Join(allowed, ", "))
	} else {
		notes = append(notes, "air gapped: "+AllowEnv+" is empty, so only this machine is reachable")
	}
	return true, notes, nil
}

// truthy reads the variable the way the rest of this CLI reads a boolean.
//
// An unrecognised value is FALSE for every variable in this repository except
// this one, where it is true. AF_AIR_GAPPED=yes, =on and =enabled are what
// somebody writes when they mean it, and reading those as off would leave an
// installation open while its configuration says it is closed. Only the values
// that unambiguously mean no turn it off.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

func split(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
