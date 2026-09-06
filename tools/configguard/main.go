// Command configguard decides whether a targeted Terraform plan of the control
// plane's container app is a configuration change and nothing else.
//
// THE FAILURE THIS EXISTS FOR. On the night of 2026-09-05 three configuration
// changes had to reach production: AF_POSTHOG_REGION and
// AF_POSTHOG_PROJECT_KEY, then the three Stripe variables with their two Key
// Vault references. Each was a line in production.tfvars, merged and released,
// and each reached the running container only because a person sat at a
// terminal and ran a targeted `terraform apply` behind a one-off guard script,
// then shifted traffic by hand. deploy/cd/deploy.sh only ever runs
// `az containerapp update --image`, so it carries the template it finds and
// sets no variable of its own, and cd.yml ran no Terraform at all. Meanwhile
// the marketing site had gone live pointing at /ph, which answered 404 for an
// hour because the variable that turns the proxy on was in a file and not in
// the app.
//
// deploy/cd/apply-config.sh is the answer: cd.yml plans the container app from
// the environment's tfvars, targeted at that one resource, and applies. This
// program is the part of it that is allowed to say no, and it is the part that
// makes an unattended apply defensible. A plan is applied by the job only when
// it is EXACTLY one of:
//
//   - no changes at all, which is the steady state after every deploy, or
//   - one in-place update of the targeted container app whose only
//     differences are the container's environment list and the app's secret
//     reference list, with the serving image, the ingress traffic weights and
//     every other attribute identical before and after.
//
// Everything else is refused with the reason and the plan's summary, so the
// job log says what the plan wanted to do and a person decides. A create is
// refused: it means the app does not exist and the runbook applies, not this.
// A destroy or a replace is refused: a replace of a Multiple revision container
// app is an outage. A change to any second resource is refused, whatever it is,
// because the targeted plan can only carry the app's dependencies and a
// dependency changing is not a configuration change. An image change is
// refused because deploy.sh owns the image, and the image is in
// ignore_changes precisely so that Terraform never moves it. A traffic weight
// change is refused because deploy.sh owns the traffic too.
//
// WHY THE COMPARISON IS "EVERYTHING EXCEPT ENV AND SECRETS" RATHER THAN A LIST
// OF ALLOWED ATTRIBUTES. The one-off guard used on the night checked the image,
// the traffic weights and the env names it had been told to expect, and that
// was right for one reviewed apply. A standing gate cannot carry a list of
// expected names, and a standing gate that checks three attributes passes a
// change to the fourth. So the before and after objects are compared whole,
// with the two allowed lists removed and with every attribute Terraform says
// will only be known after apply removed from both sides. Anything left that
// differs is named by its path and refused.
//
// WHAT IT DELIBERATELY DOES NOT READ. The `variables` block of the plan
// document carries the values of sensitive inputs, and the container app's
// secret entries carry a `value` attribute. This tool reads names only and
// prints names only. The env value of a variable is printed nowhere either:
// the names say what changed and the tfvars diff on main says what it changed
// to.
//
// IT REFUSES TO PASS A PLAN THAT COULD NOT HAVE FAILED, for the same reason
// tools/planguard does. A plan made without the real state reads the app as a
// create, and that is refused as a create rather than passed as a no-op. A
// document this tool cannot parse is an error and never an empty change set.
//
// EXIT CODES, because the script that calls this branches on them:
//
//	0  exactly one acceptable update; apply it
//	3  no changes at all; nothing to apply
//	1  refused, or the plan could not be read
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
)

// defaultTarget is the resource deploy/cd/apply-config.sh plans. It is a flag
// so a test can point the guard at a differently named app, and so a future
// stack that renames the module does not silently turn every plan into "a
// change to a second resource".
const defaultTarget = "module.control_plane.azurerm_container_app.this"

// exitNoChanges is the exit status for a plan with nothing in it. It is not
// zero, because the caller has to tell "apply this" from "there is nothing to
// apply" without parsing prose, and it is not one, because it is not a
// refusal.
const exitNoChanges = 3

