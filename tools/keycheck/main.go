// Command keycheck proves that no YAML file in this repository defines the
// same key twice in one mapping.
//
// The failure that earned it: examples/github-workflow.yml declared `seed` and
// `concurrency` twice each in its workflow_dispatch inputs. YAML resolves that
// by keeping the last definition, so the first pair's descriptions were text
// nobody would ever see, sitting in the file this product hands a new user as
// the worked example of how to wire it into CI. Nothing failed. The workflow
// parsed, GitHub accepted it, the example rendered, and the two descriptions
// were simply gone. A defect that produces no error and no wrong answer, only
// a quietly discarded half of a file, is the kind that survives every review.
//
// It is not a cosmetic class. The same shape in a workflow silently drops a
// `permissions` block, an `env` entry or an `if` guard, and the run that
// results looks exactly like the run somebody intended.
//
// WHY THIS WALKS THE NODE TREE RATHER THAN DECODING.
//
// gopkg.in/yaml.v3 does report a duplicate when a document is decoded into a
// Go map, and that is the obvious implementation. It is the wrong one here for
// two reasons, and both are the difference between a check that answers this
// question and one that answers a nearby question.
//
// The first is that decoding stops. An unmarshal error carries the first
// duplicate it met and abandons the rest of the document, so a file with four
// of these reports one, gets fixed once, and comes back. This walk reports
// every one of them in every file in a single pass.
//
// The second is that decoding fails for reasons that have nothing to do with
// duplicate keys. Half the YAML here is a Helm template or a GitHub expression,
// and a decode that errors on a type mismatch or an unknown tag is
// indistinguishable, from the caller's side, from a decode that errored on a
// duplicate. A check that cannot tell its own finding from an unrelated parse
// failure is a check that will one day be silenced wholesale. Parsing to a
// yaml.Node succeeds on anything syntactically well formed and leaves the
// judgement here.
//
// WHAT IS DELIBERATELY OUT OF SCOPE. Keys that repeat across sibling documents
// in a multi document file are not duplicates; each document is its own
// mapping and each is walked separately. A merge key is a key like any other
// and repeating it in one mapping is the same defect, so it is not special
// cased.
//
// HELM TEMPLATES ARE RENDERED RATHER THAN SKIPPED, and this is the half of the
// gate that took the longest to get right. A chart template is Go template
// source, not YAML: gopkg.in/yaml.v3 cannot parse one, so the first version of
// this command reported twelve unparseable files, checked the other forty
// three, and printed a summary that named fifty five. That summary is the
// defect this repository keeps finding in its own instruments. A check that
// silently covers four fifths of what it claims is worse than one that covers
// none, because the number reads as coverage.
//
// Nothing else catches it either. `helm lint` was pointed at a chart whose
// service.yaml defined `type` twice and it returned "1 chart(s) linted, 0
// chart(s) failed". A duplicate key survives rendering intact, so `helm
// template` output holds it and can be parsed. That is what this does.
//
// If helm is not installed this command FAILS rather than skipping the charts.
// Skipping would restore exactly the silence above, one environment variable
// further away.
//
// One default render is not coverage either. Optional resources disappear
// from it, so three valid profiles exercise inline and external credentials,
// every maintenance mode, autoscaling, ingress and sparse configuration. Helm
// names the source template above every rendered document. The union of those
// names must contain every authored YAML template before this gate may pass.
//
// There are no exemptions and there is no exemption file, because unlike a
// naming rule or a prose rule this has no defensible exception: a mapping that
// defines a key twice has thrown one of them away, and no reason makes that
// intended.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// finding is one key that a later key in the same mapping shadowed.
type finding struct {
	File       string
	Line       int
	Key        string
	First      int
	Document   string
	Path       string
	Occurrence int
	Profiles   []string
}

