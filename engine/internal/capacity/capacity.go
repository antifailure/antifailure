// Package capacity decides whether a node can hold what an environment asked
// for, and says what is missing when it cannot.
//
// THE FAILURE IT WAS WRITTEN FOR. resources.cpu and resources.memory were in
// schemas/manifest.v1.json from version one and neither runtime emitted a
// resource requirement, so every environment this engine placed asked for
// nothing. A scheduler given no request has nothing to place against: pods land
// wherever they fall, one environment starves another, and the symptom is a
// workflow that reads as flaky. It also means environments per node was not a
// large number, it was an undefined one, so nobody could capacity plan a shared
// cluster at all.
//
// Emitting the request fixes the placement and creates a second problem this
// package is the answer to. A request larger than anything on the cluster is
// accepted by the API server and then never scheduled: the pod sits Pending
// with an event nobody is watching, and af up waits out its readiness timeout
// and reports a service that did not start. Refusing it before anything is
// created turns a twenty minute wait into a sentence naming the shortfall.
//
// WHAT IT DOES NOT DECIDE, said here rather than left to be found. Fits is two
// NECESSARY conditions and neither is sufficient: every instance has to fit on
// some single node, and the total has to fit in what is free across all of
// them. A set that passes both can still be unplaceable, because packing
// instances into nodes is bin packing and this does not attempt it. The
// scheduler remains the authority on placement; this only refuses the cases
// where no packing exists at all, which are the ones a person cannot diagnose
// from a Pending pod.
package capacity

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Node is one machine an environment could be placed on, in the units the
// manifest is written in.
//
// Free is what is left rather than what the machine has. On a cluster that is
// allocatable minus the requests of the pods already on it, which is the
// quantity the scheduler itself places against; on a single daemon it is the
// whole machine, because Docker reserves nothing and there is nothing to
// subtract. The difference is the reason the field is called Free rather than
// Capacity: a check against total capacity accepts an environment onto a full
// cluster, which is the same "accepted and scheduled anyway" this exists to
// stop.
type Node struct {
	Name        string
	MilliCPU    int64
	MemoryBytes int64
}

// Ask is what one service wants, per instance and multiplied out.
type Ask struct {
	Service     string
	Instances   int
	MilliCPU    int64
	MemoryBytes int64
}

// TotalMilliCPU is the CPU every instance of this service asks for together.
func (a Ask) TotalMilliCPU() int64 { return a.MilliCPU * int64(a.instances()) }

// TotalMemoryBytes is the memory every instance asks for together.
func (a Ask) TotalMemoryBytes() int64 { return a.MemoryBytes * int64(a.instances()) }

func (a Ask) instances() int {
	if a.Instances < 1 {
		return 1
	}
	return a.Instances
}

// Total sums what an environment asks for.
func Total(asks []Ask) (milliCPU, memoryBytes int64) {
	for _, a := range asks {
		milliCPU += a.TotalMilliCPU()
		memoryBytes += a.TotalMemoryBytes()
	}
	return milliCPU, memoryBytes
}

// Fits reports why the nodes cannot hold the asks, and nil when they can.
//
// An empty ask list, or a list where nothing named a size, fits anything: a
// manifest that declares no resources is the manifest every environment was
// before this key was honoured, and refusing those would break every existing
// repository to enforce a promise none of them made.
func Fits(nodes []Node, asks []Ask) error {
	if len(nodes) == 0 {
		return nil
	}
	var reasons []string

	// Per instance first, because it is the failure with no remedy: a pod
	// asking for more than the largest node has is unschedulable however
	// empty the cluster is, and no amount of waiting changes it.
	largest := nodes[0]
	for _, n := range nodes {
		if n.MilliCPU > largest.MilliCPU {
			largest = n
		}
	}
	roomiest := nodes[0]
	for _, n := range nodes {
		if n.MemoryBytes > roomiest.MemoryBytes {
			roomiest = n
		}
	}
	for _, a := range asks {
		if a.MilliCPU > largest.MilliCPU {
			reasons = append(reasons, fmt.Sprintf(
				"service %q asks for %s CPU per instance and the roomiest node has %s free, "+
					"so one instance of it cannot be placed at all",
				a.Service, schema.FormatMilliCPU(a.MilliCPU), schema.FormatMilliCPU(largest.MilliCPU)))
		}
		if a.MemoryBytes > roomiest.MemoryBytes {
			reasons = append(reasons, fmt.Sprintf(
				"service %q asks for %s of memory per instance and the roomiest node has %s free, "+
					"so one instance of it cannot be placed at all",
				a.Service, schema.FormatMemoryBytes(a.MemoryBytes),
				schema.FormatMemoryBytes(roomiest.MemoryBytes)))
		}
	}

	// Then the whole environment against everything free, which catches the
	// case where each service fits and the set does not.
	wantCPU, wantMem := Total(asks)
	var freeCPU, freeMem int64
	for _, n := range nodes {
		freeCPU += n.MilliCPU
		freeMem += n.MemoryBytes
	}
	if wantCPU > freeCPU {
		reasons = append(reasons, fmt.Sprintf(
			"the environment asks for %s CPU in total and %s is free across %s, "+
				"short by %s",
			schema.FormatMilliCPU(wantCPU), schema.FormatMilliCPU(freeCPU),
			plural(len(nodes), "node", "nodes"), schema.FormatMilliCPU(wantCPU-freeCPU)))
	}
	if wantMem > freeMem {
		reasons = append(reasons, fmt.Sprintf(
			"the environment asks for %s of memory in total and %s is free across %s, "+
				"short by %s",
			schema.FormatMemoryBytes(wantMem), schema.FormatMemoryBytes(freeMem),
			plural(len(nodes), "node", "nodes"), schema.FormatMemoryBytes(wantMem-freeMem)))
	}
	if len(reasons) == 0 {
		return nil
	}
	sort.Strings(reasons)
	return fmt.Errorf("%s", strings.Join(reasons, "; "))
}

