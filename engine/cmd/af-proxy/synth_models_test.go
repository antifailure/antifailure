package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The proxy and the runner pick a model from the same two environment variables
// in the same order and fall back to the same two names, in two languages, and
// the control plane refuses any model it has no price for.
//
// Three files, and the coupling between them is invisible in all three.
// costOf in web/apps/api/src/providers/pricing.ts throws for an unpriced model
// and the byok route refuses the request rather than spending unmetered, which
// is the right call and is exactly why the default has to be priced. Changing
// the default on one side is a one word edit that looks local; it lands as
// every request through the proxy being refused, or as the proxy synthesising
// with one model while the runner plans with another.
//
// synthFromEnvironment is called rather than read, so this exercises the real
// selection instead of a copy of it. The TypeScript is read, because there is no
// way to call it from here.

const (
	runnerModelPath = "../../../runner/src/model.ts"
	pricingPath     = "../../../web/apps/api/src/providers/pricing.ts"
)

var (
	// provider: 'anthropic', ... model: env.AF_MODEL ?? 'claude-sonnet-5',
	runnerDefault = regexp.MustCompile(`(?s)provider: '([a-z]+)'.{0,240}?AF_MODEL \?\? '([^']+)'`)
	// 'claude-sonnet-5': { inputPerMillion: 3, ... }
	pricedModel = regexp.MustCompile(`'([^']+)': \{ inputPerMillion:`)
)

// engineDefaults asks the real selector what it picks with only a key set.
func engineDefaults(t *testing.T) map[string]string {
	t.Helper()
	only := func(name string) func(string) string {
		return func(k string) string {
			if k == name {
				return "a-key"
			}
			return ""
		}
	}
	out := map[string]string{}
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		c := synthFromEnvironment(only(key))
		if c == nil {
			t.Fatalf("synthFromEnvironment returned nothing for %s, so the proxy would refuse "+
				"every synth rule on a machine that has that key", key)
		}
		out[c.provider] = c.model
	}
	if len(out) != 2 {
		t.Fatalf("two keys selected %d providers (%v); they are supposed to be distinct", len(out), out)
	}
	return out
}

// runnerDefaults reads the same two fallbacks out of the runner's source.
func runnerDefaults(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(runnerModelPath))
	if err != nil {
		t.Fatalf("read the runner's model selection: %v", err)
	}
	out := map[string]string{}
	for _, m := range runnerDefault.FindAllSubmatch(b, -1) {
		out[string(m[1])] = string(m[2])
	}
	if len(out) == 0 {
		t.Fatalf("%s no longer declares its model defaults in a form this test can read. "+
			"Fix the test rather than deleting it: the drift it guards ends in the control "+
			"plane refusing every request through the proxy", runnerModelPath)
	}
	return out
}

func TestTheProxyAndTheRunnerDefaultToTheSameModels(t *testing.T) {
	engine := engineDefaults(t)
	runner := runnerDefaults(t)
	for provider, model := range engine {
		theirs, ok := runner[provider]
		if !ok {
			t.Errorf("the proxy selects %s and %s has no branch for it, so a machine with only "+
				"that key drives the browser with a different provider from the one the proxy "+
				"synthesises with", provider, runnerModelPath)
			continue
		}
		if theirs != model {
			t.Errorf("with only a %s key the proxy uses %q and the runner uses %q. One run, two "+
				"models, and whichever of them is not in DEFAULT_PRICES is refused outright.",
				provider, model, theirs)
		}
	}
}

func TestEveryDefaultModelHasAPrice(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean(pricingPath))
	if err != nil {
		t.Fatalf("read the control plane's price table: %v", err)
	}
	priced := map[string]bool{}
	for _, m := range pricedModel.FindAllSubmatch(b, -1) {
		priced[string(m[1])] = true
	}
	if len(priced) < 2 {
		t.Fatalf("%s parsed to %d prices, which is too few to be DEFAULT_PRICES; the assertion "+
			"below would be vacuous", pricingPath, len(priced))
	}

	for provider, model := range engineDefaults(t) {
		if !priced[model] {
			t.Errorf("the proxy defaults to %q for %s and DEFAULT_PRICES has no entry for it. "+
				"costOf throws for an unpriced model and the byok route refuses rather than "+
				"spending unmetered, so every request through the proxy on a default "+
				"configuration would be refused.", model, provider)
		}
	}
	for provider, model := range runnerDefaults(t) {
		if !priced[model] {
			t.Errorf("the runner defaults to %q for %s and DEFAULT_PRICES has no entry for it, "+
				"so every page it reads is refused before it is charged", model, provider)
		}
	}
}