func (f finding) String() string {
	line := fmt.Sprintf("%s:%d", f.File, f.Line)
	first := fmt.Sprintf("line %d", f.First)
	if len(f.Profiles) > 0 {
		line = fmt.Sprintf("%s: rendered line %d", f.File, f.Line)
		first = fmt.Sprintf("rendered line %d", f.First)
	}
	where := ""
	if f.Document != "" {
		where = " in " + f.Document
	}
	if f.Path != "" {
		where += " at " + f.Path
	}
	profiles := ""
	if len(f.Profiles) > 0 {
		profiles = " Profiles: " + strings.Join(f.Profiles, ", ") + "."
	}
	return fmt.Sprintf("%s: %q was already defined at %s%s. YAML keeps this one, so the earlier definition has no effect.%s",
		line, f.Key, first, where, profiles)
}

type findingIdentity struct {
	File       string
	Document   string
	Path       string
	Key        string
	Occurrence int
}

type chartScan struct {
	Findings  []finding
	Templates []string
}

type helmProfile struct {
	Name    string
	Release string
	Values  string
}

type chartRenderer func(root, chart string, profile helmProfile) ([]byte, error)

// skipped names directories that hold code this repository did not write.
var skipped = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "build": true,
	".next": true, "vendor": true, "testdata": true,
}

func main() {
	root := flag.String("root", ".", "directory to scan")
	flag.Parse()

	charts, err := findCharts(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keycheck:", err)
		os.Exit(2)
	}

	files, err := collect(*root, charts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keycheck:", err)
		os.Exit(2)
	}

	var found []finding
	var unread []string
	for _, f := range files {
		hits, scanErr := scan(*root, f)
		if scanErr != nil {
			// A file this could not read is not a clean file. It is recorded
			// and it fails the run, because "no duplicates" said about
			// something nothing parsed is the silence this gate exists to
			// remove.
			fmt.Fprintf(os.Stderr, "keycheck: %s could not be parsed: %v\n", f, scanErr)
			unread = append(unread, f)
			continue
		}
		found = append(found, hits...)
	}

	for _, c := range charts {
		result, renderErr := scanChart(*root, c)
		found = append(found, result.Findings...)
		if renderErr != nil {
			fmt.Fprintf(os.Stderr, "keycheck: the chart at %s could not be checked: %v\n", c, renderErr)
			unread = append(unread, c)
			continue
		}
	}
	found = mergeFindings(found)

	if len(found) == 0 && len(unread) == 0 {
		fmt.Printf("keycheck: %d YAML files and %d chart render(s), no key defined twice in one mapping\n",
			len(files), len(charts)*len(chartProfiles))
		return
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].File != found[j].File {
			return found[i].File < found[j].File
		}
		if found[i].Document != found[j].Document {
			return found[i].Document < found[j].Document
		}
		if found[i].Path != found[j].Path {
			return found[i].Path < found[j].Path
		}
		if found[i].Key != found[j].Key {
			return found[i].Key < found[j].Key
		}
		return found[i].Occurrence < found[j].Occurrence
	})
	for _, f := range found {
		fmt.Println(f)
	}
	if len(found) > 0 {
		fmt.Fprintf(os.Stderr, "\nkeycheck: %d key(s) silently discarded. YAML keeps the LAST definition, so the earlier one has no effect.\n", len(found))
	}
	if len(unread) > 0 {
		fmt.Fprintf(os.Stderr, "keycheck: %d file(s) or chart(s) were not checked completely, listed above. That is a failure, not a pass.\n", len(unread))
	}
	os.Exit(1)
}

