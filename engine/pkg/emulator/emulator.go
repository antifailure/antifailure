// Package emulator declares the third party emulators this build can start.
//
// Antifailure does not write emulators. LocalStack, Azurite and the vendors'
// own emulators exist and carry years of fidelity work that a hand written
// replacement would not have, and nobody buys this product because its S3
// emulator is good. What the engine adds is that the application needs no
// endpoint override to reach one: every name resolves to the sidecar, the
// sidecar terminates TLS with a certificate authority the environment already
// trusts, and it answers for s3.amazonaws.com itself. The unmodified
// production code path runs against the emulator.
//
// This package is the declaration half of that, and it is deliberately data:
// an image pinned by digest, the hostnames it answers for, the services it
// covers, and the licence it ships under. It holds no routing, because routing
// lives in the sidecar, and no container lifecycle, because that lives in the
// runtime.
//
// It is a public package rather than an internal one for a reason that is not
// about API design. THIRD_PARTY_NOTICES.md is generated, and the generator
// lives in the tools module, which cannot import engine/internal. An emulator
// image whose licence is recorded by hand goes stale the first time somebody
// bumps a digest, so the licence has to be readable from the same declaration
// the engine starts the container from.
package emulator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// Service is one cloud service an emulator answers for.
//
// The hosts are written out per service rather than covered by one wildcard,
// for the reason the detection catalog gives at the same list: naming the
// service is the point. A refusal that says "this is Amazon S3 and it is
// outside the emulated surface" is a sentence somebody can act on, and one
// that says "no rule matches" is not.
type Service struct {
	// Name is what a person calls it, such as "Amazon S3".
	Name string
	// Hosts are the hostnames its SDK resolves, in the spellings the policy
	// engine compiles: a star stands for one whole label, so a regional
	// endpoint is sqs.*.amazonaws.com and a virtual hosted bucket is
	// *.s3.*.amazonaws.com.
	Hosts []string
	// Proves is the API call the conformance suite makes against this
	// service. A service listed here with nothing proving it is a claim, and
	// this field is what stops the surface table being one.
	Proves string
	// Note carries anything true about this service's coverage that a reader
	// would otherwise find out from a failure.
	Note string
}

// Licence is the attribution one emulator image owes.
type Licence struct {
	// Name is the licence, such as "Apache License 2.0".
	Name string
	// Holder is the copyright line, as the project writes it.
	Holder string
	// URL is where the licence text lives.
	URL string
}

// Emulator is a third party emulator this build can start.
//
// It implements extension.Emulator, which is the socket the registry validates
// and the engine resolves through, so a built in emulator and one registered
// from outside the engine module are the same kind of thing to everything
// downstream. There is no second interface.
type Emulator struct {
	// EmulatorName is the value an egress rule names this emulator by.
	EmulatorName string
	// Vendor is the cloud it answers for, such as "AWS".
	Vendor string
	// Project is the emulator's own name, such as "LocalStack".
	Project string
	// ProjectURL is where the emulator itself lives.
	ProjectURL string
	// Official reports whether the CLOUD VENDOR ships this emulator.
	//
	// It is a separate field rather than something a reader infers from
	// Project, because the inference is wrong exactly where it matters most.
	// Google publishes emulators for five of its services and none for Cloud
	// Storage, so the storage emulator in this build is a community project
	// with no affiliation to Google, and a surface table that printed it
	// beside the five official ones with nothing distinguishing them would be
	// presenting somebody else's software as the vendor's. A user who finds
	// that out from a failing test was misled by us rather than by the
	// emulator. Recorded here so the guide and the notices file read it from
	// the same declaration the engine starts the container from.
	Official bool
	// Image is the container image, PINNED BY DIGEST. The registry's
	// validation refuses a tag, because an emulator is the thing answering
	// for production's API and a tag that moves changes what an environment
	// was tested against with nothing in this repository changing.
	Image string
	// Port is the port inside the container the sidecar forwards to.
	Port int
	// Env is what the container is started with.
	Env map[string]string
	// Services are the cloud services this emulator answers for. THIS IS THE
	// SURFACE. A host outside it is refused rather than answered, because a
	// silent wrong answer from an emulator is worse than a refusal: it will
	// be trusted.
	Services []Service
	// Outside names services of the same cloud that are deliberately NOT
	// answered, with the reason. A gap a reader finds in a failure is worse
	// than one they read first, and a provider named and not built is worse
	// than one absent.
	Outside []Service
	// Licence is the attribution this image owes.
	Licence Licence
}

// Name is the value an egress rule names this emulator by.
func (e *Emulator) Name() string { return e.EmulatorName }

