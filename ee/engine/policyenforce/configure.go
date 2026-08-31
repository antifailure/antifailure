package policyenforce

// Reading the policy an installation has actually configured, and plugging it in.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// This file exists because the hook next door did not have one, and the gap it
// left is the reason to read the rest of this comment. `policyenforce.Hook` was
// written, tested to a hundred percent, and property tested over five hundred
// random policies, and no binary ever constructed one. An organization that
// bought policy_enforcement got a licence that granted a feature, a compliance
// control that reported "no organization policy is configured", and no refusal
// on any environment ever. Every part of it looked finished except the one that
// decides whether it does anything.
//
// The policy is a file rather than a row in the control plane, and that is a
// decision worth defending rather than an accident. The control plane has no
// policy table (see ee/engine/compliance/postgres.go, which says so where it
// would read one), and the engine runs inside the customer's CI, where the
// control plane is frequently not reachable at all. A file is reviewable, is
// versioned by whatever holds it, and can be mounted by a runner. When the
// control plane grows a table, it becomes a second source rather than a
// replacement, and Stricter is already here to combine the two in the only
// direction that is safe.
//
// The rule about failure is the same one AF_SECRET_SOURCES follows, and it is
// sharper here. Somebody who sets AF_ORG_POLICY_FILE has said that environments
// must be checked against it. Starting anyway with the file unread means every
// environment is created without being checked, and nothing in the output says
// so: the engine behaves exactly as the community edition does, which is the
// behaviour somebody paid to stop. So an unreadable, unparseable, or
// unknown-field policy stops the process with the reason. An unset variable
// registers nothing, which is the ordinary case and prints nothing.

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// PolicyEnv names the variable holding the path to the policy document.
const PolicyEnv = "AF_ORG_POLICY_FILE"

// FromEnvironment reads the policy named by AF_ORG_POLICY_FILE.
//
// Returns a nil hook and a nil error when the variable is unset, because an
// installation with no organization policy is the ordinary one and is not an
// error. Every other outcome is an error: see the file comment for why a policy
// that cannot be read must not degrade to no policy.
func FromEnvironment(getenv func(string) string) (*Hook, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	path := strings.TrimSpace(getenv(PolicyEnv))
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s names %s and it cannot be read: %w", PolicyEnv, path, err)
	}
	policy, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s names %s and it cannot be used: %w", PolicyEnv, path, err)
	}
	// The approval lookup is nil, which means nothing is ever approved. That is
	// the safe direction and it is also the honest one: approvals live in the
	// control plane, this binary reads a file, and a lookup that returned
	// "approved" because it had nowhere to ask would turn the synth rule into
	// decoration.
	return NewHook(policy, nil), nil
}

// Parse reads a policy document.
//
// Unknown fields are refused. A document that said denied_host instead of
// denied_hosts would otherwise parse into an empty policy and refuse nothing,
// and the administrator who wrote it would have no way to tell that from a
// policy nobody violated.
func Parse(data []byte) (Policy, error) {
	var policy Policy
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&policy); err != nil {
		return Policy{}, err
	}

	// Refused rather than accepted, because nothing enforces it. There is no
	// reaper in the engine: runtime.ttl and runtime.idle_sleep are normalized,
	// validated, printed by af explain, and read by nothing, and a policy field
	// that joined them would be a bound an auditor could point at while every
	// environment outlived it. Accepting a key that behaves as decoration is
	// the defect this whole file exists to close, so it is refused here rather
	// than reproduced one level up.
	if policy.MaxLifetimeHours != 0 {
		return Policy{}, fmt.Errorf(
			"max_lifetime_hours is set to %d and nothing enforces it: the engine has no reaper, "+
				"so an environment would outlive the bound and the policy would report as held. "+
				"Remove the key until a reaper exists", policy.MaxLifetimeHours)
	}
	return policy, nil
}

// Rules names what the policy restricts, for a startup line and for af doctor.
//
// Worth printing for the same reason Registry.Registered is: an operator asking
// why an environment was refused, or why one was not, needs to know which rules
// are in force before anything else.
func Rules(h *Hook) []string {
	if h == nil {
		return nil
	}
	var out []string
	if n := len(h.policy.RequiredMaskedColumns); n > 0 {
		out = append(out, fmt.Sprintf("required masking (%s)",
			strings.Join(h.policy.RequiredMaskedColumns, ", ")))
	}
	if n := len(h.policy.DeniedHosts); n > 0 {
		out = append(out, fmt.Sprintf("egress deny list (%d hosts)", n))
	}
	if len(h.policy.AllowedModes) > 0 {
		out = append(out, "allowed egress modes ("+strings.Join(h.policy.AllowedModes, ", ")+")")
	}
	if h.policy.SynthRequiresApproval {
		out = append(out, "synth requires approval")
	}
	if len(h.policy.AllowedProviders) > 0 {
		out = append(out, "allowed database providers ("+
			strings.Join(h.policy.AllowedProviders, ", ")+")")
	}
	if len(h.policy.AllowedRegions) > 0 {
		out = append(out, "data residency ("+strings.Join(h.policy.AllowedRegions, ", ")+")")
	}
	sort.Strings(out)
	return out
}

// RegisterFromEnvironment reads the configured policy and plugs it in.
//
// The one call an embedding binary makes. It returns the hook so the binary can
// print which rules are in force, and so that a binary that forgot to print
// them still cannot forget to register them: there is no way to get the hook
// out of here without it already being in the registry.
func RegisterFromEnvironment(reg *extension.Registry, getenv func(string) string) (*Hook, error) {
	hook, err := FromEnvironment(getenv)
	if err != nil || hook == nil {
		return nil, err
	}
	reg.AddPolicy(hook)
	return hook, nil
}