// chartProfiles reach the three materially different shapes of the chart.
// Each profile is valid on its own. Their union renders every authored YAML
// template, including resources that the defaults deliberately leave off.
var chartProfiles = []helmProfile{
	{
		Name:    "rich inline cronjob",
		Release: "keycheck-inline",
		Values: `database:
  url: keycheck
  migrationUrl: keycheck
github:
  clientId: keycheck
  clientSecret: keycheck
  redirectUri: https://keycheck.invalid/cb
nameOverride: profile
image:
  digest: ""
  pullSecrets:
    - name: keycheck-pull
bootstrap:
  enabled: true
maintenance:
  mode: cronjob
config:
  appBaseUrl: http://keycheck.invalid
  insecureCookies: true
  operatorSetsPlan: true
  signinAllowlist: [keycheck]
  eventRetentionMonths: "2"
  eventArchiveDir: /archive
service:
  annotations:
    keycheck.service: covered
ingress:
  enabled: false
autoscaling:
  enabled: false
podDisruptionBudget:
  enabled: true
networkPolicy:
  enabled: true
  ingressFrom: []
serviceAccount:
  create: true
  name: ""
  annotations:
    keycheck.service-account: covered
podAnnotations:
  keycheck.pod-annotation: covered
podLabels:
  keycheck.pod-label: covered
nodeSelector:
  kubernetes.io/os: linux
tolerations:
  - key: keycheck
    operator: Exists
affinity:
  nodeAffinity: {}
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: kubernetes.io/hostname
    whenUnsatisfiable: ScheduleAnyway
    labelSelector: {}
`,
	},
	{
		Name:    "external secret autoscaled inProcess with ingress",
		Release: "keycheck-external",
		Values: `database:
  existingSecret: keycheck-database
github:
  existingSecret: keycheck-github
fullnameOverride: keycheck-fullname
image:
  digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
bootstrap:
  enabled: true
maintenance:
  mode: inProcess
config:
  signinAllowlist: []
autoscaling:
  enabled: true
ingress:
  enabled: true
  className: keycheck
  annotations:
    keycheck.ingress: covered
  tls:
    - secretName: keycheck-tls
      hosts: [cp.example.com]
networkPolicy:
  enabled: true
  ingressFrom:
    - namespaceSelector:
        matchLabels:
          keycheck.network: allowed
serviceAccount:
  create: false
  name: keycheck-external
`,
	},
	{
		Name:    "sparse maintenance off",
		Release: "antifailure-control-plane",
		Values: `database:
  url: keycheck
  migrationUrl: ""
github:
  clientId: keycheck
  clientSecret: keycheck
  redirectUri: https://keycheck.invalid/cb
nameOverride: ""
fullnameOverride: ""
image:
  tag: v1.1.1-keycheck
bootstrap:
  enabled: false
maintenance:
  mode: "off"
config:
  signinAllowlist: null
replicaCount: 1
podDisruptionBudget:
  enabled: true
ingress:
  enabled: true
  className: ""
  annotations: {}
  tls: []
networkPolicy:
  enabled: false
  ingressFrom: []
serviceAccount:
  create: false
  name: ""
  annotations: {}
service:
  annotations: {}
podAnnotations: {}
podLabels: {}
nodeSelector: {}
tolerations: []
affinity: {}
topologySpreadConstraints: []
`,
	},
}

// findCharts returns the directory of every Helm chart under root.
func findCharts(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipped[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "Chart.yaml" {
			return nil
		}
		rel, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// scanChart renders every profile and reports each authored duplicate once.
func scanChart(root, chart string) (chartScan, error) {
	if _, err := exec.LookPath("helm"); err != nil {
		return chartScan{}, fmt.Errorf("helm is not installed, so this chart's templates cannot be read: %w", err)
	}
	return scanChartWith(root, chart, renderChartProfile)
}

func scanChartWith(root, chart string, render chartRenderer) (chartScan, error) {
	return scanChartProfilesWith(root, chart, chartProfiles, render)
}

func scanChartProfilesWith(root, chart string, profiles []helmProfile, render chartRenderer) (chartScan, error) {
	expected, err := chartTemplates(root, chart)
	if err != nil {
		return chartScan{}, err
	}

	result := chartScan{}
	reached := map[string]bool{}
	var failures []error
	for _, profile := range profiles {
		if profileErr := validateProfile(profile); profileErr != nil {
			failures = append(failures, fmt.Errorf("profile %q has invalid values: %w", profile.Name, profileErr))
			continue
		}
		out, renderErr := render(root, chart, profile)
		if renderErr != nil {
			failures = append(failures, fmt.Errorf("profile %q could not be rendered: %w", profile.Name, renderErr))
			continue
		}
		hits, templates, parseErr := parseRendered(chart, profile.Name, out)
		if parseErr != nil {
			failures = append(failures, fmt.Errorf("profile %q produced unreadable YAML: %w", profile.Name, parseErr))
			continue
		}
		result.Findings = append(result.Findings, hits...)
		for _, template := range templates {
			reached[template] = true
		}
	}

	result.Findings = mergeFindings(result.Findings)
	for template := range reached {
		result.Templates = append(result.Templates, template)
	}
	sort.Strings(result.Templates)

	var missing []string
	for _, template := range expected {
		if !reached[template] {
			missing = append(missing, template)
		}
	}
	if len(missing) > 0 {
		failures = append(failures, fmt.Errorf("the render profiles reached no document from %s", strings.Join(missing, ", ")))
	}
	return result, errors.Join(failures...)
}

func validateProfile(profile helmProfile) error {
	hits, err := parse("profile values", []byte(profile.Values))
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		return nil
	}
	var messages []string
	for _, hit := range hits {
		messages = append(messages, hit.String())
	}
	return fmt.Errorf("%d key(s) are defined more than once: %s", len(hits), strings.Join(messages, "; "))
}

func renderChartProfile(root, chart string, profile helmProfile) ([]byte, error) {
	cmd := exec.Command("helm", "template", profile.Release, chart, "-f", "-")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(profile.Values)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = "helm returned no diagnostic"
		}
		return nil, fmt.Errorf("%w: %s", err, detail)
	}
	return out, nil
}

