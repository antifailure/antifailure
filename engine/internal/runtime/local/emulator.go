package local

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Emulator containers, and the two properties that make them worth having.
//
// They are on the INNER network and nothing else. The inner network is created
// with Docker's internal flag, which is the only setting that actually removes
// a container's route to the internet, so "the emulator reaches nothing" is a
// fact about the network rather than a promise made in a comment. Even the
// sidecar, which is the one container with a route out, is on both networks
// only because it has to be; an emulator has no reason to be and is not.
//
// They are addressed by a fixed alias rather than by an address. The sidecar's
// configuration is written before any container has an address, and an alias
// on the environment's own network resolves through the same resolver every
// service already uses. It also means the route in the sidecar's configuration
// is readable: af-emu-localstack:4566 says what it is, where an address says
// nothing.

// emulatorAliasPrefix is what an emulator answers to on the environment's
// network.
//
// Prefixed rather than bare, because service names come from the manifest and
// an emulator name comes from a registration, and a manifest with a service
// called localstack must not collide with an emulator called the same. The
// prefix is not a namespace anybody types: nothing in a manifest ever names
// this alias, only the emulator's own name.
const emulatorAliasPrefix = "af-emu-"

// EmulatorAlias is the hostname an emulator answers to inside an environment.
func EmulatorAlias(name string) string { return emulatorAliasPrefix + name }

func emulatorName(envID, name string) string {
	return "af-emu-" + name + "-" + envID
}

func companionName(envID, name string) string {
	return "af-emu-companion-" + name + "-" + envID
}

// CompanionAlias is the hostname a companion answers to inside an environment.
//
// The emulator reaches it by this name and nothing else does: the sidecar
// never forwards to a companion, because a companion is not something the
// application talks to. That is the whole difference between a companion and a
// second emulator.
func CompanionAlias(name string) string { return "af-emu-companion-" + name }

// emulatorRoutes maps each emulator to where the sidecar forwards to.
func emulatorRoutes(specs []provider.EmulatorSpec) map[string]emulatorRoute {
	if len(specs) == 0 {
		return nil
	}
	out := make(map[string]emulatorRoute, len(specs))
	for _, e := range specs {
		out[e.Name] = emulatorRoute{
			Address: fmt.Sprintf("%s:%d", EmulatorAlias(e.Name), e.Port),
		}
	}
	return out
}

// startEmulators brings up every emulator the environment declares.
//
// It runs before the sidecar, so that the sidecar reporting ready means every
// address an outbound call can be answered from already exists. Starting them
// after would leave a window in which an application's first S3 call reaches a
// name that does not resolve yet, and a connection error at that moment reads
// exactly like a blocked host.
func (r *Runtime) startEmulators(
	ctx context.Context,
	envID string,
	specs []provider.EmulatorSpec,
	nets networks,
	journal func(string, string) error,
	progress func(string),
) error {
	for _, e := range specs {
		if err := r.startEmulator(ctx, envID, e, nets, journal, progress); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) startEmulator(
	ctx context.Context,
	envID string,
	e provider.EmulatorSpec,
	nets networks,
	journal func(string, string) error,
	progress func(string),
) error {
	// Refused here as well as in the registry. The registry checks a
	// registration when it is validated, and this checks the spec that
	// actually reaches the daemon, because the two are different objects and
	// the one that starts a container is this one.
	if !strings.Contains(e.Image, "@sha256:") {
		return aferrors.Coded(aferrors.AFRUN040, "detail", fmt.Sprintf(
			"the emulator %q is pinned by %q rather than by digest, so what this environment "+
				"was tested against could change with nothing in the repository changing",
			e.Name, e.Image))
	}

	name := emulatorName(envID, e.Name)
	// Journalled BEFORE it is created, like every other resource, because a
	// container created before it was recorded is a container teardown cannot
	// find.
	if err := journal(kindContainer, name); err != nil {
		return err
	}
	if existing, err := r.cli.ContainerInspect(ctx, name); err == nil {
		if existing.State != nil && existing.State.Running {
			return nil
		}
		if rmErr := dockerutil.RemoveContainer(ctx, r.cli, existing.ID); rmErr != nil {
			return rmErr
		}
	}
	if err := r.ensureImageByRef(ctx, e.Image, "the "+e.Name+" emulator", progress); err != nil {
		return err
	}
	if err := r.startCompanions(ctx, envID, e, nets, journal, progress); err != nil {
		return err
	}

	labels := r.managed(dockerutil.KindEmulator, envID)
	labels[dockerutil.LabelService] = e.Name

	resp, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image:  e.Image,
			Labels: labels,
			Env:    emulatorEnv(e.Env),
			Cmd:    e.Command,
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
		},
		&network.NetworkingConfig{
			// The inner network alone. This is the whole containment story
			// for an emulator: no edge endpoint means no route out, and it is
			// enforced by Docker rather than by a rule somebody could change.
			EndpointsConfig: map[string]*network.EndpointSettings{
				nets.inner: {Aliases: []string{EmulatorAlias(e.Name)}},
			},
		}, nil, name)
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("creating the %s emulator: %v", e.Name, err))
	}
	if err := r.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("starting the %s emulator: %v", e.Name, err))
	}
	if len(e.Companions) == 0 {
		progress(fmt.Sprintf("%s emulator ready at %s:%d, with no route out",
			e.Name, EmulatorAlias(e.Name), e.Port))
		return nil
	}
	progress(fmt.Sprintf("%s emulator ready at %s:%d, with %d %s beside it and no route out",
		e.Name, EmulatorAlias(e.Name), e.Port,
		len(e.Companions), plural(len(e.Companions), "companion", "companions")))
	return nil
}

