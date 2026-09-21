package iac

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// The reader for `terraform show -json <planfile>`.
//
// A plan is the other half of this package's job and it answers a different
// question from the HCL. HCL says what the configuration declares, with holes
// wherever a value is decided at run time. A plan has already done that
// deciding, so a field that is Unreadable from the source is often Known from
// the plan. Between the two, a caller gets the most complete description of
// production that can be had without touching production.
//
// AND IT IS THE MORE DANGEROUS INPUT OF THE TWO, which is why this file is
// careful in ways the HCL reader does not need to be. MEASURED against
// Terraform v1.15.8 rather than remembered, over a configuration declaring
// `variable "password" { sensitive = true }` feeding a resource:
//
//   - `resource_changes[].change.after.input` held the literal secret.
//   - `planned_values...resources[].values.input` held it again.
//   - the top level `variables` object held it a THIRD time, with no
//     sensitivity marker of any kind beside it.
//   - the only signal in the first two places was a parallel object,
//     `sensitive_values: {"input": true}`.
//
// So this reader honours `sensitive_values` as a MASK, and never reads the top
// level `variables` object at all, because there is nothing in it that says
// which of its values may be published. It also never reads `prior_state`,
// which is state, for the reason the package header gives.

// planFile is the subset of `terraform show -json` this reads.
//
// The fields it deliberately omits are as much a part of this type as the ones
// it has: `prior_state` and `variables` are absent so that no code path in
// this package can reach them by accident.
type planFile struct {
	FormatVersion    string            `json:"format_version"`
	TerraformVersion string            `json:"terraform_version"`
	PlannedValues    *plannedValues    `json:"planned_values"`
	ResourceChanges  []json.RawMessage `json:"resource_changes"`
}

type plannedValues struct {
	RootModule *planModule `json:"root_module"`
}

type planModule struct {
	Address      string         `json:"address"`
	Resources    []planResource `json:"resources"`
	ChildModules []*planModule  `json:"child_modules"`
}