// chartTemplates returns every authored YAML template. NOTES.txt and helper
// templates are not YAML documents and are outside this gate's claim.
func chartTemplates(root, chart string) ([]string, error) {
	chartRoot := filepath.Join(root, chart)
	templateRoot := filepath.Join(chartRoot, "templates")
	var out []string
	err := filepath.WalkDir(templateRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		rel, relErr := filepath.Rel(chartRoot, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// collect returns every YAML file under root that this repository wrote,
// except the template directories of the charts, which are rendered instead.
func collect(root string, charts []string) ([]string, error) {
	templates := map[string]bool{}
	for _, c := range charts {
		templates[filepath.Join(c, "templates")] = true
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipped[d.Name()] {
				return fs.SkipDir
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil && templates[rel] {
				return fs.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yml", ".yaml":
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// scan reads one file and returns every shadowed key in it.
func scan(root, rel string) ([]finding, error) {
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return nil, err
	}
	return parse(rel, b)
}

// parse walks every document in one YAML stream.
func parse(rel string, b []byte) ([]finding, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	var out []finding
	document := 0
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, err
		}
		if len(doc.Content) == 0 {
			continue
		}
		document++
		hits := walk(rel, &doc)
		identity := documentIdentity(doc.Content[0], document)
		for i := range hits {
			hits[i].Document = identity
		}
		out = append(out, hits...)
	}
}

// parseRendered reads Helm's source markers as well as the YAML. Those markers
// let findings survive line movement between profiles and prove which authored
// templates produced at least one document.
func parseRendered(chart, profile string, b []byte) ([]finding, []string, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	sources := renderedSources(b)
	var found []finding
	var templates []string
	document := 0
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return found, uniqueStrings(templates), nil
			}
			return nil, nil, err
		}
		if len(doc.Content) == 0 {
			continue
		}
		document++
		source := sourceFromNode(&doc)
		if source == "" && document <= len(sources) {
			source = sources[document-1]
		}
		template, err := normalizeTemplateSource(source)
		if err != nil {
			return nil, nil, fmt.Errorf("document %d: %w", document, err)
		}
		templates = append(templates, template)

		hits := walk(filepath.ToSlash(filepath.Join(chart, template)), &doc)
		identity := documentIdentity(doc.Content[0], document)
		for i := range hits {
			hits[i].Document = identity
			hits[i].Profiles = []string{profile}
		}
		found = append(found, hits...)
	}
}

func renderedSources(b []byte) []string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# Source:") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "# Source:")))
		}
	}
	return out
}

func sourceFromNode(n *yaml.Node) string {
	comments := []string{n.HeadComment}
	if len(n.Content) > 0 {
		comments = append(comments, n.Content[0].HeadComment)
	}
	for _, comment := range comments {
		for _, line := range strings.Split(comment, "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
			if strings.HasPrefix(line, "Source:") {
				return strings.TrimSpace(strings.TrimPrefix(line, "Source:"))
			}
		}
	}
	return ""
}

