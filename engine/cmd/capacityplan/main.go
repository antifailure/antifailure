// Command capacityplan answers how many of one environment fit on one node.
//
// THE FAILURE IT WAS WRITTEN FOR. resources.cpu and resources.memory were in
// schemas/manifest.v1.json from version one and neither runtime emitted a
// resource requirement, so every environment this engine placed asked the
// scheduler for nothing. That does not make environments per node a large
// number, it makes it an UNDEFINED one: with no request the scheduler has
// nothing to place against and keeps accepting environments until the node
// falls over, and the failure arrives as somebody else's workflow going flaky.
// Nobody could capacity plan a shared cluster, which is the enterprise case.
//
// SO THIS DIVIDES. It reads a manifest, sums what one environment asks for,
// and divides a node by it. The division is the whole method and it is not an
// approximation of a measurement: a request is a reservation, so once the
// request is real the scheduler agrees with this arithmetic by construction.
// What it is NOT is a claim that every environment on the node performs well.
// It is the claim that the one after the last is refused rather than accepted
// and left Pending.
//
// WHAT IT REFUSES TO ANSWER, which is the half that keeps the number honest. A
// service that named no size contributes nothing to the total, so an
// environment where any service is unsized is not bounded by this figure at
// all: the unsized services will take whatever is left. Rather than print a
// count that reads like an answer, this names those services and says the
// environment is unbounded. A number that quietly ignored them would be
// exactly the flattering arithmetic this repository keeps finding in its own
// instruments.
//
//	go run ./cmd/capacityplan -node-cpu 16 -node-memory 64Gi
//	go run ./cmd/capacityplan -manifest examples/shop/antifailure.yaml -node-cpu 8 -node-memory 32Gi
//
// It lives in the engine module rather than under tools/ because it reads
// internal/capacity and internal/manifest, which the tools module may not
// import and surfacecheck exists to keep that way.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/antifailure/antifailure/engine/internal/capacity"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func main() {
	var (
		path    = flag.String("manifest", "antifailure.yaml", "manifest to read")
		nodeCPU = flag.String("node-cpu", "",
			"CPU free on one node, in the manifest's units, for example 16")
		nodeMemory = flag.String("node-memory", "",
			"memory free on one node, in the manifest's units, for example 64Gi")
	)
	flag.Parse()

	if *nodeCPU == "" || *nodeMemory == "" {
		// Required rather than defaulted, on purpose. There is no node size
		// that is right for every reader, and a default here would be an
		// invented figure carried into whatever the number is quoted in.
		fmt.Fprintln(os.Stderr,
			"capacityplan: -node-cpu and -node-memory are required, in the manifest's "+
				"own units, and they are what is FREE on the node rather than what it has")
		os.Exit(2)
	}
	milliCPU, err := schema.ParseMilliCPU(*nodeCPU)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capacityplan: -node-cpu: %v\n", err)
		os.Exit(2)
	}
	memoryBytes, err := schema.ParseMemoryBytes(*nodeMemory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capacityplan: -node-memory: %v\n", err)
		os.Exit(2)
	}

	abs, err := filepath.Abs(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capacityplan: %v\n", err)
		os.Exit(1)
	}
	m, err := manifest.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capacityplan: %s: %v\n", *path, err)
		os.Exit(1)
	}

	asks, unsized, err := asksOf(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capacityplan: %s: %v\n", *path, err)
		os.Exit(1)
	}

	node := capacity.Node{Name: "node", MilliCPU: milliCPU, MemoryBytes: memoryBytes}
	per := capacity.EnvironmentsPerNode(node, asks)

	fmt.Printf("%s, %d services\n", m.Name, len(m.Services))
	fmt.Printf("  one environment asks for %s CPU and %s of memory\n",
		schema.FormatMilliCPU(per.EnvMilliCPU), schema.FormatMemoryBytes(per.EnvMemoryBytes))
	fmt.Printf("  one node has free       %s CPU and %s of memory\n",
		schema.FormatMilliCPU(node.MilliCPU), schema.FormatMemoryBytes(node.MemoryBytes))

	switch {
	case len(unsized) > 0:
		// The refusal that keeps the number honest, and it fires on the whole
		// manifest rather than being folded into the total. An unsized service
		// takes whatever is left, so a count computed around it is an upper
		// bound presented as an answer.
		fmt.Printf("  environments per node   UNBOUNDED\n")
		fmt.Printf("\n%d of %d services named no size, so nothing bounds them and the\n",
			len(unsized), len(m.Services))
		fmt.Printf("scheduler has nothing to place them against:\n")
		for _, name := range unsized {
			fmt.Printf("  %s\n", name)
		}
		fmt.Printf("\nThis is the state every environment this engine placed was in before\n")
		fmt.Printf("resources.cpu and resources.memory were honoured. It is not a large\n")
		fmt.Printf("number of environments per node, it is no number at all.\n")
		os.Exit(1)
	case per.Unbounded:
		fmt.Printf("  environments per node   UNBOUNDED, because the manifest asks for nothing\n")
		os.Exit(1)
	default:
		fmt.Printf("  environments per node   %d, bound by %s\n", per.Count, per.Binding)
	}
}

// asksOf turns a manifest into what each service reserves, and names the ones
// that reserve nothing.
func asksOf(m *schema.Manifest) ([]capacity.Ask, []string, error) {
	var asks []capacity.Ask
	var unsized []string
	for _, s := range m.Services {
		instances := s.Replicas
		if instances < 1 {
			instances = 1
		}
		var milliCPU, memoryBytes int64
		if s.Resources != nil {
			if s.Resources.CPU != "" {
				v, err := schema.ParseMilliCPU(s.Resources.CPU)
				if err != nil {
					return nil, nil, fmt.Errorf("service %q: cpu: %w", s.Name, err)
				}
				milliCPU = v
			}
			if s.Resources.Memory != "" {
				v, err := schema.ParseMemoryBytes(s.Resources.Memory)
				if err != nil {
					return nil, nil, fmt.Errorf("service %q: memory: %w", s.Name, err)
				}
				memoryBytes = v
			}
		}
		// Both halves, because a service capped on memory alone is still
		// unbounded on CPU and the scheduler still has nothing to place it
		// against there.
		if milliCPU <= 0 || memoryBytes <= 0 {
			unsized = append(unsized, s.Name)
			continue
		}
		asks = append(asks, capacity.Ask{
			Service: s.Name, Instances: instances,
			MilliCPU: milliCPU, MemoryBytes: memoryBytes,
		})
	}
	return asks, unsized, nil
}