// planFile is the subset of `terraform show -json` this tool reads. Notably
// absent: `variables`, which carries sensitive input values.
type planFile struct {
	FormatVersion   string           `json:"format_version"`
	Errored         bool             `json:"errored"`
	ResourceChanges []resourceChange `json:"resource_changes"`
	PriorState      *struct {
		Values *struct {
			RootModule module `json:"root_module"`
		} `json:"values"`
	} `json:"prior_state"`
}

type module struct {
	Resources []struct {
		Mode string `json:"mode"`
	} `json:"resources"`
	ChildModules []module `json:"child_modules"`
}

// managed counts the managed resources the prior state knows about, at any
// depth. tools/planguard learned both halves of that from a real plan: the
// control plane keeps almost everything inside module.control_plane, and a
// plan made with no state still records data sources in prior_state.
func (m module) managed() int {
	n := 0
	for _, r := range m.Resources {
		if r.Mode == "managed" {
			n++
		}
	}
	for _, c := range m.ChildModules {
		n += c.managed()
	}
	return n
}

type resourceChange struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Mode    string `json:"mode"`
	Change  struct {
		Actions      []string        `json:"actions"`
		Before       json.RawMessage `json:"before"`
		After        json.RawMessage `json:"after"`
		AfterUnknown json.RawMessage `json:"after_unknown"`
	} `json:"change"`
}

func (c resourceChange) actions() string { return strings.Join(c.Change.Actions, ",") }

func (c resourceChange) isNoop() bool {
	return len(c.Change.Actions) == 1 && c.Change.Actions[0] == "no-op"
}

// verdict is what the guard decided, in a shape the tests can assert on
// without parsing the message.
type verdict struct {
	// NoChanges is true when every managed resource in the plan is a no-op.
	NoChanges bool
	// Summary is the accepted change, names only.
	Summary string
}

func main() {
	plan := flag.String("plan", "", "Path to the output of `terraform show -json <planfile>`.")
	target := flag.String("target", defaultTarget, "The one resource address that may change.")
	label := flag.String("environment", "", "Which environment this plan is for, used only in messages.")
	flag.Parse()

	v, err := run(*plan, *target, *label)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configguard: REFUSED: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(v.Summary)
	if v.NoChanges {
		os.Exit(exitNoChanges)
	}
}

