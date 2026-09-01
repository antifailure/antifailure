// Command releasecheck proves the release workflow publishes what it signs.
//
// The signing and bill of materials half of .github/workflows/release.yml has
// never run. v0.1.0 and v0.1.1 both predate it, so the first execution of
// `cosign sign-blob` and of the verify-and-tamper step is the first real tag,
// and the first time anybody sees the result is after it is published.
//
// Three things can go wrong there that no step in the workflow would notice,
// and every one of them is silent in the direction that matters: the release
// goes out looking finished and is short a file, or carries a signature nobody
// can fetch.
//
//   - softprops/action-gh-release defaults `fail_on_unmatched_files` to FALSE.
//     A pattern in `files:` that matches nothing prints "Pattern ... does not
//     match any files" to the log and the release publishes anyway, green.
//     Read out of the action's own source at the pinned commit: src/main.ts
//     warns and continues unless that input is set. So the promise this
//     pipeline makes, all nine assets or none, is not enforced by the step
//     that keeps it.
//   - A path signed in one place and published from another. The bundle is
//     written, the signing step is green, and the asset is not on the release.
//   - Keyless signing without `id-token: write`. cosign cannot ask GitHub for
//     an OIDC token without it, and the workflow declares `contents: read` at
//     the top with per-job grants, so the grant is a thing that can be dropped
//     from one job while every other job still looks right.
//
// None of the three is visible in a diff that reads correctly line by line,
// which is why this reads the workflow as what each step writes against what
// the publishing step names, rather than as text somebody proofreads.
//
// What it deliberately does NOT check: that every bundle signed is also
// verified. The verify step loops over blob names in shell and builds the
// bundle path from a variable, so there is no literal path to match, and a
// rule shaped for it would either miss it or invent findings about it. That
// half of the class stays open, and saying so is better than a check whose
// coverage is assumed.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	name := flag.String("workflow", filepath.Join(".github", "workflows", "release.yml"),
		"the release workflow, relative to root")
	flag.Parse()
	root := "."
	if args := flag.Args(); len(args) > 0 {
		root = args[0]
	}

	path := filepath.Join(root, *name)
	source, err := os.ReadFile(path)
	if err != nil {
		fail("reading %s: %v", *name, err)
	}

	problems, err := check(source, os.Stdout)
	if err != nil {
		fail("%s: %v", path, err)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "\nreleasecheck: %s would publish a release nobody can verify.\n", path)
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		fmt.Fprintf(os.Stderr, "\nThe signing half of this workflow has never run. The first tag is its "+
			"first execution, and anything it gets wrong is already published.\n")
		os.Exit(1)
	}
}

// The actions this reasons about, by the owner and name half of `uses:`.
// Matched without the version so that the check survives a pin bump, which is
// a thing that should happen often and should not need this file edited.
const (
	publisher = "softprops/action-gh-release"
	installer = "sigstore/cosign-installer"
)

type workflow struct {
	Permissions yaml.Node       `yaml:"permissions"`
	Jobs        map[string]*job `yaml:"jobs"`
}

type job struct {
	Permissions yaml.Node `yaml:"permissions"`
	Steps       []*step   `yaml:"steps"`
}

// step reads the four fields that decide any of this. `with` is a mapping of
// scalars in every step here, but a value can be a bool (`upload-artifact:
// false`) as easily as a string, so it decodes through Node and is read as
// text. Decoding it as map[string]string throws on the first bool and takes
// the whole document down with it, which is the shape of failure that discards
// a collection over one element.
type step struct {
	Name string               `yaml:"name"`
	Uses string               `yaml:"uses"`
	Run  string               `yaml:"run"`
	With map[string]yaml.Node `yaml:"with"`
}

// input returns a `with:` value as text, and whether it was given at all. The
// two are different: a missing `fail_on_unmatched_files` is the defect, and an
// explicit `false` is the same defect written down. Both have to be reportable
// and neither can be read as the other.
func (s *step) input(key string) (string, bool) {
	n, ok := s.With[key]
	if !ok {
		return "", false
	}
	return n.Value, true
}

