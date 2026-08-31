// Command tagsync refuses a version pin that names a tag nobody has published.
//
// It exists because bumping the version at release time is two different jobs
// that look like one, and doing them in one commit breaks production.
//
// Most version strings in this repository are consumed from a released tree, so
// they should name the release being cut. The Terraform image_tag defaults are
// not. `azurerm_container_app_job.maintenance` reads that default with no
// `ignore_changes` on its image, so the value is live: an apply from `main`
// takes it. Pointing it at v1.0.0 before v1.0.0 exists in the registry does not
// produce a stale deployment, it produces a failed apply on the stack that runs
// the product. The bump has to happen after the tag publishes, and "do a thing
// after the release" survives about one release.
//
// So this is the gate rather than the comment. A pin declared as live must name
// a tag that already exists. A pin declared as released-tree may also name the
// release being prepared, which is the version at the top of the changelog,
// because a chart is installed from a tag and never from `main`.
//
// It reads git for the tag list, and refuses to run when git shows none. A
// shallow clone has no tags, and a check that finds nothing to compare against
// and prints ok is worse than no check: it is a green gate over a subject it
// never examined. CI fetches tags for the job that runs this.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// kind is when a pin is read by whatever consumes it.
type kind int

const (
	// live is read by an apply from main, so it may only name a published tag.
	live kind = iota
	// released is read from a tag's own tree, so it may also name the release
	// being prepared.
	released
)

type pin struct {
	file    string
	what    string
	pattern *regexp.Regexp
	kind    kind
}

// The pins this repository has. Each pattern must match exactly once in its
// file: a rename that makes one match nothing has to fail loudly, because a
// pattern that quietly finds nothing is how this check would start passing
// everything on the day somebody moved a variable.
var pins = []pin{
	{
		file:    "infra/terraform/stacks/control-plane/variables.tf",
		what:    "the control plane stack's image_tag default",
		pattern: regexp.MustCompile(`(?s)variable\s+"image_tag"\s*\{.*?default\s*=\s*"([^"]+)"`),
		kind:    live,
	},
	{
		file:    "infra/terraform/modules/control-plane/variables.tf",
		what:    "the control plane module's image_tag default",
		pattern: regexp.MustCompile(`(?s)variable\s+"image_tag"\s*\{.*?default\s*=\s*"([^"]+)"`),
		kind:    live,
	},
	{
		file:    "deploy/helm/antifailure-control-plane/Chart.yaml",
		what:    "the chart's appVersion, which is what image.tag defaults to",
		pattern: regexp.MustCompile(`(?m)^appVersion:\s*"?([^"\s]+)"?\s*$`),
		kind:    released,
	},
}

var changelogHeading = regexp.MustCompile(`^##\s+(v[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.\-+]*)\s*$`)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}

	published, err := tags(*root)
	if err != nil {
		fail("reading the tag list: %v", err)
	}
	if len(published) == 0 {
		fail("git shows no tags in %s, so there is nothing to check a pin against.\n"+
			"  A shallow clone has no tags. Fetch them (`git fetch --tags`, or "+
			"fetch-tags: true on the checkout) rather than letting this pass over nothing.", *root)
	}

	pending, err := preparing(*root)
	if err != nil {
		fail("%v", err)
	}

	var problems []string
	for _, p := range pins {
		value, err := read(*root, p)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		switch {
		case published[value]:
			fmt.Printf("ok  %-24s %s\n", value, p.what)
		case p.kind == released && value == pending:
			fmt.Printf("ok  %-24s %s, the release being prepared\n", value, p.what)
		case p.kind == released:
			problems = append(problems, fmt.Sprintf(
				"%s names %s, which is neither a published tag nor %s, the release "+
					"at the top of the changelog.\n    %s",
				p.file, value, pending, "Somebody bumped it to a version nobody is cutting."))
		default:
			problems = append(problems, fmt.Sprintf(
				"%s names %s, which no tag publishes yet.\n"+
					"    That default is live: the maintenance container app job reads it with no\n"+
					"    ignore_changes, so the next apply from main pulls an image that does not\n"+
					"    exist. Bump it after the tag publishes, in its own commit, never with it.",
				p.file, value))
		}
	}

	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "\ntagsync: a version pin names something unpublished.\n")
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		os.Exit(1)
	}
	fmt.Printf("tagsync: every version pin names a tag that exists, or the release being prepared\n")
}

// read pulls one pin's value out of its file.
func read(root string, p pin) (string, error) {
	source, err := os.ReadFile(filepath.Join(root, p.file))
	if err != nil {
		return "", fmt.Errorf("%s is a version pin this check watches and cannot be read: %v", p.file, err)
	}
	found := p.pattern.FindAllStringSubmatch(string(source), -1)
	if len(found) != 1 {
		// Not a skip. A pattern that matches nothing is this check quietly
		// stopping, which is the failure it was written to prevent.
		return "", fmt.Errorf(
			"%s: found %d matches for %s, want exactly 1. Either the file changed shape "+
				"or this check is now reading nothing", p.file, len(found), p.what)
	}
	return found[0][1], nil
}

// tags is every tag git knows about, as a set.
func tags(root string) (map[string]bool, error) {
	cmd := exec.Command("git", "tag", "--list")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			set[name] = true
		}
	}
	return set, nil
}

// preparing is the version at the top of the changelog.
//
// The top rather than the highest, because the changelog is written newest
// first and sorting version strings correctly is a job nobody needs here.
func preparing(root string) (string, error) {
	file, err := os.Open(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return "", fmt.Errorf("reading CHANGELOG.md, which says which release is being prepared: %v", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if m := changelogHeading.FindStringSubmatch(scanner.Text()); m != nil {
			return m[1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("CHANGELOG.md has no `## vX.Y.Z` heading, so there is no release being prepared")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tagsync: "+format+"\n", args...)
	os.Exit(1)
}
