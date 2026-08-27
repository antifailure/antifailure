package k8s

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// ImageLoader makes an image available to a cluster's nodes.
//
// It exists because "the image is present" means two different things either
// side of this boundary and the difference is where af up fails for people.
// The engine builds a service's image, and the sidecar's image, on the Docker
// daemon of the machine that ran af. A cluster's nodes cannot see that daemon.
// So an image that exists, that was built successfully, that is right there in
// docker images, is an image the cluster will report as ErrImagePull several
// minutes later with no hint that a build on the wrong machine is what went
// wrong.
//
// There are exactly two honest answers: put the image where the nodes can pull
// it, which means a registry, or put it into the nodes directly, which the
// local development clusters support and no real cluster does. This interface
// is the choice between them, made once, by whoever knows which kind of
// cluster this is.
type ImageLoader interface {
	// Ensure makes ref pullable by the cluster's nodes, and returns the
	// reference a pod should actually use, which is not always the one it was
	// given: pushing to a registry renames the image.
	Ensure(ctx context.Context, ref string) (string, error)
	// Describe says what this loader does, for the error that mentions it.
	Describe() string
}

// NoImageLoader is for a cluster that can already pull every image named.
//
// The right answer whenever images come from a registry the nodes are
// configured for, which is every production cluster.
type NoImageLoader struct{}

func (NoImageLoader) Ensure(_ context.Context, ref string) (string, error) { return ref, nil }
func (NoImageLoader) Describe() string {
	return "images are pulled by the cluster, and none are copied to it"
}

// LocalClusterLoader copies an image from the local Docker daemon into the
// nodes of a development cluster.
//
// Both k3d and kind support this, and nothing else does: it works by writing
// into the node's own image store, which is only reachable because the node is
// a container on this machine. It is the reason a laptop can run this runtime
// at all without standing up a registry first.
type LocalClusterLoader struct {
	// Tool is "k3d" or "kind".
	Tool string
	// Cluster is the cluster name that tool knows it by.
	Cluster string
}

func (l LocalClusterLoader) Describe() string {
	return fmt.Sprintf("images are copied from the local Docker daemon into the %s cluster %q",
		l.Tool, l.Cluster)
}

func (l LocalClusterLoader) Ensure(ctx context.Context, ref string) (string, error) {
	var cmd *exec.Cmd
	switch l.Tool {
	case "k3d":
		cmd = exec.CommandContext(ctx, "k3d", "image", "import", "--cluster", l.Cluster, ref)
	case "kind":
		cmd = exec.CommandContext(ctx, "kind", "load", "docker-image", "--name", l.Cluster, ref)
	default:
		return "", aferrors.Coded(aferrors.AFRUN040,
			"detail", fmt.Sprintf("unknown local cluster tool %q", l.Tool))
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", aferrors.Coded(aferrors.AFRUN040,
			"detail", fmt.Sprintf("copying %s into the %s cluster %q failed: %v: %s",
				ref, l.Tool, l.Cluster, err, strings.TrimSpace(string(out))))
	}
	return ref, nil
}

// LocalClusterLoaderFor recognises a development cluster from its kubeconfig
// context name, which is the only place the tool that made it leaves a mark.
//
// k3d and kind both prefix the context with their own name, so this is exact
// rather than a guess, and it returns nothing for anything else rather than
// assuming. A wrong guess here would try to copy an image into a production
// cluster and fail confusingly; no guess just means the images have to come
// from a registry, which is what a production cluster wanted anyway.
func LocalClusterLoaderFor(kubeContext string) (ImageLoader, bool) {
	for _, tool := range []string{"k3d", "kind"} {
		if name, ok := strings.CutPrefix(kubeContext, tool+"-"); ok && name != "" {
			return LocalClusterLoader{Tool: tool, Cluster: name}, true
		}
	}
	return nil, false
}

// ensureImages puts every image an environment needs where the nodes can find
// it, before anything is created.
func (r *Runtime) ensureImages(ctx context.Context, spec provider.EnvSpec, progress func(string)) error {
	proxy, err := r.proxyImage(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	refs := []string{proxy}
	for _, s := range spec.Services {
		refs = append(refs, s.Image)
	}
	for _, ref := range refs {
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		if _, err := r.images.Ensure(ctx, ref); err != nil {
			return err
		}
	}
	if len(seen) > 0 {
		progress(fmt.Sprintf("%d images available to the cluster", len(seen)))
	}
	return nil
}