// writes is everything a step could name a path in: the shell it runs and
// every value it hands an action. `output-file: dist/sbom.spdx.json` is a path
// this release depends on and it is not in any `run:`, so reading only the
// shell would report the bill of materials as published from nowhere.
func (s *step) writes() string {
	var b strings.Builder
	b.WriteString(s.Run)
	keys := make([]string, 0, len(s.With))
	for k := range s.With {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteByte('\n')
		b.WriteString(s.With[k].Value)
	}
	return b.String()
}

func (s *step) label() string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Uses != "":
		return s.Uses
	default:
		return "an unnamed step"
	}
}

func (s *step) uses(action string) bool { return strings.HasPrefix(s.Uses, action+"@") }

// check returns one string per thing wrong with the workflow, and an error
// only when it could not reach an opinion at all.
//
// The two are kept apart for the same reason sbomcheck keeps them apart: a
// problem is a release that would go out wrong, and an error is not knowing,
// which is the same verdict and a different message to whoever has to fix it.
func check(source []byte, out io.Writer) ([]string, error) {
	var wf workflow
	if err := yaml.Unmarshal(source, &wf); err != nil {
		return nil, fmt.Errorf("reading the workflow: %w", err)
	}
	if len(wf.Jobs) == 0 {
		return nil, fmt.Errorf("it declares no jobs, so either this is not the release workflow " +
			"or the shape changed and this command is now reading nothing")
	}

	jobName, publishing, index, err := publishStep(wf)
	if err != nil {
		return nil, err
	}
	publish := wf.Jobs[jobName]

	var problems []string
	problems = append(problems, checkSigningToken(wf, out)...)
	problems = append(problems, checkUnmatchedFiles(publishing, out)...)

	assets := assetPatterns(publishing)
	if len(assets) == 0 {
		return nil, fmt.Errorf("the %q step names no files, so this release publishes the notes and "+
			"nothing else", publishing.label())
	}
	problems = append(problems, checkAssetsAreNamedEarlier(publish, index, assets, out)...)
	problems = append(problems, checkBundlesArePublished(publish, assets, out)...)

	if len(problems) == 0 {
		fmt.Fprintf(out, "releasecheck: %d assets, every one named before it is published, "+
			"every signature published, and the signing job holds an OIDC token\n", len(assets))
	}
	return problems, nil
}

// publishStep finds the one step that creates the release, and refuses to
// guess. Two of them would mean two releases from one tag and this command
// would be reasoning about whichever it saw first; none means the workflow
// stopped publishing, which is worth stopping for rather than passing over.
func publishStep(wf workflow) (string, *step, int, error) {
	names := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		names = append(names, name)
	}
	sort.Strings(names)

	var found []string
	var out *step
	var outJob string
	var outIndex int
	for _, name := range names {
		for i, s := range wf.Jobs[name].Steps {
			if s.uses(publisher) {
				found = append(found, name+" step "+s.label())
				out, outJob, outIndex = s, name, i
			}
		}
	}
	switch len(found) {
	case 0:
		return "", nil, 0, fmt.Errorf("no step uses %s, so nothing here publishes a release", publisher)
	case 1:
		return outJob, out, outIndex, nil
	default:
		return "", nil, 0, fmt.Errorf("%d steps use %s (%s) and one tag cannot publish twice",
			len(found), publisher, strings.Join(found, ", "))
	}
}

