package env

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/iac"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Turning production's infrastructure as code into the cloud resources an
// environment creates inside its emulators.
//
// THE GAP THIS CLOSES. An emulator comes up EMPTY, deliberately: every one is
// configured to keep nothing, so that a twin which inherited the last twin's
// buckets would be reproducible only by accident. The consequence is that a
// bucket, a queue and a table which exist in production exist nowhere in the
// twin, and an application that reads its own bucket on startup meets an
// emulator that has none. engine/pkg/emulator knows how to create them and
// engine/internal/runtime/local knows when to, and until this file existed
// nothing in the product filled the list they both read: it was written only
// by two end to end tests. A seam filled by its own tests is a feature that
// looks finished and does nothing.
//
// THIS FILE IS THE PRODUCER, and it sits here rather than in
// engine/internal/iac on purpose. The reader knows nothing about environments,
// emulators or providers, and the test asserting that it can reach neither
// os/exec nor net/http over its whole import graph is worth more than the
// convenience of putting the conversion next to the types it converts.
//
// WHAT IT REFUSES TO PASS ON, which is the part worth reading:
//
//   - A resource whose NAME this reader could not resolve. Creating a bucket
//     called "${var.env}-assets" literally, or guessing at it, gives the twin
//     a bucket production does not have under a name the application will
//     never ask for, and reports it as reproduced. A name that is not known is
//     not a name.
//   - A value the reader WITHHELD because it is a credential. Those must never
//     reach a seeding request, which is built into a command and written into
//     a journal. This is the one rule in this file with no exception and no
//     configuration.
//   - A resource the configuration declares and does not deploy, meaning a
//     count that resolves to zero.
//
// None of those is dropped silently. Everything this file could not pass on
// comes back as an iac.Unmeasured beside the resources, so a run says what it
// could not reproduce rather than reproducing less than it claims.

// seedableKinds are the component kinds that are cloud resources an emulator
// can hold.
//
// Services and datastores are deliberately absent: a container app is not
// something an emulator creates, and a Postgres is reproduced by the database
// machinery rather than by a seeding request. Passing them through would give
// every run a line saying no emulator creates an azurerm_role_assignment,
// which is true and is noise.
var seedableKinds = map[iac.Kind]bool{
	iac.KindObjectStore: true,
	iac.KindQueue:       true,
	iac.KindTopic:       true,
	iac.KindTable:       true,
	iac.KindSecret:      true,
	iac.KindParameter:   true,
}

// cloudResources reads every stack the manifest names and returns what
// production declares, plus everything that could not be read.
//
// An absent infrastructure section is not an error and not an empty result: it
// is a manifest that never said where production is declared, and the caller
// reports that rather than reporting that production declares nothing.
func cloudResources(
	ctx context.Context,
	root string,
	infra *schema.Infrastructure,
) ([]provider.CloudResource, []iac.Unmeasured, error) {
	if infra == nil || len(infra.Stacks) == 0 {
		return nil, nil, nil
	}
	var (
		out        []provider.CloudResource
		unmeasured []iac.Unmeasured
	)
	for _, stack := range infra.Stacks {
		if stack.Source != schema.InfraTerraform {
			// The manifest's enum carries only the sources whose reader
			// exists, so this is unreachable today and is here because an
			// enum member added without its reader must not read as nothing.
			unmeasured = append(unmeasured, iac.Unmeasured{
				What: "the stack at " + stack.Path,
				Why: fmt.Sprintf("this build has no reader for a %s stack, so nothing it "+
					"declares reached the twin", stack.Source),
				At: iac.Position{File: stack.Path},
			})
			continue
		}
		dir := filepath.Join(root, filepath.FromSlash(stack.Path))
		opts := []iac.Option{}
		if stack.Workspace != "" {
			opts = append(opts, iac.WithWorkspace(stack.Workspace))
		}
		if rel := varFilesRelativeTo(root, stack); len(rel) > 0 {
			opts = append(opts, iac.WithVarFiles(rel...))
		}
		reading, err := iac.Read(ctx, dir, opts...)
		if err != nil {
			// A stack that cannot be walked at all fails the environment. The
			// manifest named this directory and the validator confirmed it
			// exists, so a failure here is a real one and quietly returning
			// no resources would describe production as empty.
			return nil, nil, fmt.Errorf("reading the infrastructure at %s: %w", stack.Path, err)
		}
		res, missed := seedableFrom(reading, stack.Path)
		out = append(out, res...)
		unmeasured = append(unmeasured, missed...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Name < out[j].Name
	})
	return out, unmeasured, nil
}