func run(planPath, target, label string) (verdict, error) {
	if planPath == "" {
		return verdict{}, errors.New("-plan is required")
	}
	where := "this plan"
	if label != "" {
		where = label
	}

	p, err := readPlan(planPath)
	if err != nil {
		return verdict{}, err
	}
	if p.Errored {
		return verdict{}, fmt.Errorf("%s: terraform reports the plan itself errored, so there is nothing here to trust", where)
	}

	// A plan without real state reads the app as a create. That is refused
	// below as a create, but it is refused here first with the reason that
	// matters: the check would otherwise be passing a document that examined
	// nothing.
	if p.PriorState == nil || p.PriorState.Values == nil || p.PriorState.Values.RootModule.managed() == 0 {
		return verdict{}, fmt.Errorf(`%s has NO PRIOR STATE, so every resource in it reads as a create and nothing in it can be a configuration change.

A plan made without the state backend is not a plan of the running app. Point
this at a plan made against the environment's real state.

  plan: %s`, where, planPath)
	}

	// Rule one: every managed resource other than the target is a no-op, and
	// the target is either a no-op or a single in-place update. Data sources
	// are reads and are not changes.
	var others []resourceChange
	var targetChange *resourceChange
	for i := range p.ResourceChanges {
		c := p.ResourceChanges[i]
		if c.Mode != "managed" {
			continue
		}
		if c.Address == target {
			targetChange = &p.ResourceChanges[i]
			continue
		}
		if !c.isNoop() {
			others = append(others, c)
		}
	}
	if len(others) > 0 {
		sort.Slice(others, func(i, j int) bool { return others[i].Address < others[j].Address })
		var b strings.Builder
		fmt.Fprintf(&b, "%s changes %d resource(s) other than %s, and a configuration apply may touch nothing else:\n", where, len(others), target)
		for _, c := range others {
			fmt.Fprintf(&b, "  %s  (%s, actions: %s)\n", c.Address, c.Type, c.actions())
		}
		b.WriteString(planSummary(p))
		return verdict{}, errors.New(b.String())
	}

	if targetChange == nil {
		return verdict{}, fmt.Errorf("%s does not mention %s at all. A targeted plan of the app always carries the app, so this plan was made without the target or against a stack that does not declare it.%s", where, target, planSummary(p))
	}

	if targetChange.isNoop() {
		return verdict{
			NoChanges: true,
			Summary:   fmt.Sprintf("configguard: %s: no configuration change. %s is exactly what the tfvars describe and nothing else in the plan moves.", where, target),
		}, nil
	}

	if targetChange.actions() != "update" {
		return verdict{}, fmt.Errorf("%s would %s %s, and only an in-place update is a configuration change. A create means the app does not exist and the production runbook applies; a replace or a destroy of a Multiple revision app is an outage.%s",
			where, describe(targetChange.Change.Actions), target, planSummary(p))
	}

	// Rule two: with env and secrets set aside, before equals after.
	var before, after map[string]any
	if err := json.Unmarshal(targetChange.Change.Before, &before); err != nil || before == nil {
		return verdict{}, fmt.Errorf("%s: the update of %s has no readable `before` object, so nothing can be compared: %w", where, target, err)
	}
	if err := json.Unmarshal(targetChange.Change.After, &after); err != nil || after == nil {
		return verdict{}, fmt.Errorf("%s: the update of %s has no readable `after` object, so nothing can be compared: %w", where, target, err)
	}
	var unknown any
	if len(targetChange.Change.AfterUnknown) > 0 {
		if err := json.Unmarshal(targetChange.Change.AfterUnknown, &unknown); err != nil {
			return verdict{}, fmt.Errorf("%s: the update of %s has an unreadable `after_unknown`: %w", where, target, err)
		}
	}

	// The two named checks first, because their messages are the ones an
	// operator needs at three in the morning. Both are also covered by the
	// whole-object comparison below; naming them is not what makes them held.
	if bi, ai := pathString(before, "template", 0, "container", 0, "image"), pathString(after, "template", 0, "container", 0, "image"); bi != ai {
		return verdict{}, fmt.Errorf("%s would change the serving image of %s, and the image belongs to deploy.sh, which moves it by digest after the migration and the health gate. Terraform ignores the image on this resource; a plan that changes it was made against a stale or wrong state.%s", where, target, planSummary(p))
	}
	if !reflect.DeepEqual(path(before, "ingress", 0, "traffic_weight"), path(after, "ingress", 0, "traffic_weight")) {
		return verdict{}, fmt.Errorf("%s would change the ingress traffic weights of %s, and traffic belongs to deploy.sh: it puts a revision in at zero, checks it, and shifts. Terraform ignores the weights on this resource; a plan that changes them was made against a stale or wrong state.%s", where, target, planSummary(p))
	}

	beforeEnv := envEntries(path(before, "template", 0, "container", 0, "env"))
	afterEnv := envEntries(path(after, "template", 0, "container", 0, "env"))
	beforeSecrets := secretEntries(before["secret"])
	afterSecrets := secretEntries(after["secret"])

	// Set the allowed lists aside, on both sides, and set aside everything
	// that is only known after apply, on both sides, then demand equality.
	stripAllowed(before)
	stripAllowed(after)
	stripUnknown(before, unknown)
	stripUnknown(after, unknown)
	var diffs []string
	diff("", before, after, &diffs)
	if len(diffs) > 0 {
		sort.Strings(diffs)
		var b strings.Builder
		fmt.Fprintf(&b, "%s changes %d attribute(s) of %s that are not the environment list or the secret references:\n", where, len(diffs), target)
		for _, d := range diffs {
			fmt.Fprintf(&b, "  %s\n", d)
		}
		b.WriteString("\nA configuration apply may change what the container reads and which vault secrets it references, and nothing else. Whatever moved above is either a stack change that needs a person to plan and apply it in full, or drift somebody made by hand that the tfvars do not describe.")
		b.WriteString(planSummary(p))
		return verdict{}, errors.New(b.String())
	}

	envAdded, envRemoved, envChanged := diffNames(beforeEnv, afterEnv)
	secAdded, secRemoved, secChanged := diffNames(beforeSecrets, afterSecrets)
	if len(envAdded)+len(envRemoved)+len(envChanged)+len(secAdded)+len(secRemoved)+len(secChanged) == 0 {
		// Terraform says update and this tool can find nothing that changed.
		// That is a plan this tool does not understand, and a plan it does not
		// understand is not one it applies.
		return verdict{}, fmt.Errorf("%s reports an update of %s in which neither the environment list nor the secret references differ and nothing else does either. Refusing a change this tool cannot describe.%s", where, target, planSummary(p))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "configguard: %s: one in-place update of %s, configuration only.\n", where, target)
	fmt.Fprintf(&b, "  env:     %s\n", describeNames(envAdded, envRemoved, envChanged))
	fmt.Fprintf(&b, "  secrets: %s\n", describeNames(secAdded, secRemoved, secChanged))
	b.WriteString("  image:   unchanged\n")
	b.WriteString("  traffic: unchanged\n")
	fmt.Fprintf(&b, "  %d other resource(s) in the plan, all no-op", countOthers(p, target))
	return verdict{Summary: b.String()}, nil
}