// checkSigningToken requires every job that signs to hold the token signing
// needs, from its OWN grant or from the workflow's, whichever GitHub would
// actually give it.
//
// A job-level `permissions:` block REPLACES the workflow-level one rather than
// adding to it, so a job that grants `contents: write` and forgets id-token
// has silently taken away a grant the top of the file appears to make. That is
// the whole reason this resolves the two rather than looking for the string.
func checkSigningToken(wf workflow, out io.Writer) []string {
	names := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		names = append(names, name)
	}
	sort.Strings(names)

	var problems []string
	for _, name := range names {
		j := wf.Jobs[name]
		if !signs(j) {
			continue
		}
		granted := effectivePermissions(wf, j)
		if granted["id-token"] != "write" {
			problems = append(problems, fmt.Sprintf(
				"the %q job signs and its effective permissions are %s. Keyless signing asks GitHub "+
					"for a short lived OIDC token and cannot get one without `id-token: write`, so "+
					"every cosign command in it fails and the release publishes nothing",
				name, describe(granted)))
			continue
		}
		fmt.Fprintf(out, "ok  the %q job signs and holds `id-token: write`\n", name)
	}
	return problems
}

// signs reports whether a job runs cosign, by either installing it or calling
// it. Both, because a job that installs and never calls needs no token, and a
// job that calls without installing is a different bug this would still be
// right to ask about.
func signs(j *job) bool {
	for _, s := range j.Steps {
		if s.uses(installer) || cosignCall.MatchString(s.Run) {
			return true
		}
	}
	return false
}

var cosignCall = regexp.MustCompile(`(?m)(^|[\s;&|(])cosign\s`)

// effectivePermissions resolves what GitHub would hand the job's token.
func effectivePermissions(wf workflow, j *job) map[string]string {
	if j.Permissions.Kind != 0 {
		return permissions(j.Permissions)
	}
	return permissions(wf.Permissions)
}

// permissions reads either shape the key takes: a mapping of scope to level,
// or the `write-all` / `read-all` scalar shorthand.
func permissions(n yaml.Node) map[string]string {
	switch n.Kind {
	case yaml.MappingNode:
		out := map[string]string{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = n.Content[i+1].Value
		}
		return out
	case yaml.ScalarNode:
		if n.Value == "write-all" {
			return map[string]string{"id-token": "write", "contents": "write"}
		}
		return map[string]string{}
	default:
		return nil
	}
}

func describe(granted map[string]string) string {
	if granted == nil {
		return "GitHub's default, which grants no id-token at all"
	}
	if len(granted) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(granted))
	for k := range granted {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+granted[k])
	}
	return strings.Join(parts, ", ")
}

// checkUnmatchedFiles is the one that would have let a short release publish.
func checkUnmatchedFiles(publishing *step, out io.Writer) []string {
	value, given := publishing.input("fail_on_unmatched_files")
	if !given {
		return []string{fmt.Sprintf(
			"the %q step does not set `fail_on_unmatched_files`, and %s defaults it to false. "+
				"A pattern in `files:` that matches nothing is a warning in the log and a green "+
				"step, so the release publishes short and looks finished",
			publishing.label(), publisher)}
	}
	if value != "true" {
		return []string{fmt.Sprintf(
			"the %q step sets `fail_on_unmatched_files: %s`. A release missing an asset is worse "+
				"than one that failed to publish, because nobody goes looking for a file they were "+
				"not told was absent",
			publishing.label(), value)}
	}
	fmt.Fprintf(out, "ok  a `files:` pattern that matches nothing stops the release\n")
	return nil
}

