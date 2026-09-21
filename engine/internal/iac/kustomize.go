package iac

import (
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// The reader for Kustomize overlays.
//
// THE DANGER A KUSTOMIZATION CREATES, and the only reason this file exists.
// The walk reads every Kubernetes document in the tree, so a base's
// deployment.yaml is already a component with its base image and its base
// replica count. An overlay beside it may say `images: [{name: api, newTag:
// v2}]`, and then the image this reader has already reported is NOT what
// production runs. Nothing about that is visible: the component looks complete,
// it has a Known image with a real line number behind it, and it is wrong.
//
// A reader that ignored kustomizations would therefore be CONFIDENTLY WRONG on
// exactly the trees that use them, which is the one failure worse than a hole.
// So:
//
//   - `images` and `replicas` are APPLIED. They are small, completely
//     specified transformers, and applying them is how the reported value
//     becomes the deployed value.
//   - every other transformer, meaning strategic merge patches, JSON 6902
//     patches, name prefixes and suffixes, common labels and variable
//     substitutions, is NOT applied, and each one is recorded as an unread
//     source naming what it targets. The component keeps the base's values and
//     the caller is told, by name, that something it cannot see would change
//     them.
//
// It is the difference between "this is what production runs" and "this is
// what the base says, and here is the list of things that edit it".

type kustomization struct {
	Resources []string `yaml:"resources"`
	// Bases is the superseded spelling of Resources, still in wide use.
	Bases  []string `yaml:"bases"`
	Images []struct {
		Name    string `yaml:"name"`
		NewName string `yaml:"newName"`
		NewTag  string `yaml:"newTag"`
		Digest  string `yaml:"digest"`
	} `yaml:"images"`
	Replicas []struct {
		Name  string `yaml:"name"`
		Count *int   `yaml:"count"`
	} `yaml:"replicas"`
	NamePrefix string `yaml:"namePrefix"`
	NameSuffix string `yaml:"nameSuffix"`
	Patches    []struct {
		Path   string `yaml:"path"`
		Target struct {
			Kind string `yaml:"kind"`
			Name string `yaml:"name"`
		} `yaml:"target"`
	} `yaml:"patches"`
	PatchesStrategicMerge []string `yaml:"patchesStrategicMerge"`
	PatchesJSON6902       []struct {
		Path   string `yaml:"path"`
		Target struct {
			Kind string `yaml:"kind"`
			Name string `yaml:"name"`
		} `yaml:"target"`
	} `yaml:"patchesJson6902"`
	CommonLabels       map[string]string `yaml:"commonLabels"`
	CommonAnnotations  map[string]string `yaml:"commonAnnotations"`
	ConfigMapGenerator []struct {
		Name string `yaml:"name"`
	} `yaml:"configMapGenerator"`
	SecretGenerator []struct {
		Name string `yaml:"name"`
	} `yaml:"secretGenerator"`
}

func readKustomize(c *candidate, out *Reading) {
	var k kustomization
	if err := yaml.Unmarshal(c.body, &k); err != nil {
		out.Sources = append(out.Sources, Source{
			Path: c.rel, Dialect: DialectKustomize, Read: false,
			Why: "this kustomization did not parse as YAML, so any image or replica count it " +
				"overrides is still reported as the base declares it: " + err.Error(),
		})
		return
	}
	dir := path.Dir(c.rel)
	if dir == "." {
		dir = ""
	}
	scope := newKustomizeScope(dir, append(append([]string{}, k.Resources...), k.Bases...))

	applied := 0
	for i := range out.Components {
		comp := &out.Components[i]
		if !scope.covers(comp.At.File) {
			continue
		}
		for _, img := range k.Images {
			if applyImage(comp, img.Name, img.NewName, img.NewTag, img.Digest, c.rel) {
				applied++
			}
		}
		for _, r := range k.Replicas {
			if r.Count != nil && comp.Name == r.Name {
				comp.Replicas = Resolved(*r.Count, Position{File: c.rel})
				applied++
			}
		}
	}

	var unapplied []string
	add := func(what, target string) {
		if target != "" {
			what += " targeting " + target
		}
		unapplied = append(unapplied, what)
	}
	for _, p := range k.Patches {
		add("a patch", strings.TrimSpace(p.Target.Kind+" "+p.Target.Name))
	}
	for _, p := range k.PatchesStrategicMerge {
		add("a strategic merge patch from "+p, "")
	}
	for _, p := range k.PatchesJSON6902 {
		add("a JSON 6902 patch", strings.TrimSpace(p.Target.Kind+" "+p.Target.Name))
	}
	if k.NamePrefix != "" || k.NameSuffix != "" {
		add("a name prefix or suffix, so the names above are not the names in the cluster", "")
	}
	for _, g := range k.SecretGenerator {
		add("a secret generator producing "+g.Name, "")
	}
	for _, g := range k.ConfigMapGenerator {
		add("a config map generator producing "+g.Name, "")
	}

	switch {
	case len(unapplied) == 0:
		out.Sources = append(out.Sources, Source{Path: c.rel, Dialect: DialectKustomize, Read: true})
	default:
		out.Sources = append(out.Sources, Source{
			Path: c.rel, Dialect: DialectKustomize, Read: false,
			Why: "this overlay declares " + strings.Join(unapplied, ", ") + ", which this reader " +
				"does not apply, so the components it covers are described as their base declares " +
				"them rather than as this overlay deploys them",
		})
	}
}

// applyImage is Kustomize's image transformer, which matches on the image name
// BEFORE the tag and replaces the parts the overlay names.
func applyImage(comp *Component, name, newName, newTag, digest, from string) bool {
	current, ok := comp.Image.Get()
	if !ok || name == "" {
		return false
	}
	base, tag := splitImage(current)
	if base != name {
		return false
	}
	if newName != "" {
		base = newName
	}
	switch {
	case digest != "":
		tag = "@" + digest
	case newTag != "":
		tag = ":" + newTag
	}
	comp.Image = Resolved(base+tag, Position{File: from})
	return true
}

// splitImage separates an image reference from its tag or digest, leaving a
// registry's port alone: the colon in `registry:5000/api` is not a tag.
func splitImage(ref string) (string, string) {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		return ref[:i], ref[i:]
	}
	i := strings.LastIndex(ref, ":")
	if i < 0 {
		return ref, ""
	}
	if strings.Contains(ref[i+1:], "/") {
		return ref, ""
	}
	return ref[:i], ref[i:]
}

