package fidelity

import (
	"fmt"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The topology dimension, which is the other half of the finding the
// datastores dimension closed.
//
// Six dimensions measured what an environment held and none of them measured
// its SHAPE. A manifest asking for three instances of a service ran one, and
// the report said "reproduced" about it, because the services dimension asks
// whether a service is up and stops there. Everything that only goes wrong
// above one instance was therefore invisible in the one place whose job is to
// say what a twin does not reproduce: leader election, a queue processed
// twice, a cache coherent with one instance and not two, a sticky session
// assumption, a migration safe against a single writer.
//
// Both runtimes honour the count now. This is the dimension that says whether
// they did, on this environment, for each service, in both numbers.
//
// WHY A SERVICE THAT NAMES NO COUNT IS UNMEASURED RATHER THAN REPRODUCED, and
// it is the whole argument of the file. The manifest's count is the only
// statement anybody has made about how many instances a service runs. A
// service that declares none has made no statement, and the environment runs
// one of it because one is what an omitted key means, not because anything
// compared that against production. Calling that reproduced would put a number
// in the numerator that nothing measured, which is the exact failure the
// runtime dimension refuses one level up: the manifest says where the copy
// runs and says nothing at all about where production runs, so there is
// nothing to compare it against.
//
// The consequence is deliberate and is worth saying out loud: on a manifest
// that names no counts at all, this dimension moves the score by nothing and
// prints one line saying why. A dimension that inflated a percentage with a
// row of trivially satisfied components would be worse than no dimension,
// because the number would go UP for an environment nobody had learned
// anything about.

// topology reports how many instances of each service are running against how
// many the manifest asked for.
func topology(obs Observation) Dimension {
	d := Dimension{Name: schema.FidelityTopology}
	declared := obs.Manifest.Services
	if len(declared) == 0 {
		d.NotApplicable = "the manifest declares no services"
		return d
	}
	if !anyCountDeclared(declared) {
		// One line rather than one unmeasured component per service. The
		// sentence is the finding: this environment runs one of everything,
		// and nothing here knows whether production does.
		d.NotApplicable = "no service says how many instances it runs, so this environment runs " +
			"one of each and nothing in the manifest says whether production runs one"
		return d
	}
	if obs.ServicesReason != "" {
		for _, s := range declared {
			d.Components = append(d.Components, Component{
				Name: s.Name, State: Unmeasured, Detail: obs.ServicesReason,
			})
		}
		return d
	}

	running := map[string]int{}
	present := map[string]bool{}
	for _, r := range obs.Running {
		present[r.Name] = true
		running[r.Name] = r.Instances
	}
	for _, s := range declared {
		d.Components = append(d.Components, instanceComponent(s, present[s.Name], running[s.Name]))
	}
	return d
}

// anyCountDeclared reports whether any service names an instance count.
//
// Read off the manifest struct rather than off the document, because this is
// the same question every runtime asks when it decides how many containers to
// start, and a count of one written out by hand is a statement its author made
// even though it is also the default.
func anyCountDeclared(services []schema.Service) bool {
	for _, s := range services {
		if s.Replicas > 0 {
			return true
		}
	}
	return false
}

// instanceComponent is one service's instance count, against what was asked.
func instanceComponent(s schema.Service, present bool, got int) Component {
	c := Component{Name: s.Name}
	want := instancesAsked(s)

	switch {
	case s.Replicas < 1:
		// In a manifest where some other service names a count. Still
		// unmeasured, for the reason at the top of this file, and the sentence
		// says which half of it is missing rather than repeating the verdict.
		c.State = Unmeasured
		c.Detail = "this service names no count, so it runs one and nothing says whether production runs one"
	case !present:
		c.State = Absent
		c.Detail = fmt.Sprintf("%s asked for and nothing is running", plural(int64(want), "instance", "instances"))
	case got < 1:
		// A runtime that predates instance counts reports none. Every reader
		// has to treat that as one rather than as none, and this dimension is
		// the one place where "at least one" is not an answer to the question
		// being asked.
		c.State = Unmeasured
		c.Detail = fmt.Sprintf(
			"the runtime did not say how many instances it is running, and the manifest asked for %d", want)
	case got == want:
		c.State = Reproduced
		c.Detail = fmt.Sprintf("%d of %d instances", got, want)
	case got < want:
		c.State = Absent
		c.Detail = fmt.Sprintf(
			"%d of %d instances, so anything that only breaks above one instance can still pass here",
			got, want)
	default:
		// More than asked for. A rolling update leaves the old pods beside the
		// new ones for a moment, and an instance nothing removed stays. Absent
		// rather than a state of its own, because the question is whether the
		// environment is the shape the manifest declared, and it is not; the
		// sentence carries which direction, so the word never has to.
		c.State = Absent
		c.Detail = fmt.Sprintf("%d instances running and %d asked for", got, want)
	}
	return c
}

// instancesAsked is how many instances the manifest asked for.
//
// Zero and a negative are one, which is what provider.ServiceSpec.Instances
// decides for the runtimes. Written again here rather than imported, because
// this package takes a schema.Service and that one takes the spec a runtime is
// handed, and a conversion between them purely to read one field would tie the
// report to the runtime's own struct.
func instancesAsked(s schema.Service) int {
	if s.Replicas < 1 {
		return 1
	}
	return s.Replicas
}
