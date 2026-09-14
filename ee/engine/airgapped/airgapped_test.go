// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package airgapped_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/airgapped"
	"github.com/antifailure/antifailure/ee/engine/db/managed"
	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

func licensed(features ...license.Feature) context.Context {
	v := license.NewVerifier(nil)
	status := v.Evaluate(license.Claims{
		ID: "l", Org: "acme", Features: features,
		ExpiresAt: time.Now().AddDate(1, 0, 0),
	}, license.Evaluation{Org: "acme", Now: time.Now()})
	return feature.With(context.Background(), status)
}

func env(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func clean(t *testing.T) {
	t.Helper()
	airgap.Reset()
	t.Cleanup(airgap.Reset)
}

func TestWithoutTheVariableNothingIsSealedAndNothingIsSaid(t *testing.T) {
	clean(t)
	reg := extension.NewRegistry()

	on, notes, err := airgapped.RegisterFromEnvironment(licensed(license.FeatureAirGapped), reg, env(nil))
	require.NoError(t, err)
	require.False(t, on)
	require.Empty(t, notes)
	require.False(t, airgap.Sealed())
}

func TestTheVariableWithoutTheLicenceRefusesToStart(t *testing.T) {
	clean(t)
	reg := extension.NewRegistry()

	_, _, err := airgapped.RegisterFromEnvironment(
		licensed(license.FeatureSSO), reg, env(map[string]string{airgapped.ModeEnv: "1"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "air_gapped")
	require.Contains(t, err.Error(), "believing it was sealed")
	require.False(t, airgap.Sealed(),
		"the one thing that must not happen is starting with the network open "+
			"while the configuration says it is closed")
}

func TestTheVariableWithTheLicenceSealsTheProcess(t *testing.T) {
	clean(t)
	reg := extension.NewRegistry()

	on, notes, err := airgapped.RegisterFromEnvironment(
		licensed(license.FeatureAirGapped), reg,
		env(map[string]string{
			airgapped.ModeEnv:  "1",
			airgapped.AllowEnv: "registry.internal:5000, 10.4.0.0/16",
		}))
	require.NoError(t, err)
	require.True(t, on)
	require.True(t, airgap.Sealed())
	require.Equal(t, []string{"10.4.0.0/16", "registry.internal:5000"}, airgap.Allowed())
	require.Contains(t, strings.Join(notes, "\n"), "registry.internal:5000")

	require.ErrorIs(t, airgap.Check(airgap.SiteReleaseCheck, "tcp", "api.github.com:443"),
		airgap.ErrSealed)
	require.NoError(t, airgap.Check(airgap.SiteImagePull, "tcp", "registry.internal:5000"))
}

func TestAnEmptyAllowListSaysSoRatherThanLookingConfigured(t *testing.T) {
	clean(t)
	_, notes, err := airgapped.RegisterFromEnvironment(
		licensed(license.FeatureAirGapped), extension.NewRegistry(),
		env(map[string]string{airgapped.ModeEnv: "1"}))
	require.NoError(t, err)
	require.Contains(t, strings.Join(notes, "\n"), "only this machine is reachable")
}

func TestAnAllowListWithATypoStopsTheBinary(t *testing.T) {
	clean(t)
	_, _, err := airgapped.RegisterFromEnvironment(
		licensed(license.FeatureAirGapped), extension.NewRegistry(),
		env(map[string]string{
			airgapped.ModeEnv:  "1",
			airgapped.AllowEnv: "https://registry.internal/v2/",
		}))
	require.Error(t, err)
	require.False(t, airgap.Sealed())
}

func TestTheWordsThatMeanYesAreReadAsYes(t *testing.T) {
	for _, value := range []string{"1", "true", "yes", "on", "enabled", "TRUE", " yes "} {
		clean(t)
		on, _, err := airgapped.RegisterFromEnvironment(
			licensed(license.FeatureAirGapped), extension.NewRegistry(),
			env(map[string]string{airgapped.ModeEnv: value}))
		require.NoErrorf(t, err, "%q", value)
		require.Truef(t, on, "%q left the installation open while its configuration asked for an air gap", value)
	}
	for _, value := range []string{"", "0", "false", "no", "off"} {
		clean(t)
		on, _, err := airgapped.RegisterFromEnvironment(
			licensed(license.FeatureAirGapped), extension.NewRegistry(),
			env(map[string]string{airgapped.ModeEnv: value}))
		require.NoErrorf(t, err, "%q", value)
		require.Falsef(t, on, "%q", value)
	}
}

func TestTheHookRefusesEveryEgressModeThatWouldLeaveTheNetwork(t *testing.T) {
	clean(t)
	airgap.Seal("a test")
	ctx := licensed(license.FeatureAirGapped)

	for mode, why := range map[string]string{
		"allow":   "forwards the request to the real host",
		"sandbox": "still forwards to the real host",
		"synth":   "asks a model provider",
	} {
		err := airgapped.Hook{}.Check(ctx, extension.EnvironmentRequest{
			Provider:    "docker",
			EgressHosts: []string{"api.stripe.com"},
			EgressModes: map[string]string{"api.stripe.com": mode},
		})
		require.Errorf(t, err, "%s mode reaches the internet and was permitted", mode)
		require.Containsf(t, err.Error(), "api.stripe.com", "%s", mode)
		require.Containsf(t, err.Error(), mode, "%s", mode)
		require.NotEmpty(t, why)
	}
}

func TestTheHookRefusesAManifestWithNoRulesAndAnOpenDefault(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	// The shape that would have got past a hook reading only the rules. It is
	// a valid manifest, the validator only warns about it, and it reaches the
	// whole internet.
	err := airgapped.Hook{}.Check(licensed(license.FeatureAirGapped), extension.EnvironmentRequest{
		Provider:      "docker",
		EgressDefault: "allow",
	})
	require.Error(t, err, "egress default allow with no rules reaches everything")
	require.Contains(t, err.Error(), "every host no rule names")
	require.Contains(t, err.Error(), "1 egress rule ")
}

func TestTheHookLeavesADefaultThatReachesNothing(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	for _, mode := range []string{"", "block", "capture", "mock"} {
		require.NoErrorf(t, airgapped.Hook{}.Check(
			licensed(license.FeatureAirGapped),
			extension.EnvironmentRequest{Provider: "docker", EgressDefault: mode}),
			"default %q is answered inside the environment", mode)
	}
}

func TestTheHookLeavesTheModesThatReachNothing(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	err := airgapped.Hook{}.Check(licensed(license.FeatureAirGapped), extension.EnvironmentRequest{
		Provider: "docker",
		EgressModes: map[string]string{
			"api.stripe.com": "capture",
			"api.openai.com": "mock",
			"example.com":    "block",
		},
	})
	require.NoError(t, err,
		"an air gapped installation is supposed to be able to run an application "+
			"whose third party calls are captured, and refusing those would make "+
			"the mode useless rather than strict")
}

func TestTheHookNamesEveryOffendingRuleRatherThanTheFirst(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	err := airgapped.Hook{}.Check(licensed(license.FeatureAirGapped), extension.EnvironmentRequest{
		Provider: "docker",
		EgressModes: map[string]string{
			"api.stripe.com":   "allow",
			"api.openai.com":   "synth",
			"s3.amazonaws.com": "sandbox",
			"logs.internal":    "capture",
		},
	})
	require.Error(t, err)
	for _, host := range []string{"api.stripe.com", "api.openai.com", "s3.amazonaws.com"} {
		require.Containsf(t, err.Error(), host, "an operator fixing this needs the whole list, not the first one")
	}
	require.NotContains(t, err.Error(), "logs.internal")
	require.Contains(t, err.Error(), "3 egress rules")
}

func TestTheHookDoesNothingWhenTheProcessIsNotSealed(t *testing.T) {
	clean(t)
	require.NoError(t, airgapped.Hook{}.Check(context.Background(), extension.EnvironmentRequest{
		Provider:    "docker",
		EgressModes: map[string]string{"api.stripe.com": "allow"},
	}))
}

func TestTheHookKeepsRefusingWhenTheLicenceLapsesUnderIt(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	// The opposite of every other licence check in this product, on purpose.
	// An installation deployed behind an air gap must not start creating
	// environments that reach the internet because a purchase order was slow.
	err := airgapped.Hook{}.Check(context.Background(), extension.EnvironmentRequest{
		Provider:    "docker",
		EgressModes: map[string]string{"api.stripe.com": "allow"},
	})
	require.Error(t, err)
}

func TestTheFeatureNowHasADeclaredEnforcementSite(t *testing.T) {
	sites := feature.Sites(license.FeatureAirGapped)
	require.NotEmpty(t, sites,
		"air_gapped was one of nine features the licence sold and nothing enforced")
	// path:symbol, relative to ee/engine, which is what feature.SplitSite
	// parses and what the entitlement checks in ee/engine/cmd/af open. The
	// earlier spelling was a dotted package qualifier: it reads the same to a
	// person and names no file, so nothing could confirm it.
	require.Contains(t, sites, "airgapped/airgapped.go:RegisterFromEnvironment")
	require.Contains(t, sites, "airgapped/airgapped.go:Hook.Check")
}

func TestTheHookIsRegisteredSoTheEngineActuallyConsultsIt(t *testing.T) {
	clean(t)
	reg := extension.NewRegistry()
	_, _, err := airgapped.RegisterFromEnvironment(
		licensed(license.FeatureAirGapped), reg,
		env(map[string]string{airgapped.ModeEnv: "1"}))
	require.NoError(t, err)

	// Defined, wired, effective. A hook constructed and never added to the
	// registry is the exact shape of the gap this whole lane exists to close.
	err = reg.CheckPolicy(context.Background(), extension.EnvironmentRequest{
		Provider:    "docker",
		EgressModes: map[string]string{"api.stripe.com": "allow"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "air gapped")
}

func TestTheLicenceItselfCannotPhoneHome(t *testing.T) {
	t.Parallel()
	// The package comment on ee/engine/license says verification is offline:
	// no network call, nothing that can fail at three in the morning because a
	// licensing service is down. That is the single most load bearing sentence
	// in an air gapped installation, and until now the only thing holding it
	// was the sentence.
	//
	// Checked as an import, because that is the level at which the claim is
	// absolute. A package that cannot name net/http, net or crypto/tls cannot
	// open a connection, whatever any future function in it does, and no
	// reviewer has to notice.
	forbidden := map[string]bool{"net": true, "net/http": true, "crypto/tls": true}

	entries, err := os.ReadDir("../license")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var found []string
	files := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		file, err := parser.ParseFile(fset, filepath.Join("../license", name), nil, parser.ImportsOnly)
		require.NoErrorf(t, err, "%s could not be parsed, so it was NOT checked", name)
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if forbidden[path] {
				found = append(found, name+" imports "+path)
			}
		}
	}
	require.Positive(t, files, "no files were read, so NOTHING was checked")
	require.Emptyf(t, found, "licence verification is offline and %d of its files can dial: %v",
		len(found), found)
}

// builtinDatabaseProviders reads the names the engine's own switch answers to
// from the DBProvider constants in engine/pkg/schema, which is where the
// engine takes them from. Parsed rather than written out, so a built in
// provider added to the engine is classified here without anybody editing
// this file.
func builtinDatabaseProviders(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("..", "..", "..", "engine", "pkg", "schema", "manifest.go"), nil, 0)
	require.NoError(t, err)
	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		typ, ok := spec.Type.(*ast.Ident)
		if !ok || typ.Name != "DBProvider" {
			return true
		}
		for _, v := range spec.Values {
			if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				name, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				names = append(names, name)
			}
		}
		return true
	})
	return names
}