// varFilesRelativeTo rebases the manifest's repository relative variable files
// onto the stack directory the reader is pointed at.
//
// The manifest writes them from the repository root because that is the only
// place a person can write a path that means one thing, and the reader takes
// them relative to the directory it is reading, because that is the only tree
// it has. Getting this wrong is silent: a variable file that does not resolve
// is simply not applied, and every variable it would have set comes back
// unresolved, which reads as a configuration that does not say.
func varFilesRelativeTo(root string, stack schema.InfraStack) []string {
	dir := filepath.Join(root, filepath.FromSlash(stack.Path))
	var out []string
	for _, vf := range stack.VarFiles {
		abs := filepath.Join(root, filepath.FromSlash(vf))
		rel, err := filepath.Rel(dir, abs)
		if err != nil {
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

// seedableFrom converts one reading into declarations, and reports what it
// could not convert.
func seedableFrom(r *iac.Reading, stackPath string) ([]provider.CloudResource, []iac.Unmeasured) {
	var (
		out        []provider.CloudResource
		unmeasured []iac.Unmeasured
	)
	// Everything the reader itself could not look at travels with the result.
	// A file it could not parse may have held three buckets, and a run that
	// listed the two it did read as "reproduced" without mentioning the file
	// would be the exact overstatement this package exists to prevent.
	for _, u := range r.Unmeasured() {
		u.What = stackPath + ": " + u.What
		unmeasured = append(unmeasured, u)
	}

	for _, c := range r.Components {
		if !seedableKinds[c.Kind] {
			continue
		}
		at := c.At
		if present, known := c.Present.Get(); known && !present {
			// Declared and deliberately not deployed. Not a hole: the
			// configuration says production does not have this.
			continue
		}
		name, known := c.Name, true
		if v, ok := nameOf(c); ok {
			name = v
		} else {
			known = false
		}
		if !known || name == "" {
			unmeasured = append(unmeasured, iac.Unmeasured{
				What: fmt.Sprintf("%s: the %s %s", stackPath, c.Kind, c.Address),
				Why: "its name is not resolvable without running Terraform, and a resource " +
					"created under a guessed name is one the application will never ask for, " +
					"so it was not created in the twin",
				At: at,
			})
			continue
		}
		out = append(out, provider.CloudResource{
			Type: c.Type, Name: name, Attributes: attributesOf(c),
		})

		if c.Present.State() == iac.Unreadable {
			// Created anyway, and said so. An application that reads its own
			// bucket on startup fails hard against a twin that lacks it,
			// where an extra empty bucket costs nothing, so the tie breaks
			// towards creating it. That is a judgement and it is reported
			// rather than buried.
			unmeasured = append(unmeasured, iac.Unmeasured{
				What: fmt.Sprintf("%s: whether production has the %s %s", stackPath, c.Kind, name),
				Why: "it was created in the twin anyway, because a missing resource breaks an " +
					"application that reads it and an unused one costs nothing: " + c.Present.Why(),
				At: at,
			})
		}
	}
	return out, unmeasured
}

// nameOf is the name production gives the resource.
//
// Component.Name already falls back to the resource's local label when the
// name attribute could not be resolved, which is right for a report and wrong
// here: a bucket created under a Terraform label rather than under its real
// name is a bucket the application never asks for. So this insists on the
// resolved `name` attribute when the component carries one.
func nameOf(c iac.Component) (string, bool) {
	for _, a := range c.Attrs {
		if a.Name != "name" && a.Name != "bucket" {
			continue
		}
		if v, ok := a.Value.Get(); ok && v != "" {
			return v, true
		}
		return "", false
	}
	// No name attribute at all: the resource is named by its label, which is
	// what the provider would use.
	return c.Name, c.Name != ""
}

// attributesOf flattens a component's known attributes into the map the
// seeder reads.
//
// IT REPORTS NOTHING, and that is a deletion rather than an omission. It used
// to return an iac.Unmeasured for every attribute it dropped, and a mutation
// aimed at that reporting SURVIVED: iac.Reading.Unmeasured already carries
// every Unreadable and Withheld field of every component, and seedableFrom
// already passes all of it through, so each dropped attribute was arriving in
// the report twice under two different wordings. A second reporter that no
// failing case can be aimed at is not a safety net, it is something that hides
// one.
//
// A withheld value cannot reach the map even if this function tried: Get is
// the only route to a Value and it refuses everything that is not Known, which
// is why the leak is impossible here rather than merely prevented.
func attributesOf(c iac.Component) map[string]string {
	attrs := map[string]string{}
	single := singleLeafParents(c)
	for _, a := range c.Attrs {
		v, ok := a.Value.Get()
		if !ok {
			// Omitted rather than sent as an empty string. The seeder's Attr
			// then answers "the declaration did not carry it", which is true:
			// this declaration carries no value anybody can act on.
			continue
		}
		attrs[a.Name] = v
		// THE ALIAS, and it is here because of a real mismatch rather than a
		// guess. The seeders read flat Terraform attribute names,
		// `versioning` and `hash_key` and `shard_count`, while a nested block
		// arrives from the reader dotted, so `versioning { enabled = true }`
		// becomes `versioning.enabled` and a bucket declaring versioning the
		// legacy way would have had it silently not reproduced. A block
		// carrying exactly ONE attribute also publishes its parent name,
		// which covers that shape without a table of per provider special
		// cases and cannot overwrite a real attribute of the same name.
		if parent, ok := single[a.Name]; ok {
			if _, taken := attrs[parent]; !taken {
				attrs[parent] = v
			}
		}
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

// singleLeafParents maps a dotted attribute to its parent, for parents that
// have exactly one child.
func singleLeafParents(c iac.Component) map[string]string {
	children := map[string][]string{}
	for _, a := range c.Attrs {
		i := strings.LastIndex(a.Name, ".")
		if i <= 0 {
			continue
		}
		parent := a.Name[:i]
		children[parent] = append(children[parent], a.Name)
	}
	out := map[string]string{}
	for parent, kids := range children {
		if len(kids) == 1 {
			out[kids[0]] = parent
		}
	}
	return out
}
