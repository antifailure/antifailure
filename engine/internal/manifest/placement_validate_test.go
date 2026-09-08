package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The rules that refuse a placement nothing could ever satisfy.
//
// Every one of them is decidable from the manifest alone: the requirement and
// the targets are in the same file, so the contradiction is visible before
// anything is dispatched and the author is looking at both lines while they fix
// it. The alternative is a scheduler in a cluster whose only honest answer is
// that nothing satisfies the requirement, reported to somebody who is not the
// person who wrote it.

func TestParse_RefusesAPlacementRequirementWithNoTargetsToPlaceOn(t *testing.T) {
	t.Parallel()
	body := minimal + `
runtime:
  requires:
    region: eu-west-1
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "no targets to place on")
	require.Contains(t, msg, "runtime.targets")
}

func TestParse_RefusesARequirementNoTargetSatisfiesAndSaysWhatIsOffered(t *testing.T) {
	t.Parallel()
	body := minimal + `
runtime:
  provider: local
  requires:
    region: eu-west-2
  targets:
    - name: us
      tags:
        region: us-east-1
    - name: eu
      tags:
        region: eu-west-1
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "No placement target satisfies region=eu-west-2")
	// What the targets DO offer, because "nothing satisfies it" leaves the
	// author to go and read six target blocks to find out what they could have
	// asked for instead.
	require.Contains(t, msg, "eu-west-1")
	require.Contains(t, msg, "us-east-1")
}

func TestParse_SaysNoTargetCarriesTheTagWhenNoneDoes(t *testing.T) {
	t.Parallel()
	// A different sentence from the one above on purpose. Asking for a tag
	// nobody declares is a typo in the key; asking for a value nobody offers is
	// a typo in the value, and the fixes are different.
	body := minimal + `
runtime:
  provider: local
  requires:
    isolation: strict
  targets:
    - name: us
      tags:
        region: us-east-1
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "No placement target satisfies isolation=strict")
	require.Contains(t, msg, "no target declares that tag at all")
}

func TestParse_RefusesTwoTargetsWithOneName(t *testing.T) {
	t.Parallel()
	body := minimal + `
runtime:
  provider: local
  targets:
    - name: eu
      tags:
        region: eu-west-1
    - name: eu
      tags:
        region: eu-central-1
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `Two placement targets are called "eu"`)
}

func TestParse_RefusesTwoKubernetesTargetsOnOneCluster(t *testing.T) {
	t.Parallel()
	// The mistake a fleet written by copy and paste makes. Both targets inherit
	// the runtime block's context, so both resolve to the same cluster, and a
	// placement decision between them decides nothing while looking like it
	// decided something.
	body := minimal + `
runtime:
  provider: kubernetes
  domain: preview.example.com
  targets:
    - name: eu
      tags:
        region: eu-west-1
    - name: us
      tags:
        region: us-east-1
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "both resolve to whichever kubeconfig context is current")
}

func TestParse_RefusesTwoKubernetesTargetsNamingOneContext(t *testing.T) {
	t.Parallel()
	body := minimal + `
runtime:
  provider: kubernetes
  domain: preview.example.com
  targets:
    - name: eu
      kubeconfig_context: prod
      tags:
        region: eu-west-1
    - name: us
      kubeconfig_context: prod
      tags:
        region: us-east-1
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `both resolve to kubeconfig context "prod"`)
}

func TestParse_RefusesAKubernetesTargetStillOnLocalhost(t *testing.T) {
	t.Parallel()
	// The same rule the unplaced runtime block already had, applied per target,
	// because a target overrides the domain and a fleet where one cluster was
	// left on localhost has one cluster whose preview URLs do not resolve.
	body := minimal + `
runtime:
  provider: kubernetes
  domain: preview.example.com
  targets:
    - name: eu
      kubeconfig_context: eu
      domain: eu.preview.example.com
      tags:
        region: eu-west-1
    - name: us
      kubeconfig_context: us
      domain: localhost
      tags:
        region: us-east-1
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `Target "us" is Kubernetes and its domain is still localhost`)
	require.NotContains(t, msg, `Target "eu" is Kubernetes`)
}

func TestParse_RefusesATargetWithNoName(t *testing.T) {
	t.Parallel()
	body := minimal + `
runtime:
  provider: local
  targets:
    - tags:
        region: eu-west-1
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "A placement target has no name")
}

func TestParse_AcceptsAFleetAndResolvesWhatEachTargetInherits(t *testing.T) {
	t.Parallel()
	// The control that makes every refusal above mean something. A rule keyed
	// on the wrong thing would refuse this too, and a validator that says no to
	// everything says nothing.
	m := mustParse(t, minimal+`
runtime:
  provider: kubernetes
  domain: preview.example.com
  namespace_prefix: shop
  requires:
    region: eu-west-1
  targets:
    - name: eu
      kubeconfig_context: eu-prod
      domain: eu.preview.example.com
      tags:
        region: eu-west-1
    - name: us
      kubeconfig_context: us-prod
      tags:
        region: us-east-1
`)
	require.Len(t, m.Runtime.Targets, 2)

	// Written, so unchanged.
	require.Equal(t, "eu.preview.example.com", m.Runtime.Targets[0].Domain)
	// Not written, so inherited from the runtime block rather than left empty.
	// An empty domain would put a Kubernetes environment behind a hostname with
	// nothing in front of the dot.
	require.Equal(t, "preview.example.com", m.Runtime.Targets[1].Domain)
	require.Equal(t, schema.RuntimeKubernetes, m.Runtime.Targets[1].Provider)
	require.Equal(t, "shop", m.Runtime.Targets[1].NamespacePrefix)
}

func TestParse_AcceptsOneTargetAsALabelOnTheRuntimeThatIsAlreadyThere(t *testing.T) {
	t.Parallel()
	// One target is not a fleet. It says where the single runtime is, which is
	// what an organization residency policy reads, and it has to parse without
	// a requirement and without a second target to compare against.
	m := mustParse(t, minimal+`
runtime:
  provider: local
  targets:
    - name: laptop
      tags:
        region: eu-west-1
`)
	require.Equal(t, "eu-west-1", m.Runtime.Targets[0].Tags[schema.RegionTag])
}

func TestParse_AcceptsAManifestWithNoPlacementAtAll(t *testing.T) {
	t.Parallel()
	// Every manifest that existed before placement did. Normalization must not
	// invent a target, because a manifest carrying one target would go down the
	// placement path and a manifest carrying two would meet a licence gate,
	// and neither is what any of them asked for.
	m := mustParse(t, minimal)
	require.Empty(t, m.Runtime.Targets)
	require.Empty(t, m.Runtime.Requires)
}