// registeredDatabaseProviders is what the enterprise binary registers, read by
// calling the function main.go calls.
func registeredDatabaseProviders() []string {
	reg := extension.NewRegistry()
	managed.Register(reg)
	return reg.DatabaseProviderNames()
}

// The largest outbound path the process guard cannot see. A Postgres
// connection goes wherever its URL points, and a managed branch's URL points at
// somebody else's cloud, through a driver no dialer here sits on. So in a sealed
// installation the hook has to refuse every provider whose control plane is not
// the operator's own.
//
// This ENUMERATES rather than naming providers, because the list it replaced
// named aurora and cloudsql and not azurepg, and nothing noticed. Both sides are
// asserted. Every provider that arrives through the registry is a managed cloud
// provider, which is how main.go and cloudgate define cloud, and each one must be
// refused by name. Every built in provider must land on the side this test
// states, and a built in provider on neither side fails until somebody decides.
func TestTheHookRefusesADatabaseProviderWhoseControlPlaneIsSomebodyElses(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	builtin := builtinDatabaseProviders(t)
	registered := registeredDatabaseProviders()

	// The enumeration is a control before it is a source of cases: a parse
	// that found nothing, or a registration that registered nothing, would
	// make every loop below pass by iterating over an empty list.
	require.Subsetf(t, builtin, []string{"docker", "neon", "supabase", "dblab", "pgurl"},
		"the schema constants read as %v, which is missing providers the engine is known to "+
			"build, so the enumeration is broken and this test would check less than it says", builtin)
	require.Subsetf(t, registered, []string{"aurora", "cloudsql", "azurepg"},
		"managed.Register registered %v, which is missing providers this edition is known to "+
			"ship, so the enumeration is broken and this test would check less than it says", registered)

	operatorHosted := map[string]bool{"docker": true, "dblab": true, "pgurl": true}
	somebodyElses := map[string]bool{"neon": true, "supabase": true, "xata": true}

	refused := func(provider string) {
		err := airgapped.Hook{}.Check(licensed(license.FeatureAirGapped),
			extension.EnvironmentRequest{Provider: provider})
		require.Errorf(t, err, "%s reaches a control plane outside the network and a sealed "+
			"installation accepted it", provider)
		require.Containsf(t, err.Error(), "air gapped", "%s", provider)
		require.Containsf(t, err.Error(), strings.ToLower(provider), "the refusal for %s does not name it", provider)
	}

	for _, provider := range registered {
		require.Falsef(t, operatorHosted[provider] || somebodyElses[provider],
			"%s is registered through the extension registry and is also a built in name", provider)
		refused(provider)
	}
	for _, provider := range builtin {
		switch {
		case operatorHosted[provider]:
			require.NoErrorf(t, airgapped.Hook{}.Check(licensed(license.FeatureAirGapped),
				extension.EnvironmentRequest{Provider: provider}),
				"%s is the operator's own and refusing it would make the mode unusable", provider)
		case somebodyElses[provider]:
			refused(provider)
		default:
			t.Errorf("%s is a built in database provider this test does not classify. Decide "+
				"whether its control plane is the operator's own, and put it on that side here "+
				"and in localProviders", provider)
		}
	}

	// Case is not a way around it.
	refused("Neon")
}