// PerNode is how many of one environment a node holds, and how the answer was
// reached.
//
// Zero with a Binding of "" means the environment asked for nothing, which is
// what every environment this engine placed did before resources were honoured.
// That is not a large number of environments per node, it is no number: with no
// request the scheduler has nothing to place against and will keep accepting
// them until the machine falls over. Reported as Unbounded rather than as a
// count, because printing a very large integer there would read as an answer.
type PerNode struct {
	// Count is how many fit. Meaningless when Unbounded.
	Count int
	// Unbounded reports that nothing was asked for, so nothing bounds it.
	Unbounded bool
	// Binding names the dimension that ran out first, "cpu" or "memory".
	Binding string
	// EnvMilliCPU and EnvMemoryBytes are what one environment asks for.
	EnvMilliCPU    int64
	EnvMemoryBytes int64
}

// EnvironmentsPerNode divides one node by one environment.
//
// The number this lane owes. It is a division and not a measurement because
// that is what a request MEANS: a scheduler places against the request, so
// once the request is real the count is arithmetic the scheduler will agree
// with. What it is not is a statement that eleven environments on a node all
// perform well; it is a statement that the twelfth is refused instead of being
// accepted and left Pending.
func EnvironmentsPerNode(n Node, asks []Ask) PerNode {
	cpu, mem := Total(asks)
	out := PerNode{EnvMilliCPU: cpu, EnvMemoryBytes: mem}
	if cpu <= 0 && mem <= 0 {
		out.Unbounded = true
		return out
	}
	byCPU, byMem := -1, -1
	if cpu > 0 {
		byCPU = int(n.MilliCPU / cpu)
	}
	if mem > 0 {
		byMem = int(n.MemoryBytes / mem)
	}
	switch {
	case byCPU < 0:
		out.Count, out.Binding = byMem, "memory"
	case byMem < 0:
		out.Count, out.Binding = byCPU, "cpu"
	case byCPU <= byMem:
		out.Count, out.Binding = byCPU, "cpu"
	default:
		out.Count, out.Binding = byMem, "memory"
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// AsksFor is the sizes an environment declared, one entry per service that
// named one.
//
// One function rather than one per runtime, and that is the whole reason it
// exists here. There were two of these, identical, in the local and the
// Kubernetes packages, which is the shape serviceSpec had when health_timeout
// went missing from one of the two copies: every field a size ever gains has
// to be added to BOTH or the runtime that was forgotten quietly checks less
// than the other one, and nothing in the compiler notices.
//
// A service that named neither is left out rather than entered as a zero. A
// zero row would make the environment look like it asked for something and got
// nothing, and it would put a service with no request into a shortfall message
// that has nothing to say about it.
func AsksFor(services []provider.ServiceSpec) []Ask {
	var out []Ask
	for _, s := range services {
		if s.CPUMillis <= 0 && s.MemoryBytes <= 0 {
			continue
		}
		out = append(out, Ask{
			Service:     s.Name,
			Instances:   s.Instances(),
			MilliCPU:    s.CPUMillis,
			MemoryBytes: s.MemoryBytes,
		})
	}
	return out
}