// Hosts are the hostnames it answers for, gathered from its covered services.
//
// Derived rather than declared separately, so that a service added to the
// surface table without its hosts, or a host routed to the emulator with no
// service claiming it, cannot happen: there is one list and it is the table.
func (e *Emulator) Hosts() []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(e.Services))
	for _, s := range e.Services {
		for _, h := range s.Hosts {
			if seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// Container describes what the engine starts.
func (e *Emulator) Container() extension.EmulatorContainer {
	env := make(map[string]string, len(e.Env))
	for k, v := range e.Env {
		env[k] = v
	}
	return extension.EmulatorContainer{Image: e.Image, Port: e.Port, Env: env}
}

// ServiceFor returns the covered service a host belongs to.
//
// A host that no covered service claims returns false, which is the refusal:
// the emulator answers for its surface and for nothing else.
func (e *Emulator) ServiceFor(host string) (Service, bool) {
	for _, s := range e.Services {
		for _, pattern := range s.Hosts {
			if hostMatches(pattern, host) {
				return s, true
			}
		}
	}
	return Service{}, false
}

// hostMatches is the same rule the policy engine compiles, in the small.
//
// A star stands for exactly one label anywhere in the pattern, EXCEPT a
// leading one, which stands for one or more. That asymmetry is not a detail:
// *.s3.*.amazonaws.com has to answer for my.bucket.s3.us-east-1.amazonaws.com,
// because a dot is legal in an S3 bucket name and virtual hosted addressing
// puts the bucket in the hostname. A version of this function that required
// the label counts to be equal refused that bucket while the policy engine
// routed it to the emulator, so the sidecar would have forwarded a request the
// surface table said was outside the surface. The test that compiles every
// pattern with the real engine is what found it, and it is why this function
// is checked against that engine rather than trusted to agree with it: host
// matching already lives in more than one place in this repository, and what
// holds those places together is a corpus of vectors, never a shared belief.
func hostMatches(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSuffix(pattern, "."))
	anyPrefix := strings.HasPrefix(pattern, "*.")
	if anyPrefix {
		pattern = pattern[2:]
	}
	p := strings.Split(pattern, ".")
	h := strings.Split(strings.ToLower(strings.TrimSuffix(host, ".")), ".")

	if anyPrefix {
		// The leading star covers at least one label, so an apex is not
		// matched by a pattern about its subdomains.
		if len(h) <= len(p) {
			return false
		}
		h = h[len(h)-len(p):]
	} else if len(p) != len(h) {
		return false
	}
	for i := range p {
		if h[i] == "" {
			return false
		}
		if p[i] != "*" && p[i] != h[i] {
			return false
		}
	}
	return true
}

// builtin holds the emulators compiled into this build, keyed by name.
//
// Registered from each cloud's own file in an init rather than listed in one
// place, so that three lanes adding three clouds do not all edit one slice
// literal and meet in a merge. Builtin sorts, so the order does not depend on
// which file the linker initialised first.
var builtin = map[string]*Emulator{}

// register adds an emulator to this build. It panics on a duplicate name,
// because two emulators under one name is a build defect and the registry's
// own validation would refuse it later, at the first af up, with the same
// message and much less context.
func register(e *Emulator) {
	if _, dup := builtin[e.EmulatorName]; dup {
		panic(fmt.Sprintf("emulator: two emulators are built in as %q", e.EmulatorName))
	}
	builtin[e.EmulatorName] = e
}

// Builtin returns every emulator this build can start, ordered by name.
func Builtin() []*Emulator {
	out := make([]*Emulator, 0, len(builtin))
	for _, e := range builtin {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EmulatorName < out[j].EmulatorName })
	return out
}

// Named returns the built in emulator registered under a name.
func Named(name string) (*Emulator, bool) {
	e, ok := builtin[name]
	return e, ok
}

// Names lists what is built in, ordered.
func Names() []string {
	out := make([]string, 0, len(builtin))
	for name := range builtin {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// RegisterBuiltin adds every built in emulator to a registry.
//
// It exists because the engine resolves an emulate rule through the registry
// and through nothing else: a rule names an emulator, the registry supplies
// the image, the digest, the port and the variables, and a name nothing
// registered is refused rather than defaulted. So an emulator this repository
// ships has to arrive the same way one written outside it does, and a
// declaration nobody registers is a provider named and not built, which is the
// thing this plan refuses on every other socket.
//
// A name already registered is left alone rather than added twice. Two
// emulators under one name is what Registry.Validate refuses, and an
// organization that has registered its own licensed image under the name aws
// has made a deliberate choice that a built in registration must not undo.
func RegisterBuiltin(r *extension.Registry) {
	for _, e := range Builtin() {
		if _, taken := r.EmulatorNamed(e.Name()); taken {
			continue
		}
		r.AddEmulator(e)
	}
}