func TestTheHookPermitsADatabaseTheOperatorHosts(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	for _, provider := range []string{"docker", "dblab", "pgurl", "DOCKER"} {
		require.NoErrorf(t, airgapped.Hook{}.Check(licensed(license.FeatureAirGapped),
			extension.EnvironmentRequest{Provider: provider}),
			"%s is the operator's own and refusing it would make the mode unusable", provider)
	}
}

func TestAProviderTheHookCannotSeeIsRefusedRatherThanAssumed(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	// The orchestrator defaults this to docker and only replaces it when the
	// manifest names one, so an empty provider does not reach here in
	// production. It is refused rather than assumed because a provider this
	// hook could not see is a provider it could not have refused, and a caller
	// that forgot the field would otherwise switch the check off in silence.
	err := airgapped.Hook{}.Check(licensed(license.FeatureAirGapped),
		extension.EnvironmentRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "names no database provider")
}

func TestTheDatabaseIsReportedBeforeTheEgressRules(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	// Both wrong at once. CheckPolicy returns the first refusal by design, and
	// the database is the larger problem and a different line of the manifest,
	// so it is the one a reader gets first.
	err := airgapped.Hook{}.Check(licensed(license.FeatureAirGapped),
		extension.EnvironmentRequest{
			Provider:    "neon",
			EgressModes: map[string]string{"api.stripe.com": "allow"},
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "neon")
	require.NotContains(t, err.Error(), "api.stripe.com")
}