func normalizeTemplateSource(source string) (string, error) {
	source = filepath.ToSlash(strings.TrimSpace(source))
	at := strings.Index(source, "templates/")
	if at < 0 {
		if source == "" {
			return "", errors.New("the rendered document has no Helm source marker")
		}
		return "", fmt.Errorf("the Helm source marker %q names no template", source)
	}
	return source[at:], nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range in {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func mergeFindings(in []finding) []finding {
	byIdentity := map[findingIdentity]int{}
	var out []finding
	for _, hit := range in {
		identity := findingIdentity{
			File: hit.File, Document: hit.Document, Path: hit.Path,
			Key: hit.Key, Occurrence: hit.Occurrence,
		}
		if at, ok := byIdentity[identity]; ok {
			out[at].Profiles = appendUnique(out[at].Profiles, hit.Profiles...)
			continue
		}
		byIdentity[identity] = len(out)
		hit.Profiles = appendUnique(nil, hit.Profiles...)
		out = append(out, hit)
	}
	return out
}

func appendUnique(current []string, values ...string) []string {
	for _, value := range values {
		seen := false
		for _, existing := range current {
			if existing == value {
				seen = true
				break
			}
		}
		if !seen {
			current = append(current, value)
		}
	}
	return current
}

func documentIdentity(root *yaml.Node, ordinal int) string {
	if root.Kind != yaml.MappingNode {
		return fmt.Sprintf("document %d", ordinal)
	}
	apiVersion := mappingScalar(root, "apiVersion")
	kind := mappingScalar(root, "kind")
	metadata := mappingNode(root, "metadata")
	name := mappingScalar(metadata, "name")
	namespace := mappingScalar(metadata, "namespace")
	if kind == "" || name == "" {
		return fmt.Sprintf("document %d", ordinal)
	}
	identity := kind + " " + name
	if namespace != "" {
		identity = kind + " " + namespace + "/" + name
	}
	if apiVersion != "" {
		identity = apiVersion + " " + identity
	}
	return identity
}

func mappingNode(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func mappingScalar(n *yaml.Node, key string) string {
	value := mappingNode(n, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return value.Value
}

// walk descends one document, reporting duplicates in every mapping it holds.
func walk(rel string, n *yaml.Node) []finding {
	return walkAt(rel, n, "$")
}

func walkAt(rel string, n *yaml.Node, path string) []finding {
	var out []finding
	switch n.Kind {
	case yaml.MappingNode:
		// A mapping's Content alternates key, value, key, value. Stepping by
		// two over pairs rather than iterating every child is what keeps a
		// value that happens to equal a key's name from being mistaken for one.
		type seenKey struct {
			line  int
			count int
		}
		seen := map[string]seenKey{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			prior := seen[k.Value]
			prior.count++
			if prior.line == 0 {
				prior.line = k.Line
			} else {
				out = append(out, finding{
					File: rel, Line: k.Line, Key: k.Value, First: prior.line,
					Path: path, Occurrence: prior.count,
				})
			}
			seen[k.Value] = prior
			out = append(out, walkAt(rel, n.Content[i+1], mappingPath(path, k.Value))...)
		}
	case yaml.SequenceNode:
		for i, child := range n.Content {
			out = append(out, walkAt(rel, child, path+sequenceSegment(child, i))...)
		}
	default:
		for _, child := range n.Content {
			out = append(out, walkAt(rel, child, path)...)
		}
	}
	return out
}

func mappingPath(parent, key string) string {
	return fmt.Sprintf("%s[%q]", parent, key)
}

func sequenceSegment(n *yaml.Node, index int) string {
	for _, key := range []string{"name", "host", "path", "kind", "port", "type"} {
		if value := mappingScalar(n, key); value != "" {
			return fmt.Sprintf("[%s=%q]", key, value)
		}
	}
	return fmt.Sprintf("[%d]", index)
}