// assetPatterns is the `files:` list, one pattern per line, comments dropped.
func assetPatterns(publishing *step) []string {
	value, _ := publishing.input("files")
	var out []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// glob metacharacters, which decide whether a pattern names a file or a set.
const globMeta = "*?["

// checkAssetsAreNamedEarlier requires every asset to be named by a step that
// runs before the one publishing it.
//
// Named rather than proven to exist. Nothing static can run the workflow, so
// the strongest available question is whether the path the release publishes
// is the path the earlier steps talk about. That is enough for the failure
// this is here for, which is two spellings of one file.
//
// This is the "an SBOM written to one path and signed from another" failure,
// one step further down: written to one path and PUBLISHED from another. The
// signing step is green either way, because it signed a file that exists.
//
// A glob is checked by its directory rather than by its expansion, because
// `dist/*.tar.gz` names four files whose names this cannot know. Asking
// whether anything writes into `dist/` is the strongest question available
// about a pattern, and it is still enough to catch a directory renamed on one
// side of the workflow and not the other.
func checkAssetsAreNamedEarlier(publish *job, index int, assets []string, out io.Writer) []string {
	var earlier strings.Builder
	for i, s := range publish.Steps {
		if i >= index {
			break
		}
		earlier.WriteString(s.writes())
		earlier.WriteByte('\n')
	}
	corpus := earlier.String()

	var problems []string
	for _, pattern := range assets {
		if meta := strings.IndexAny(pattern, globMeta); meta >= 0 {
			slash := strings.LastIndex(pattern[:meta], "/")
			if slash < 0 {
				// A bare glob at the workspace root names no directory to ask
				// about. Reported rather than skipped: an unanswerable pattern
				// in this list is worth a person's attention, and a silent skip
				// is how a check starts covering less than its name says.
				problems = append(problems, fmt.Sprintf(
					"`%s` is a glob with no directory, so nothing here can tell whether any step "+
						"names what it would match", pattern))
				continue
			}
			dir := pattern[:slash+1]
			if !strings.Contains(corpus, dir) {
				problems = append(problems, fmt.Sprintf(
					"`%s` is published and no step before it names anything in %s",
					pattern, dir))
				continue
			}
			fmt.Fprintf(out, "ok  %-36s named into %s\n", pattern, dir)
			continue
		}
		if !names(corpus, pattern) {
			problems = append(problems, fmt.Sprintf(
				"`%s` is published and no step before it names that path. A file signed at one "+
					"path and published from another leaves every step green and the asset absent",
				pattern))
			continue
		}
		fmt.Fprintf(out, "ok  %-36s named before it is published\n", pattern)
	}
	return problems
}

// checkBundlesArePublished is the same question from the other side: a
// signature written and never published is a verification instruction that
// 404s, and the signing step is green because it signed something.
func checkBundlesArePublished(publish *job, assets []string, out io.Writer) []string {
	published := map[string]bool{}
	for _, a := range assets {
		published[a] = true
	}

	seen := map[string]bool{}
	var order []string
	for _, s := range publish.Steps {
		for _, m := range bundleFlag.FindAllStringSubmatch(s.Run, -1) {
			path := m[1]
			// A path built from a shell variable, as the verify step's loop
			// builds one, has no literal to compare. Skipped on purpose and
			// named in this command's doc comment, because a rule that guessed
			// at it would report a file that does not exist.
			if strings.ContainsAny(path, `$"'`) || seen[path] {
				continue
			}
			seen[path] = true
			order = append(order, path)
		}
	}

	var problems []string
	for _, path := range order {
		if !published[path] {
			problems = append(problems, fmt.Sprintf(
				"`%s` is signed and is not in the published `files:` list, so the bundle exists on "+
					"the runner and nobody can fetch it", path))
			continue
		}
		fmt.Fprintf(out, "ok  %-36s signed and published\n", path)
	}
	return problems
}

var bundleFlag = regexp.MustCompile(`--bundle[=\s]+(\S+)`)

// names reports whether the corpus contains the path as a whole path rather
// than as the head of a longer one.
//
// The boundary is the point. `dist/sbom.spdx.json` is a prefix of
// `dist/sbom.spdx.json.sigstore.json`, so a plain substring test would report
// the bill of materials as produced by the line that only signs its bundle,
// and the check would pass over exactly the release that had lost it.
func names(corpus, path string) bool {
	for i := 0; ; {
		j := strings.Index(corpus[i:], path)
		if j < 0 {
			return false
		}
		end := i + j + len(path)
		if end == len(corpus) || !pathByte(corpus[end]) {
			return true
		}
		i += j + 1
	}
}

func pathByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '.', c == '_', c == '-', c == '/':
		return true
	default:
		return false
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "releasecheck: "+format+"\n", args...)
	os.Exit(1)
}