type planResource struct {
	Address string `json:"address"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Index   any    `json:"index"`
	// Values is the attributes the plan will apply.
	Values map[string]json.RawMessage `json:"values"`
	// SensitiveValues is a parallel object marking which of them must not be
	// published. Its shape mirrors Values: true for a sensitive scalar, and a
	// nested object or array for a sensitive part of a nested structure.
	SensitiveValues json.RawMessage `json:"sensitive_values"`
}

func readPlan(c *candidate, out *Reading) {
	var plan planFile
	if err := json.Unmarshal(c.body, &plan); err != nil {
		out.Sources = append(out.Sources, Source{
			Path: c.rel, Dialect: DialectTerraformPlan, Read: false,
			Why: "this looked like a Terraform plan and did not decode as one: " + err.Error(),
		})
		return
	}
	if plan.PlannedValues == nil || plan.PlannedValues.RootModule == nil {
		out.Sources = append(out.Sources, Source{
			Path: c.rel, Dialect: DialectTerraformPlan, Read: false,
			Why: "the plan has no planned_values, so it describes no resources; a plan of a " +
				"configuration with nothing in it looks like this",
		})
		return
	}
	n := len(out.Components)
	readPlanModule(c.rel, plan.PlannedValues.RootModule, out)
	out.Sources = append(out.Sources, Source{
		Path: c.rel, Dialect: DialectTerraformPlan, Read: len(out.Components) > n,
		Why: whenEmpty(len(out.Components) > n, "the plan's planned_values named no managed resources"),
	})
}

func whenEmpty(read bool, why string) string {
	if read {
		return ""
	}
	return why
}

func readPlanModule(file string, mod *planModule, out *Reading) {
	for i := range mod.Resources {
		res := &mod.Resources[i]
		// A data source is not something production holds, it is something the
		// configuration looked up. Reading one as a component would put the
		// things production READS beside the things production IS.
		if res.Mode != "" && res.Mode != "managed" {
			continue
		}
		out.Components = append(out.Components, planComponent(file, res))
	}
	for _, child := range mod.ChildModules {
		if child != nil {
			readPlanModule(file, child, out)
		}
	}
}

func planComponent(file string, res *planResource) Component {
	at := Position{File: file}
	mask := newMask(res.SensitiveValues)
	vals := map[string]cval{}
	for k, raw := range res.Values {
		vals[k] = decodePlanValue(raw, mask.child(k), at)
	}

	c := Component{
		Name:    res.Name,
		Address: res.Address,
		Type:    res.Type,
		At:      at,
		Kind:    KindUnknown,
		From:    DialectTerraformPlan,
		// A plan is a statement about what WILL be, so everything in
		// planned_values is present. That is the one thing a plan knows better
		// than the source does: every count and every for_each is resolved.
		Present: Resolved(true, at),
	}
	if v, ok := vals["name"]; ok && v.st == Known && v.kind == cvalScalar && v.s != "" {
		c.Name = v.s
	}

	get := func(names ...string) Value[string] {
		for _, n := range names {
			if v, ok := vals[n]; ok {
				return scrub(res.Type, n, valueAt(v, at))
			}
		}
		return Value[string]{}
	}
	c.Engine = get("engine")
	c.Version = get("engine_version", "version")
	if kind, ok := resourceKinds[res.Type]; ok {
		c.Kind = kind
		if c.Engine.State() == Absent {
			if e := engineOfKind(kind); e != "" {
				c.Engine = Resolved(e, at)
			}
		}
	} else if got, ok := c.Engine.Get(); ok {
		if kind, known := engineFamilies[strings.ToLower(got)]; known {
			c.Kind = kind
		}
	}
	c.Image = get("image")

	names := make([]string, 0, len(vals))
	for k := range vals {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		flattenPlanAttr(&c, res.Type, k, vals[k], at)
	}
	sort.SliceStable(c.Attrs, func(i, j int) bool { return c.Attrs[i].Name < c.Attrs[j].Name })
	return c
}

// flattenPlanAttr turns one decoded attribute into dotted Attrs, the same way
// genericAttrs does for HCL, so a component read from a plan and the same
// component read from its source carry their attributes under the same names.
func flattenPlanAttr(c *Component, resType, name string, v cval, at Position) {
	switch {
	case v.st == Known && v.kind == cvalObject:
		for _, key := range v.keys {
			flattenPlanAttr(c, resType, name+"."+key, v.obj[key], at)
		}
	case v.st == Known && v.kind == cvalList:
		for i, elem := range v.list {
			flattenPlanAttr(c, resType, name+"."+itoa(i), elem, at)
		}
	case v.st == Absent:
		// A null in a plan is the provider saying the attribute is unset,
		// which is Absent and worth nothing in the list.
	default:
		c.Attrs = append(c.Attrs, Attr{Name: name, Value: scrub(resType, name, valueAt(v, at)), At: at})
	}
}

// decodePlanValue turns one JSON value into a cval, dropping whatever the
// mask says is sensitive.
//
// PER ELEMENT, not per collection. A list with one sensitive element keeps the
// other elements and carries one hole, rather than the whole list disappearing
// because one entry could not be published.
func decodePlanValue(raw json.RawMessage, mask sensitivityMask, at Position) cval {
	if mask.all {
		return cval{st: Withheld, why: "the plan marks this value sensitive, so this reader " +
			"records that it is set and not what it is", at: at}
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return cval{at: at}
	}
	switch trimmed[0] {
	case '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return unknownVal(at, "the plan's value for this attribute did not decode")
		}
		out := cval{st: Known, kind: cvalObject, at: at, obj: map[string]cval{}}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.keys = append(out.keys, k)
			out.obj[k] = decodePlanValue(obj[k], mask.child(k), at)
		}
		return out
	case '[':
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return unknownVal(at, "the plan's value for this attribute did not decode")
		}
		out := cval{st: Known, kind: cvalList, at: at}
		for i, elem := range list {
			out.list = append(out.list, decodePlanValue(elem, mask.index(i), at))
		}
		return out
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return unknownVal(at, "the plan's value for this attribute did not decode")
		}
		return scalar(s, at)
	case 't', 'f':
		return scalar(trimmed, at)
	default:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return unknownVal(at, "the plan's value for this attribute did not decode")
		}
		if f == float64(int64(f)) {
			return scalar(strconv.FormatInt(int64(f), 10), at)
		}
		return scalar(strconv.FormatFloat(f, 'f', -1, 64), at)
	}
}

// sensitivityMask is Terraform's parallel `sensitive_values` object, walked
// alongside the values it masks.
//
// Terraform writes `true` for a sensitive value, and for a nested structure it
// writes an object or an array of the same shape with `true` at the sensitive
// leaves. An empty object means nothing under here is sensitive, which is the
// common case and why an absent mask must read as "not sensitive" rather than
// as "unknown".
type sensitivityMask struct {
	all  bool
	obj  map[string]json.RawMessage
	list []json.RawMessage
}

func newMask(raw json.RawMessage) sensitivityMask {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "" || trimmed == "null" || trimmed == "false":
		return sensitivityMask{}
	case trimmed == "true":
		return sensitivityMask{all: true}
	case trimmed[0] == '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			// A mask that will not decode is treated as MASKING EVERYTHING.
			// It is the only safe direction: an undecodable mask beside a
			// value means this reader cannot tell whether the value is a
			// secret, and "I could not tell" must never publish it.
			return sensitivityMask{all: true}
		}
		return sensitivityMask{obj: obj}
	case trimmed[0] == '[':
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return sensitivityMask{all: true}
		}
		return sensitivityMask{list: list}
	default:
		return sensitivityMask{all: true}
	}
}

func (m sensitivityMask) child(key string) sensitivityMask {
	if m.all {
		return sensitivityMask{all: true}
	}
	if raw, ok := m.obj[key]; ok {
		return newMask(raw)
	}
	return sensitivityMask{}
}

func (m sensitivityMask) index(i int) sensitivityMask {
	if m.all {
		return sensitivityMask{all: true}
	}
	if i < len(m.list) {
		return newMask(m.list[i])
	}
	return sensitivityMask{}
}