func describe(actions []string) string {
	switch strings.Join(actions, ",") {
	case "create":
		return "CREATE"
	case "delete":
		return "DESTROY"
	case "delete,create", "create,delete":
		return "REPLACE"
	default:
		return strings.Join(actions, ",")
	}
}

func countOthers(p *planFile, target string) int {
	n := 0
	for _, c := range p.ResourceChanges {
		if c.Mode == "managed" && c.Address != target {
			n++
		}
	}
	return n
}

// planSummary is every managed change in the plan, one line each, so that a
// refusal in a job log says what the plan wanted without anybody re-running
// it. Addresses and actions only.
func planSummary(p *planFile) string {
	var b strings.Builder
	b.WriteString("\n\nPlan summary (managed resources, no-ops omitted):\n")
	n := 0
	for _, c := range p.ResourceChanges {
		if c.Mode != "managed" || c.isNoop() {
			continue
		}
		n++
		fmt.Fprintf(&b, "  %-8s %s\n", c.actions(), c.Address)
	}
	if n == 0 {
		b.WriteString("  (nothing)\n")
	}
	return b.String()
}

// --- the comparison -------------------------------------------------------

// stripAllowed removes the two lists a configuration apply is allowed to
// change: every container's env and the app's secret references.
func stripAllowed(app map[string]any) {
	delete(app, "secret")
	for _, t := range asList(app["template"]) {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		for _, c := range asList(tm["container"]) {
			if cm, ok := c.(map[string]any); ok {
				delete(cm, "env")
			}
		}
	}
}

// stripUnknown removes, from a before or after object, every attribute the
// plan marks as known only after apply. Terraform records those as `true` at
// the same path in after_unknown and omits them from after, so a comparison
// that did not strip them would call every update a change to
// latest_revision_name. Stripping the same paths from before keeps the two
// sides comparable.
func stripUnknown(value any, unknown any) {
	switch u := unknown.(type) {
	case map[string]any:
		m, ok := value.(map[string]any)
		if !ok {
			return
		}
		for k, uv := range u {
			if b, isBool := uv.(bool); isBool {
				if b {
					delete(m, k)
				}
				continue
			}
			stripUnknown(m[k], uv)
		}
	case []any:
		l, ok := value.([]any)
		if !ok {
			return
		}
		for i, uv := range u {
			if i >= len(l) {
				break
			}
			if b, isBool := uv.(bool); isBool {
				if b {
					l[i] = nil
				}
				continue
			}
			stripUnknown(l[i], uv)
		}
	}
}

