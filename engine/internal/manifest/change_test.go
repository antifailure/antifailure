package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A project teaching the classifier its own layout is the normal case for a
// monorepo, so the accepted form is tested before the refused ones.
func TestParse_AcceptsChangeRules(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`
change:
  rules:
    - path: packages/*/src/**
      surface: code
    - path: ops/**
      surface: infrastructure
      note: the deployment scripts, which no environment runs
`)
	require.NotNil(t, m.Change)
	require.Len(t, m.Change.Rules, 2)
	assert.Equal(t, "packages/*/src/**", m.Change.Rules[0].Path)
	assert.Equal(t, "code", m.Change.Rules[0].Surface)
	assert.Equal(t, "the deployment scripts, which no environment runs", m.Change.Rules[1].Note)
}

// The refusal that matters. An unrecognised path selects every check, which is
// the fail safe the whole analysis rests on. A rule matching every path would
// classify everything and that fail safe would never fire again, so the
// manifest refuses to let anybody write one.
func TestParse_RefusesAChangeRuleThatMatchesEveryPath(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{"**", "*", "**/*", "./**"} {
		_, err := parse(t, minimal+`
change:
  rules:
    - path: "`+pattern+`"
      surface: docs
`)
		ps := problems(t, err)
		assert.Containsf(t, messages(ps), "matches every path",
			"the pattern %q was accepted, and it defeats the fail safe", pattern)
		assert.Contains(t, messages(ps), "change.rules[0].path")
	}
}

func TestParse_RefusesASurfaceTheEngineDoesNotKnow(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`
change:
  rules:
    - path: ops/**
      surface: infra
`)
	ps := problems(t, err)
	assert.Contains(t, messages(ps), `The surface "infra" is not one this engine knows`)
	assert.Contains(t, messages(ps), "infrastructure",
		"the hint has to name the spellings that work, or the reader guesses again")
}

// A service and the masking rules file are derived from declarations already
// in this file. A second way to say them would be a second answer to
// disagree with the first.
func TestParse_RefusesASurfaceOnlyTheManifestCanAssign(t *testing.T) {
	t.Parallel()
	for _, surface := range []string{"service", "manifest", "masking", "egress", "unknown"} {
		_, err := parse(t, minimal+`
change:
  rules:
    - path: ops/**
      surface: `+surface+`
`)
		ps := problems(t, err)
		assert.Containsf(t, messages(ps), "is not one this engine knows",
			"the surface %q was accepted by hand", surface)
	}
}

func TestParse_RefusesTwoChangeRulesWithTheSamePattern(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`
change:
  rules:
    - path: ops/**
      surface: infrastructure
    - path: ops/**
      surface: docs
`)
	assert.Contains(t, messages(problems(t, err)), "already declared at change.rules[0]")
}

func TestParse_RefusesAChangeRuleWithNoPattern(t *testing.T) {
	t.Parallel()
	_, err := parse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
change:
  rules:
    - path: ""
      surface: docs
`)
	assert.Contains(t, messages(problems(t, err)), "has no path pattern")
}