// kustomizeScope is the set of files an overlay's transformers reach.
//
// Matching by FILE rather than by name is what keeps two overlays over one
// base apart. A tree with a base and a staging and a production overlay yields
// three sets of components from the same documents, and an image override in
// the production overlay must not rewrite the staging one.
type kustomizeScope struct {
	files []string
	dirs  []string
}

func newKustomizeScope(dir string, resources []string) kustomizeScope {
	var s kustomizeScope
	for _, r := range resources {
		r = strings.TrimSpace(r)
		if r == "" || strings.Contains(r, "://") {
			// A remote resource is fetched over the network, which this reader
			// does not do. It is out of scope by being unreachable.
			continue
		}
		p := path.Clean(path.Join(dir, r))
		if strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml") {
			s.files = append(s.files, p)
			continue
		}
		s.dirs = append(s.dirs, p)
	}
	if len(s.files) == 0 && len(s.dirs) == 0 {
		// A kustomization with no resources still transforms the directory it
		// is in, which is how a single directory overlay is written.
		s.dirs = append(s.dirs, orDot(dir))
	}
	return s
}

func orDot(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

func (s kustomizeScope) covers(file string) bool {
	for _, f := range s.files {
		if f == file {
			return true
		}
	}
	for _, d := range s.dirs {
		if d == "." || file == d || strings.HasPrefix(file, d+"/") {
			return true
		}
	}
	return false
}