// diff walks two decoded JSON values and records the path of every
// difference. It stops at the first differing path within a subtree, which is
// enough to name the attribute; the plan text in the job log has the values.
func diff(prefix string, a, b any, out *[]string) {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, orRoot(prefix))
			return
		}
		keys := map[string]bool{}
		for k := range av {
			keys[k] = true
		}
		for k := range bv {
			keys[k] = true
		}
		for k := range keys {
			diff(join(prefix, k), av[k], bv[k], out)
		}
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			*out = append(*out, orRoot(prefix))
			return
		}
		for i := range av {
			diff(fmt.Sprintf("%s[%d]", prefix, i), av[i], bv[i], out)
		}
	default:
		if !reflect.DeepEqual(a, b) {
			*out = append(*out, orRoot(prefix))
		}
	}
}

func orRoot(p string) string {
	if p == "" {
		return "(whole object)"
	}
	return p
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// --- names ----------------------------------------------------------------

// entry is one env or secret item, keyed by name, with the rest of the item
// kept only so that a change to the same name (a value edit, a secret
// reference repointed) is detected. Values are never printed.
type entry struct {
	name string
	body string
}

func envEntries(v any) map[string]entry {
	out := map[string]entry{}
	for _, item := range asList(v) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		body, _ := json.Marshal(m)
		out[name] = entry{name: name, body: string(body)}
	}
	return out
}

func secretEntries(v any) map[string]entry {
	out := map[string]entry{}
	for _, item := range asList(v) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		// The reference is what matters and the value is what must never be
		// printed or compared into a message. Only the id and the identity
		// decide whether a reference changed.
		ref := fmt.Sprintf("%v|%v", m["key_vault_secret_id"], m["identity"])
		out[name] = entry{name: name, body: ref}
	}
	return out
}

func diffNames(before, after map[string]entry) (added, removed, changed []string) {
	for n := range after {
		if _, ok := before[n]; !ok {
			added = append(added, n)
		}
	}
	for n, b := range before {
		a, ok := after[n]
		if !ok {
			removed = append(removed, n)
			continue
		}
		if a.body != b.body {
			changed = append(changed, n)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return
}

func describeNames(added, removed, changed []string) string {
	var parts []string
	for _, n := range added {
		parts = append(parts, "+"+n)
	}
	for _, n := range removed {
		parts = append(parts, "-"+n)
	}
	for _, n := range changed {
		parts = append(parts, "~"+n)
	}
	if len(parts) == 0 {
		return "unchanged"
	}
	return strings.Join(parts, " ")
}

// --- helpers --------------------------------------------------------------

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// path walks a decoded JSON value by string keys and integer indexes and
// returns nil when any step is missing.
func path(v any, steps ...any) any {
	cur := v
	for _, s := range steps {
		switch k := s.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = m[k]
		case int:
			l, ok := cur.([]any)
			if !ok || k >= len(l) {
				return nil
			}
			cur = l[k]
		}
	}
	return cur
}

func pathString(v any, steps ...any) string {
	s, _ := path(v, steps...).(string)
	return s
}

// readPlan refuses anything it cannot understand rather than reporting an
// empty change set. "Could not read it" and "found nothing" must not share an
// exit code.
func readPlan(path string) (*planFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading plan: %w", err)
	}
	var p planFile
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parsing %s: %w\n\nThis wants the output of `terraform show -json <planfile>`, not the plan file itself and not the human readable plan", path, err)
	}
	if p.FormatVersion == "" {
		return nil, fmt.Errorf("%s has no format_version, so it is not a `terraform show -json` document. Refusing to call a file this tool did not understand a no-op", path)
	}
	return &p, nil
}