// startCompanions brings up the containers an emulator does not work without.
//
// Before the emulator, not after. The Azure Service Bus emulator refuses to
// start without its MSSQL instance and exits, and a container that exited
// during startup reads as a broken image rather than as a missing dependency.
func (r *Runtime) startCompanions(
	ctx context.Context,
	envID string,
	e provider.EmulatorSpec,
	nets networks,
	journal func(string, string) error,
	progress func(string),
) error {
	for _, c := range e.Companions {
		// The same digest rule, and it is not a formality. A companion runs
		// beside a copy of production data on exactly the terms the emulator
		// does, and "it came with the emulator" is not a provenance.
		if !strings.Contains(c.Image, "@sha256:") {
			return aferrors.Coded(aferrors.AFRUN040, "detail", fmt.Sprintf(
				"the companion %q of the %s emulator is pinned by a tag rather than by "+
					"digest", c.Image, e.Name))
		}
		name := companionName(envID, c.Name)
		if err := journal(kindContainer, name); err != nil {
			return err
		}
		if existing, err := r.cli.ContainerInspect(ctx, name); err == nil {
			if existing.State != nil && existing.State.Running {
				continue
			}
			if rmErr := dockerutil.RemoveContainer(ctx, r.cli, existing.ID); rmErr != nil {
				return rmErr
			}
		}
		if err := r.ensureImageByRef(ctx, c.Image,
			fmt.Sprintf("the %s emulator's companion", e.Name), progress); err != nil {
			return err
		}

		labels := r.managed(dockerutil.KindEmulator, envID)
		labels[dockerutil.LabelService] = c.Name

		resp, err := r.cli.ContainerCreate(ctx,
			&container.Config{
				Image:  c.Image,
				Labels: labels,
				Env:    emulatorEnv(c.Env),
				Cmd:    c.Command,
			},
			&container.HostConfig{
				RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			},
			&network.NetworkingConfig{
				// The inner network alone, exactly as the emulator gets. A
				// companion with a route out would be a hole shaped like
				// "the emulator needed a database".
				EndpointsConfig: map[string]*network.EndpointSettings{
					nets.inner: {Aliases: []string{CompanionAlias(c.Name)}},
				},
			}, nil, name)
		if err != nil {
			return aferrors.Wrap(err, aferrors.AFRUN040, "detail",
				fmt.Sprintf("creating the %s emulator's companion %s: %v", e.Name, c.Name, err))
		}
		if err := r.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			return aferrors.Wrap(err, aferrors.AFRUN040, "detail",
				fmt.Sprintf("starting the %s emulator's companion %s: %v", e.Name, c.Name, err))
		}
	}
	return nil
}

// ensureImageByRef pulls an image if the daemon does not have it.
//
// One function for the emulator and its companions, because a companion image
// that failed to pull has to fail in the same words: the Service Bus emulator
// and its MSSQL instance are two pulls and one feature, and a message that
// only ever named the emulator would send somebody to the wrong registry.
func (r *Runtime) ensureImageByRef(
	ctx context.Context, ref, what string, progress func(string),
) error {
	if _, err := r.cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	progress(fmt.Sprintf("pulling the image for %s (once per digest)", what))
	rc, err := r.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("pulling %s for %s: %v", ref, what, err))
	}
	// Drained before the pull counts as finished, or the image is only partly
	// present when the create below asks for it.
	dockerutil.Discard(rc)
	if _, err := r.cli.ImageInspect(ctx, ref); err != nil {
		return aferrors.Coded(aferrors.AFRUN040, "detail", fmt.Sprintf(
			"%s is not present after pulling it for %s", ref, what))
	}
	return nil
}

// emulatorEnv renders the registration's variables, sorted so that two runs of
// one registration produce the same container.
func emulatorEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
